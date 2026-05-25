package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/skills"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
)

type ExecutionEngine struct {
	Publisher telemetry.EventPublisher
	mutex     sync.Mutex
}

func (e *ExecutionEngine) getPublisher() telemetry.EventPublisher {
	if e.Publisher != nil {
		return e.Publisher
	}
	return telemetry.Default
}

var GlobalEngine = &ExecutionEngine{}

const CacheExplorationGuide = `

### DISK-BACKED CACHE EXPLORATION GUIDE
A previous step resulted in a large payload that has been cached on disk to protect the context window.
You have access to the following special tools to explore and query this cached data:

1. 'introspect_cache': Retrieve schema, field lists, types, and sample record of the cached payload.
   Format: {"tool_arguments": {"cacheId": "cache_..."}}
2. 'read_cached_data': Page through the records of an array data type using standard offset-based pagination.
   Format: {"tool_arguments": {"cacheId": "cache_...", "limit": 10, "offset": 0}}
3. 'jq_cached_data': Query the cached payload using standard JQ filters (e.g. to filter, map, select, group, or calculate).
   Format: {"tool_arguments": {"cacheId": "cache_...", "filter": ".records[] | select(.Age > 30)"}}

If you need to analyze, filter, paginate, or count records from the cache, you MUST use one of these tools instead of attempting to read the raw cache envelope directly.`


// ExecuteGraph runs the compiled topological execution levels.
// It executes nodes at the same Kahn level in parallel via goroutines,
// writing states to memory and pushing audit events to the observer.
func (e *ExecutionEngine) ExecuteGraph(ctx context.Context, graph *compiler.ExecutionGraph, levels [][]string) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	fmt.Fprintf(os.Stderr, "[Executor] Starting execution for Task %s with %d topological levels...\n", graph.TaskID, len(levels))
	e.getPublisher().PublishEvent("task_started", graph.TaskID, "", "Task execution initiated")

	// Pre-populate states as pending
	for _, node := range graph.Nodes {
		_ = memory.DB.SetNodeState(graph.TaskID, node.ID, "pending", "")
	}

	var allCompletedStates []memory.NodeState

	for levelIdx, level := range levels {
		fmt.Fprintf(os.Stderr, "[Executor] Running topological level %d/%d containing %d parallel actions...\n", levelIdx+1, len(levels), len(level))

		var wg sync.WaitGroup
		levelErrors := make(chan error, len(level))

		for _, nodeID := range level {
			wg.Add(1)
			go func(nID string) {
				defer wg.Done()

				// Find original node configurations
				var node *compiler.GraphNode
				for i := range graph.Nodes {
					if graph.Nodes[i].ID == nID {
						node = &graph.Nodes[i]
						break
					}
				}

				if node == nil {
					levelErrors <- fmt.Errorf("node %s configurations not found", nID)
					return
				}

				err := e.executeSingleNode(ctx, graph.TaskID, node)
				if err != nil {
					_ = memory.DB.SetNodeState(graph.TaskID, node.ID, "failed", err.Error())
					_, _ = notification.Send(ctx, "executor", "error", fmt.Sprintf("Action Node '%s' Failed", node.Action), err.Error(), notification.WithTaskID(graph.TaskID), notification.WithTargetID(node.ID))
					if statePayload, jerr := json.Marshal(map[string]string{"status": "failed", "output": err.Error()}); jerr == nil {
						e.getPublisher().PublishStream(stream.StreamChunk{
							Source:  "executor",
							TaskID:  graph.TaskID,
							NodeID:  node.ID,
							Type:    "node_state",
							Content: string(statePayload),
						})
					}
					levelErrors <- err
				}
			}(nodeID)
		}

		wg.Wait()
		close(levelErrors)

		// Check if any errors occurred during parallel executions
		for err := range levelErrors {
			e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", err.Error())
			_, _ = notification.Send(ctx, "executor", "error", "Task Execution Failed", fmt.Sprintf("Task '%s' execution aborted due to error: %s", graph.TaskID, err.Error()), notification.WithTaskID(graph.TaskID))
			return fmt.Errorf("level execution error: %w", err)
		}

		// Gather completed states for this level to save
		for _, nodeID := range level {
			state, ok := memory.DB.GetNodeState(graph.TaskID, nodeID)
			if ok {
				allCompletedStates = append(allCompletedStates, state)
			}
		}

		// Brief delay between levels for visual representation in GUI (500ms)
		time.Sleep(500 * time.Millisecond)
	}

	// Synthesis SOP skill on successful completion
	fmt.Fprintf(os.Stderr, "[Executor] Task %s completed successfully. Synthesizing SOP...\n", graph.TaskID)
	e.getPublisher().PublishEvent("task_completed", graph.TaskID, "", "Task execution completed successfully")
	_, _ = notification.Send(ctx, "executor", "info", "Task Completed Successfully", fmt.Sprintf("Task '%s' completed all topological levels successfully.", graph.TaskID), notification.WithTaskID(graph.TaskID))

	// Retrieve user goal prompt from first node or custom string
	goalDescription := "Dynamic Workflow automation goal"
	if len(graph.Nodes) > 0 {
		goalDescription = graph.Nodes[0].Instructions
	}

	_, err := skills.SynthesizeSOP(graph.TaskID, goalDescription, allCompletedStates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Executor Synthesis Warning] Failed to save SOP: %v\n", err)
	}

	return nil
}



