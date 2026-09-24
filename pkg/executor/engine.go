package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// ToolDispatcher dispatches tool node execution.
type ToolDispatcher interface {
	Dispatch(ctx context.Context, tool string, args map[string]interface{}) (map[string]interface{}, error)
}

// EngineOption configures the Engine via functional options.
type EngineOption func(*Engine)

// WithMaxConcurrency sets the maximum number of concurrent node executions.
func WithMaxConcurrency(n int) EngineOption {
	return func(e *Engine) {
		if n > 0 {
			e.MaxConcurrency = n
		}
	}
}

// WithDecider sets the System 1 Decision Daemon client.
func WithDecider(d Decider) EngineOption {
	return func(e *Engine) { e.decider = d }
}

// WithExtractor sets the zero-shot span extractor client.
func WithExtractor(ex Extractor) EngineOption {
	return func(e *Engine) { e.extractor = ex }
}

// WithToolDispatcher sets a custom tool dispatcher for in-process tool execution.
func WithToolDispatcher(td ToolDispatcher) EngineOption {
	return func(e *Engine) { e.toolDispatcher = td }
}

// Engine is the System 1 Graph Call runtime.
type Engine struct {
	MaxConcurrency int
	decider        Decider
	extractor      Extractor
	toolDispatcher ToolDispatcher
}

// NewEngine creates a new graph execution engine with the given options.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		MaxConcurrency: 4, // sensible default
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Execute runs the graph using Kahn's topological sort with concurrent bounded dispatch.
func (e *Engine) Execute(ctx context.Context, g *Graph) (*ExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Build adjacency and in-degree maps
	nodeMap := make(map[string]*Node, len(g.Nodes))
	inDegree := make(map[string]int, len(g.Nodes))
	dependents := make(map[string][]string) // nodeID → list of nodes that depend on it

	for i := range g.Nodes {
		n := &g.Nodes[i]
		nodeMap[n.ID] = n
		inDegree[n.ID] = len(n.DependsOn)
		for _, dep := range n.DependsOn {
			dependents[dep] = append(dependents[dep], n.ID)
		}
	}

	// Validate all dependencies reference existing nodes
	for _, n := range g.Nodes {
		for _, dep := range n.DependsOn {
			if _, ok := nodeMap[dep]; !ok {
				return nil, fmt.Errorf("node %q depends on unknown node %q", n.ID, dep)
			}
		}
	}

	// Kahn's algorithm: find initial ready set (in-degree 0)
	var ready []string
	for id, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, id)
		}
	}

	outputs := make(map[string]NodeOutput)
	var mu sync.Mutex
	processed := 0

	// Process nodes in topological order
	for len(ready) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Dispatch current ready batch with bounded concurrency
		batch := ready
		ready = nil

		concurrency := e.MaxConcurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		if concurrency > len(batch) {
			concurrency = len(batch)
		}

		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for _, id := range batch {
			wg.Add(1)
			go func(nodeID string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				node := nodeMap[nodeID]
				output := e.executeNode(ctx, node, outputs, &mu)

				mu.Lock()
				outputs[nodeID] = output
				processed++
				mu.Unlock()
			}(id)
		}

		wg.Wait()

		// Find newly ready nodes
		mu.Lock()
		for _, id := range batch {
			for _, depID := range dependents[id] {
				inDegree[depID]--
				if inDegree[depID] == 0 {
					// Check if all dependencies completed successfully or
					// if the node allows failed dependencies
					depNode := nodeMap[depID]
					blocked := false
					for _, dep := range depNode.DependsOn {
						depOutput, exists := outputs[dep]
						if !exists || (depOutput.Status != "completed" && !depNode.AllowFailedDependencies) {
							blocked = true
							break
						}
					}
					if blocked {
						outputs[depID] = NodeOutput{
							NodeID: depID,
							Status: "blocked",
						}
						processed++
						// Propagate blocking to transitive dependents
						e.propagateBlocked(depID, dependents, inDegree, outputs, nodeMap, &processed, &ready)
					} else {
						ready = append(ready, depID)
					}
				}
			}
		}
		mu.Unlock()
	}

	// Cycle detection: if we haven't processed all nodes, there's a cycle
	if processed < len(g.Nodes) {
		return nil, fmt.Errorf("graph contains a cycle: %d nodes could not be scheduled", len(g.Nodes)-processed)
	}

	// Determine overall status
	status := "completed"
	hasFailed := false
	for _, out := range outputs {
		if out.Status == "yielded" {
			status = "yielded"
			break
		}
		if out.Status == "failed" || out.Status == "blocked" {
			hasFailed = true
		}
	}
	if status == "completed" && hasFailed {
		status = "failed"
	}

	// Resolve returns
	returns := e.resolveReturns(g.Returns, outputs)

	return &ExecutionResult{
		TaskID:  g.TaskID,
		Status:  status,
		Outputs: outputs,
		Returns: returns,
	}, nil
}

// propagateBlocked marks all transitive dependents of a blocked node as blocked
// and increments the processed counter for each, preventing false cycle detection.
func (e *Engine) propagateBlocked(nodeID string, dependents map[string][]string, inDegree map[string]int, outputs map[string]NodeOutput, nodeMap map[string]*Node, processed *int, ready *[]string) {
	for _, depID := range dependents[nodeID] {
		if _, done := outputs[depID]; done {
			continue
		}
		inDegree[depID]--
		depNode := nodeMap[depID]
		if depNode.AllowFailedDependencies {
			// This node accepts failed dependencies — schedule it if ready
			if inDegree[depID] == 0 {
				*ready = append(*ready, depID)
			}
		} else {
			outputs[depID] = NodeOutput{
				NodeID: depID,
				Status: "blocked",
			}
			*processed++
			e.propagateBlocked(depID, dependents, inDegree, outputs, nodeMap, processed, ready)
		}
	}
}

// executeNode dispatches a single node based on its type.
func (e *Engine) executeNode(ctx context.Context, node *Node, outputs map[string]NodeOutput, mu *sync.Mutex) NodeOutput {
	// Resolve pointer references in args and input
	mu.Lock()
	resolvedArgs, err := ResolvePointers(node.Args, outputs)
	if err != nil {
		mu.Unlock()
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: fmt.Sprintf("pointer resolution failed: %v", err),
		}
	}
	resolvedInput, err := ResolvePointers(node.Input, outputs)
	if err != nil {
		mu.Unlock()
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: fmt.Sprintf("input pointer resolution failed: %v", err),
		}
	}
	mu.Unlock()

	switch node.Type {
	case NodeTypeTool:
		return e.executeTool(ctx, node, resolvedArgs)
	case NodeTypeDecision:
		return e.executeDecision(ctx, node, resolvedInput)
	case NodeTypeExtract:
		return e.executeExtract(ctx, node, resolvedInput)
	case NodeTypeGroup:
		return e.executeGroup(ctx, node, outputs, mu)
	default:
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: fmt.Sprintf("unknown node type: %q", node.Type),
		}
	}
}

// executeTool runs a tool node. If a custom ToolDispatcher is set, it delegates
// to it. Otherwise, it handles "bash" directly.
func (e *Engine) executeTool(ctx context.Context, node *Node, args map[string]interface{}) NodeOutput {
	tool := node.Tool

	// If a custom dispatcher is set and the tool is not bare "bash", delegate
	if e.toolDispatcher != nil && tool != "bash" {
		result, err := e.toolDispatcher.Dispatch(ctx, tool, args)
		if err != nil {
			return NodeOutput{
				NodeID:   node.ID,
				Status:   "failed",
				ExitCode: 1,
				Stderr:   err.Error(),
			}
		}
		return NodeOutput{
			NodeID: node.ID,
			Status: "completed",
			Data:   result,
		}
	}

	// Handle bash tool directly
	if tool == "bash" {
		return e.executeBash(ctx, node.ID, args)
	}

	// If we have a tool dispatcher, try it for any tool
	if e.toolDispatcher != nil {
		result, err := e.toolDispatcher.Dispatch(ctx, tool, args)
		if err != nil {
			return NodeOutput{
				NodeID:   node.ID,
				Status:   "failed",
				ExitCode: 1,
				Stderr:   err.Error(),
			}
		}
		return NodeOutput{
			NodeID: node.ID,
			Status: "completed",
			Data:   result,
		}
	}

	return NodeOutput{
		NodeID: node.ID,
		Status: "failed",
		Stderr: fmt.Sprintf("no dispatcher for tool %q", tool),
	}
}