func (e *ExecutionEngine) executeSingleNode(ctx context.Context, taskID string, node *compiler.GraphNode) error {
	fmt.Fprintf(os.Stderr, "[Executor] Executing Action Node: %s (Type: %s, Action: %s)\n", node.ID, node.Type, node.Action)
	
	// Update node state to running
	_ = memory.DB.SetNodeState(taskID, node.ID, "running", "")
	e.getPublisher().PublishEvent("node_started", taskID, node.ID, fmt.Sprintf("Started %s", node.Action))

	if statePayload, err := json.Marshal(map[string]string{"status": "running", "output": ""}); err == nil {
		e.getPublisher().PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  node.ID,
			Type:    "node_state",
			Content: string(statePayload),
		})
	}

	// 1. Pre-flight Variable Interpolation
	interpolatedPrompt := interpolateVariables(node.Instructions, taskID)
	fmt.Fprintf(os.Stderr, "[Executor] Interpolated instruction: %s\n", interpolatedPrompt)

	// 2. Dynamic GBNF Schema selection
	schemaStr, schemaErr := tools.GetSchema(node.Action)
	if schemaErr != nil {
		fmt.Fprintf(os.Stderr, "[Executor Warning] Failed to get GBNF schema for action %s: %v. Using fallback.\n", node.Action, schemaErr)
		schemaStr = ""
	}

	// Fetch Graph-RAG context for matched entities
	ragCtx := memory.DB.GetGraphRAGContext(interpolatedPrompt)

	// 3. Determine Execution Tier and call model using unified seam
	cfg := config.Get()
	var inferenceResult string
	var err error

	var executionTier string = "Local Tactician"
	if cfg.ModelMode == "cloud" || inference.GlobalLocalModel.IsForceCloud(taskID) {
		executionTier = "Cloud Fallback"
	}

	meta := inference.StreamMeta{
		StreamID: fmt.Sprintf("exec_%s_%s", taskID, node.ID),
		Source:   "executor",
		TaskID:   taskID,
		NodeID:   node.ID,
	}

	var isCacheExploration = strings.Contains(strings.ToLower(interpolatedPrompt), "cacheid") || strings.Contains(strings.ToLower(interpolatedPrompt), "cache_")

	var systemPrompt string
	if isCacheExploration {
		systemPrompt = fmt.Sprintf(
			"You are the Local Tactician Node Executor. Your job is to convert the dynamic user step instruction into structured tool parameters.\n\nALLOWED TOOLS:\n- %s\n- introspect_cache\n- read_cached_data\n- jq_cached_data%s",
			node.Action,
			CacheExplorationGuide,
		)
	} else {
		systemPrompt = fmt.Sprintf(
			"You are the Local Tactician Node Executor. Your job is to convert the dynamic user step instruction into structured tool parameters for the tool '%s'.\n\nALLOWED TOOLS:\n- %s",
			node.Action,
			node.Action,
		)
	}
	if ragCtx != "" {
		systemPrompt += "\n\n" + ragCtx
	}

	req := inference.StructuredInferenceRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   interpolatedPrompt,
		JSONSchema:   schemaStr,
		StreamMeta:   &meta,
		TaskID:       taskID,
	}

	inferenceResult, err = inference.GlobalLocalModel.ExecuteStructured(ctx, req)
	if err != nil {
		return fmt.Errorf("node execution failed: %w", err)
	}

	// 4. Extract structured arguments
	var toolCall struct {
		ToolArguments map[string]interface{} `json:"tool_arguments"`
	}
	if err := json.Unmarshal([]byte(inferenceResult), &toolCall); err != nil {
		toolCall.ToolArguments = extractToolArguments(inferenceResult)
	}

	fmt.Fprintf(os.Stderr, "[Executor] Tool arguments extracted: %v\n", toolCall.ToolArguments)

	// 5. Execute tool via the dynamic Tool Registry seam
	var output string
	output, err = tools.Call(ctx, node.Action, toolCall.ToolArguments)
	if err != nil {
		return fmt.Errorf("tool '%s' execution failed: %w", node.Action, err)
	}

	// 6. Compact Output & Cache via deep module
	compactedOutput, cacheID, err := cache.Process(ctx, output, interpolatedPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Executor Compactor Warning] Failed to process payload in cache: %v\n", err)
	} else if cacheID != "" {
		fmt.Fprintf(os.Stderr, "[Executor Compactor] Payload > 12KB. Saved to SQLite and disk cache -> CacheID: %s\n", cacheID)
		e.getPublisher().PublishEvent("cache_envelope_created", taskID, node.ID, fmt.Sprintf("Cached %s output to SQLite and disk (%dKB) -> CacheID: %s", node.Action, len(output)/1024, cacheID))
	}

	time.Sleep(800 * time.Millisecond)

	// Save finished state checkpoint including execution tier metadata
	nodeStatus := fmt.Sprintf("[%s] %s", executionTier, compactedOutput)
	_ = memory.DB.SetNodeState(taskID, node.ID, "completed", nodeStatus)
	e.getPublisher().PublishEvent("node_completed", taskID, node.ID, nodeStatus)

	if statePayload, err := json.Marshal(map[string]string{"status": "completed", "output": nodeStatus}); err == nil {
		e.getPublisher().PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  node.ID,
			Type:    "node_state",
			Content: string(statePayload),
		})
	}

	fmt.Fprintf(os.Stderr, "[Executor] Completed Action Node: %s -> Status: Completed\n", node.ID)
	return nil
}

func interpolateVariables(instruction string, taskID string) string {
	reProp := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\.([^}]+)\}\}`)
	instruction = reProp.ReplaceAllStringFunc(instruction, func(match string) string {
		submatches := reProp.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		nodeID := submatches[1]
		propertyKey := submatches[2]

		state, ok := memory.DB.GetNodeState(taskID, nodeID)
		if !ok {
			return "null"
		}

		rawOutput := state.Output
		if idx := strings.Index(rawOutput, "] "); idx != -1 {
			rawOutput = rawOutput[idx+2:]
		}

		var outputMap map[string]interface{}
		if err := json.Unmarshal([]byte(rawOutput), &outputMap); err != nil {
			return "null"
		}

		val, found := outputMap[propertyKey]
		if !found {
			return "null"
		}
		if mVal, ok := val.(map[string]interface{}); ok {
			b, _ := json.Marshal(mVal)
			return string(b)
		}
		if aVal, ok := val.([]interface{}); ok {
			b, _ := json.Marshal(aVal)
			return string(b)
		}
		return fmt.Sprintf("%v", val)
	})

	reFull := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\}\}`)
	instruction = reFull.ReplaceAllStringFunc(instruction, func(match string) string {
		submatches := reFull.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		nodeID := submatches[1]
		state, ok := memory.DB.GetNodeState(taskID, nodeID)
		if !ok {
			return "null"
		}
		rawOutput := state.Output
		if idx := strings.Index(rawOutput, "] "); idx != -1 {
			rawOutput = rawOutput[idx+2:]
		}
		return rawOutput
	})

	return instruction
}


func extractToolArguments(raw string) map[string]interface{} {
	startIdx := strings.Index(raw, "{")
	endIdx := strings.LastIndex(raw, "}")
	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(raw[startIdx:endIdx+1]), &parsed) == nil {
			if args, ok := parsed["tool_arguments"].(map[string]interface{}); ok {
				return args
			}
			return parsed
		}
	}
	return map[string]interface{}{"query": raw}
}