// executeBash runs a sandboxed bash command.
func (e *Engine) executeBash(ctx context.Context, nodeID string, args map[string]interface{}) NodeOutput {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return NodeOutput{
			NodeID: nodeID,
			Status: "failed",
			Stderr: "bash tool requires 'command' argument",
		}
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return NodeOutput{
				NodeID:   nodeID,
				Status:   "failed",
				ExitCode: 1,
				Stderr:   err.Error(),
			}
		}
	}

	// Check accepted exit codes
	status := "completed"
	acceptedCodes := []int{0}
	if raw, ok := args["accepted_exit_codes"]; ok {
		if codes, ok := raw.([]interface{}); ok {
			acceptedCodes = make([]int, 0, len(codes))
			for _, c := range codes {
				switch v := c.(type) {
				case float64:
					acceptedCodes = append(acceptedCodes, int(v))
				case int:
					acceptedCodes = append(acceptedCodes, v)
				}
			}
		}
	}

	accepted := false
	for _, code := range acceptedCodes {
		if exitCode == code {
			accepted = true
			break
		}
	}
	if !accepted {
		status = "failed"
	}

	return NodeOutput{
		NodeID:   nodeID,
		Status:   status,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
}

// executeDecision runs a decision node using the configured Decider.
func (e *Engine) executeDecision(ctx context.Context, node *Node, resolvedInput map[string]interface{}) NodeOutput {
	if e.decider == nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: "no decider configured for decision node",
		}
	}

	if node.Question == nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: "decision node requires a 'question' field",
		}
	}

	req := &DecisionInput{
		QuestionType: node.Question.Type,
		Prompt:       node.Question.Prompt,
		Options:      node.Question.Options,
		State:        resolvedInput,
	}

	resp, err := e.decider.Decide(ctx, req)
	if err != nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: fmt.Sprintf("decision failed: %v", err),
		}
	}

	// Check acceptance criteria
	if node.Accept != nil && node.Accept.MinConfidence > 0 {
		if resp.Confidence < node.Accept.MinConfidence {
			return NodeOutput{
				NodeID: node.ID,
				Status: "yielded",
				Data: map[string]interface{}{
					"answer":     resp.Answer,
					"confidence": resp.Confidence,
					"reason":     "acceptance_criteria_unmet",
				},
			}
		}
	}

	data := map[string]interface{}{
		"answer":     resp.Answer,
		"confidence": resp.Confidence,
	}
	if resp.Scores != nil {
		data["scores"] = resp.Scores
	}

	return NodeOutput{
		NodeID: node.ID,
		Status: "completed",
		Data:   data,
	}
}

// executeExtract runs an extract node using the configured Extractor.
func (e *Engine) executeExtract(ctx context.Context, node *Node, resolvedInput map[string]interface{}) NodeOutput {
	if e.extractor == nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: "no extractor configured for extract node",
		}
	}

	// Determine input text from the resolved input map.
	// Use deterministic key lookup order to avoid Go's random map iteration.
	var text string
	if resolvedInput != nil {
		// Prefer well-known keys in a deterministic order
		for _, key := range []string{"text", "input", "content", "stdout"} {
			if v, ok := resolvedInput[key]; ok {
				if s, ok := v.(string); ok {
					text = s
					break
				}
			}
		}
		// Fallback: use first string value found (sorted for determinism)
		if text == "" {
			keys := make([]string, 0, len(resolvedInput))
			for k := range resolvedInput {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if s, ok := resolvedInput[k].(string); ok {
					text = s
					break
				}
			}
		}
	}

	labels := node.Labels
	if len(labels) == 0 {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: "extract node requires 'labels' field",
		}
	}

	spans, err := e.extractor.Extract(ctx, text, labels)
	if err != nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: fmt.Sprintf("extraction failed: %v", err),
		}
	}

	// Convert spans to data map
	data := make(map[string]interface{})
	spanList := make([]interface{}, len(spans))
	for i, s := range spans {
		spanList[i] = map[string]interface{}{
			"label":      s.Label,
			"text":       s.Text,
			"start":      s.Start,
			"end":        s.End,
			"confidence": s.Confidence,
		}
	}
	data["spans"] = spanList

	// Also set extracted values as top-level keys for easy $ref access
	for _, s := range spans {
		data[s.Label] = s.Text
	}

	return NodeOutput{
		NodeID: node.ID,
		Status: "completed",
		Data:   data,
	}
}

// executeGroup expands items into parallel sub-evaluations bounded by max_concurrency.
func (e *Engine) executeGroup(ctx context.Context, node *Node, outputs map[string]NodeOutput, mu *sync.Mutex) NodeOutput {
	if node.Template == nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: "group node requires a 'template' field",
		}
	}

	// Resolve items — they can be a direct array or a $ref pointer
	items, err := e.resolveGroupItems(node, outputs, mu)
	if err != nil {
		return NodeOutput{
			NodeID: node.ID,
			Status: "failed",
			Stderr: fmt.Sprintf("resolving group items: %v", err),
		}
	}

	if len(items) == 0 {
		return NodeOutput{
			NodeID: node.ID,
			Status: "completed",
			Data: map[string]interface{}{
				"results": []interface{}{},
			},
		}
	}

	// Determine concurrency limit
	concurrency := node.MaxConcurrency
	if concurrency <= 0 {
		concurrency = e.MaxConcurrency
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}

	// Execute each item using the template
	type indexedResult struct {
		index  int
		output NodeOutput
	}

	results := make([]interface{}, len(items))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var resultMu sync.Mutex

	for i, item := range items {
		wg.Add(1)
		go func(idx int, itemValue interface{}) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Create a node from the template with item-specific input.
			// Deep copy Input and Args maps to prevent concurrent map writes
			// across goroutines sharing the same template pointer.
			subNode := *node.Template
			subNode.ID = fmt.Sprintf("%s_item_%d", node.ID, idx)

			// Deep copy maps to avoid concurrent map write panics
			subNode.Input = deepCopyInterfaceMap(node.Template.Input)
			subNode.Args = deepCopyInterfaceMap(node.Template.Args)

			// Inject item value into the sub-node's own isolated input map
			if subNode.Input == nil {
				subNode.Input = make(map[string]interface{})
			}
			subNode.Input["item"] = itemValue
			subNode.Input["index"] = idx

			// Execute the sub-node
			subOutput := e.executeNode(ctx, &subNode, outputs, mu)

			resultMu.Lock()
			results[idx] = map[string]interface{}{
				"item":      itemValue,
				"index":     idx,
				"status":    subOutput.Status,
				"data":      subOutput.Data,
				"exit_code": subOutput.ExitCode,
			}
			resultMu.Unlock()
		}(i, item)
	}

	wg.Wait()

	return NodeOutput{
		NodeID: node.ID,
		Status: "completed",
		Data: map[string]interface{}{
			"results":    results,
			"item_count": len(items),
		},
	}
}

// resolveGroupItems extracts the items array from the group node.
func (e *Engine) resolveGroupItems(node *Node, outputs map[string]NodeOutput, mu *sync.Mutex) ([]interface{}, error) {
	switch items := node.Items.(type) {
	case []interface{}:
		return items, nil
	case []string:
		result := make([]interface{}, len(items))
		for i, s := range items {
			result[i] = s
		}
		return result, nil
	case map[string]interface{}:
		// Could be a $ref pointer
		if ref, ok := items["$ref"]; ok {
			refStr, ok := ref.(string)
			if !ok {
				return nil, fmt.Errorf("$ref must be a string")
			}
			mu.Lock()
			resolved, err := resolvePointer(refStr, outputs)
			mu.Unlock()
			if err != nil {
				return nil, err
			}
			if arr, ok := resolved.([]interface{}); ok {
				return arr, nil
			}
			return nil, fmt.Errorf("$ref resolved to %T, expected array", resolved)
		}
		return nil, fmt.Errorf("items map must contain $ref")
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported items type: %T", items)
	}
}

// resolveReturns collects the requested return values from node outputs.
func (e *Engine) resolveReturns(returnPaths []string, outputs map[string]NodeOutput) map[string]interface{} {
	if len(returnPaths) == 0 {
		return nil
	}

	returns := make(map[string]interface{})
	for _, path := range returnPaths {
		val, err := resolvePointer(path, outputs)
		if err != nil {
			continue
		}
		// Use the path as the key, cleaned up
		key := strings.TrimPrefix(path, "/")
		returns[key] = val
	}
	return returns
}

// deepCopyInterfaceMap returns a deep copy of a map[string]interface{} via
// JSON round-trip, or nil if the input is nil.
func deepCopyInterfaceMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var out map[string]interface{}
	json.Unmarshal(b, &out)
	return out
}
