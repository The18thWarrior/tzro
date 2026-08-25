package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/strategy"
	"tzro/internal/stream"
	"tzro/internal/tools"
)

// ---------------------------------------------------------------------------
// Concrete capability adapters — bridge executor internals to strategy interfaces
// ---------------------------------------------------------------------------

// executorInferenceProvider adapts inference.ExecuteWorkerStructured and cloud
// escalation into the InferenceProvider interface.
type executorInferenceProvider struct {
	taskID  string
	isCloud bool
}

func (p *executorInferenceProvider) CallModel(
	ctx context.Context,
	messages []inference.InferenceMessage,
	jsonSchema string,
) (*inference.InferenceResult, error) {
	req := inference.StructuredInferenceRequest{
		Messages:   messages,
		JSONSchema: jsonSchema,
		TaskID:     p.taskID,
	}
	output, err := inference.ExecuteWorkerStructured(ctx, req)
	if err != nil {
		return nil, err
	}
	return &inference.InferenceResult{Content: output}, nil
}

func (p *executorInferenceProvider) CallModelStream(
	ctx context.Context,
	messages []inference.InferenceMessage,
	jsonSchema string,
	meta inference.StreamMeta,
) (*inference.InferenceResult, error) {
	req := inference.StructuredInferenceRequest{
		Messages:   messages,
		JSONSchema: jsonSchema,
		StreamMeta: &meta,
		TaskID:     p.taskID,
	}
	output, err := inference.ExecuteWorkerStructured(ctx, req)
	if err != nil {
		return nil, err
	}
	return &inference.InferenceResult{Content: output}, nil
}

func (p *executorInferenceProvider) IsCloud() bool { return p.isCloud }

// executorToolDispatcher adapts tools.Call and tools.GetSchema.
type executorToolDispatcher struct {
	taskID       string
	allowedTools []string // scoped tool whitelist (nil = all)
	engine       *ExecutionEngine
}

func (d *executorToolDispatcher) Dispatch(
	ctx context.Context,
	toolName string,
	args map[string]interface{},
) (string, error) {
	result, err := tools.Call(ctx, toolName, args)
	if err != nil {
		return "", fmt.Errorf("tool %s dispatch failed: %w", toolName, err)
	}
	// Record dispatch for envelope assembly
	if d.engine != nil {
		d.engine.RecordDispatch(d.taskID, toolName, args)
	}
	return result, nil
}

func (d *executorToolDispatcher) GetSchema(toolName string) (string, error) {
	return tools.GetSchema(toolName)
}

func (d *executorToolDispatcher) ListAvailable() []string {
	if d.allowedTools != nil {
		return d.allowedTools
	}
	// All tools available when no whitelist is set.
	// The actual tool list is dynamic (MCP + built-in), so we return nil
	// to indicate "all tools allowed".
	return nil
}

// executorStatePersister adapts memory.DB operations scoped to a task+node.
type executorStatePersister struct {
	taskID string
	nodeID string
}

func (sp *executorStatePersister) SetNodeState(status string, output string) error {
	return memory.DB.SetNodeState(sp.taskID, sp.nodeID, status, output)
}

func (sp *executorStatePersister) SetRawOutput(output string) error {
	return memory.DB.SetNodeRawOutput(sp.taskID, sp.nodeID, output)
}

func (sp *executorStatePersister) GetNodeState(nodeID string) (*compiler.NodeState, error) {
	ns, ok := memory.DB.GetNodeState(sp.taskID, nodeID)
	if !ok {
		return nil, fmt.Errorf("node state not found: %s/%s", sp.taskID, nodeID)
	}
	return &compiler.NodeState{
		TaskID:   ns.TaskID,
		NodeID:   ns.NodeID,
		Status:   ns.Status,
		Output:   ns.Output,
		RawOutput: ns.RawOutput,
	}, nil
}

func (sp *executorStatePersister) GetAllNodeStates() ([]compiler.NodeState, error) {
	states := memory.DB.GetAllNodeStates(sp.taskID)
	result := make([]compiler.NodeState, len(states))
	for i, s := range states {
		result[i] = compiler.NodeState{
			TaskID:   s.TaskID,
			NodeID:   s.NodeID,
			Status:   s.Status,
			Output:   s.Output,
			RawOutput: s.RawOutput,
		}
	}
	return result, nil
}

func (sp *executorStatePersister) PersistThoughtStep(step *compiler.ThoughtStep) error {
	return memory.DB.AddThoughtStep(memory.ThoughtStep{
		ID:         step.ID,
		ProbeID:    step.ProbeID,
		TaskID:     step.TaskID,
		StepIndex:  step.StepIndex,
		Thought:    step.Thought,
		ToolName:   step.ToolName,
		ToolArgs:   step.ToolArgs,
		ToolOutput: step.ToolOutput,
		CreatedAt:  step.CreatedAt,
	})
}

func (sp *executorStatePersister) GetThoughtSteps(probeID string) ([]compiler.ThoughtStep, error) {
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil {
		return nil, err
	}
	result := make([]compiler.ThoughtStep, len(steps))
	for i, s := range steps {
		result[i] = compiler.ThoughtStep{
			ID:         s.ID,
			ProbeID:    s.ProbeID,
			TaskID:     s.TaskID,
			StepIndex:  s.StepIndex,
			Thought:    s.Thought,
			ToolName:   s.ToolName,
			ToolArgs:   s.ToolArgs,
			ToolOutput: s.ToolOutput,
			CreatedAt:  s.CreatedAt,
		}
	}
	return result, nil
}

func (sp *executorStatePersister) PersistPhaseResult(phase string, result *strategy.StageResult) error {
	// Phase results are persisted via SetNodeState with a phase prefix
	data, _ := json.Marshal(result)
	return memory.DB.SetNodeState(sp.taskID, sp.nodeID+"_phase_"+phase, "completed", string(data))
}

func (sp *executorStatePersister) GetPhaseResults(nodeID string) (map[string]*strategy.StageResult, error) {
	// Stub for future stage plan support
	return nil, nil
}

// executorDAGMutator adapts DAG mutation operations.
type executorDAGMutator struct {
	engine *ExecutionEngine
	graph  *compiler.ExecutionGraph
}

func (m *executorDAGMutator) SpawnNode(node compiler.GraphNode, edgeFromNodeID string) error {
	m.graph.Nodes = append(m.graph.Nodes, node)
	m.graph.Edges = append(m.graph.Edges, compiler.GraphEdge{

		SourceID: edgeFromNodeID,
		TargetID: node.ID,
	})
	return nil
}

func (m *executorDAGMutator) PropagateSkip(nodeID string) error {
	m.engine.propagateSkip(m.graph, nodeID)
	return nil
}

func (m *executorDAGMutator) GetMutationBudget() *compiler.MutationBudget {
	return &compiler.MutationBudget{MaxSpawns: 10, RemainingSpawns: 10, MaxDepth: 3}
}

func (m *executorDAGMutator) SpawnChildTask(graph *compiler.ExecutionGraph) (string, error) {
	if SpawnSubTask == nil {
		return "", fmt.Errorf("SpawnSubTask not initialized")
	}
	return SpawnSubTask(context.Background(), "", nil, graph.TaskID, "")
}

// executorEventPublisher adapts the engine's telemetry publisher.
type executorEventPublisher struct {
	engine *ExecutionEngine
}

func (ep *executorEventPublisher) PublishEvent(eventType, taskID, nodeID, content string) {
	ep.engine.getPublisher().PublishEvent(eventType, taskID, nodeID, content)
}

func (ep *executorEventPublisher) PublishStream(chunk stream.StreamChunk) {
	ep.engine.getPublisher().PublishStream(chunk)
}

// executorConfigProvider adapts config.Get() and node policies.
type executorConfigProvider struct{}

func (cp *executorConfigProvider) GetExecutionPolicy() map[string]interface{} {
	cfg := config.Get()
	return map[string]interface{}{
		"modelMode": cfg.ModelMode,
	}
}

func (cp *executorConfigProvider) GetNodePolicy(nodeType, action string) map[string]interface{} {
	return nil // placeholder for node-type-specific policies
}

// executorUpstreamProvider adapts accumulated context and binding resolution.
type executorUpstreamProvider struct {
	taskID string
	graph  *compiler.ExecutionGraph
	node   *compiler.GraphNode
}

func (up *executorUpstreamProvider) AccumulatedContext() string {
	return buildAccumulatedContext(up.taskID, up.graph, up.node.Type)
}

func (up *executorUpstreamProvider) ResolveBinding(ctx context.Context, bindingPath string) (json.RawMessage, error) {
	state, ok := GetNodeStateTolerant(up.taskID, bindingPath)
	if !ok {
		return nil, fmt.Errorf("binding %q not resolved for task %s", bindingPath, up.taskID)
	}
	return json.RawMessage(state.Output), nil
}

func (up *executorUpstreamProvider) GetUpstreamOutput(nodeID string) (string, error) {
	state, ok := GetNodeStateTolerant(up.taskID, nodeID)
	if !ok {
		return "", fmt.Errorf("upstream node %q not found for task %s", nodeID, up.taskID)
	}
	return state.Output, nil
}

// ---------------------------------------------------------------------------
// buildNodeRuntime — factory function
// ---------------------------------------------------------------------------

// buildNodeRuntime constructs a NodeRuntime wiring executor internals into
// the strategy capability interfaces. Called at the top of executeSingleNode
// before dispatching to strategy.Execute.
func (e *ExecutionEngine) buildNodeRuntime(
	ctx context.Context,
	graph *compiler.ExecutionGraph,
	node *compiler.GraphNode,
) *strategy.NodeRuntime {
	taskID := graph.TaskID
	isCloud := config.Get().ModelMode == "cloud" || inference.GlobalLocalModel.IsForceCloud(taskID)

	// Extract pre-computed execution params from context
	p := getExecutionParams(ctx)

	fmt.Fprintf(os.Stderr, "[Runtime] Building NodeRuntime for %s (type=%s, cloud=%v)\n", node.ID, node.Type, isCloud)

	return strategy.NewNodeRuntime(
		taskID,
		node,
		graph,
		&executorInferenceProvider{taskID: taskID, isCloud: isCloud},
		&executorToolDispatcher{taskID: taskID, engine: e},
		&executorStatePersister{taskID: taskID, nodeID: node.ID},
		&executorDAGMutator{engine: e, graph: graph},
		&executorEventPublisher{engine: e},
		&executorConfigProvider{},
		&executorUpstreamProvider{taskID: taskID, graph: graph, node: node},
		p.executionTier,
		p.meta,
		p.interpolatedPrompt,
	)
}

// ---------------------------------------------------------------------------
// dispatchViaStrategy — the dispatch envelope
// ---------------------------------------------------------------------------

// dispatchViaStrategy wraps strategy.Execute with directive processing,
// state management, hook evaluation, and event publishing.
//
// Two modes:
//   - SelfManaged=true: The strategy managed the full lifecycle internally.
//     The envelope only handles propagation (propagateSkip) and flow signals.
//   - SelfManaged=false: Strategy returns output only. The envelope manages
//     the full ceremony: running → completed/failed/skipped state transitions,
//     AfterNode hooks with output modification, and event publishing.
func (e *ExecutionEngine) dispatchViaStrategy(
	ctx context.Context,
	graph *compiler.ExecutionGraph,
	node *compiler.GraphNode,
	s strategy.NodeStrategy,
	activeHooks []ExecutionHook,
) error {
	taskID := graph.TaskID
	nr := e.buildNodeRuntime(ctx, graph, node)

	result, err := s.Execute(ctx, nr)
	if err != nil {
		return err
	}

	// Self-managed mode: strategy already handled state, events, and hooks.
	// Only handle propagation and flow signals.
	if result.SelfManaged {
		switch result.Directive {
		case strategy.DirectiveSkipDownstream:
			e.propagateSkip(graph, node.ID)
		case strategy.DirectivePause:
			return ErrTaskPaused
		case strategy.DirectiveHalt:
			return fmt.Errorf("strategy %s halted: %s", s.Type(), result.Output)
		}
		return nil
	}

	// Strategy-owned mode: envelope manages the full ceremony.

	// 1. Handle failure directives (state already set by strategy or set here)
	switch result.Directive {
	case strategy.DirectiveHalt:
		_ = memory.DB.SetNodeState(taskID, node.ID, "failed", result.Output)
		e.getPublisher().PublishEvent("node_failed", taskID, node.ID, result.Output)
		e.publishNodeStateStream(taskID, node.ID, "failed", result.Output)
		return fmt.Errorf("strategy %s halted: %s", s.Type(), result.Output)

	case strategy.DirectivePause:
		_ = memory.DB.SetNodeState(taskID, node.ID, "pending", "Paused by strategy")
		e.getPublisher().PublishEvent("node_paused", taskID, node.ID, "Execution paused by strategy")
		return ErrTaskPaused

	case strategy.DirectiveSkipDownstream:
		_ = memory.DB.SetNodeState(taskID, node.ID, "skipped", result.Output)
		e.getPublisher().PublishEvent("node_skipped", taskID, node.ID, result.Output)
		e.publishNodeStateStream(taskID, node.ID, "skipped", result.Output)
		e.propagateSkip(graph, node.ID)
		return nil
	}

	// 2. DirectiveContinue: run AfterNode hooks, then persist state
	output := result.Output
	var nodeAfterAction HookAction = ActionContinue
	for _, h := range activeHooks {
		action, hookErr := h.AfterNode(ctx, taskID, node, &output)
		if hookErr != nil {
			return fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, hookErr)
		}
		if action == ActionAbort {
			return fmt.Errorf("AfterNode hook aborted execution for node %s", node.ID)
		}
		if action == ActionPause {
			nodeAfterAction = ActionPause
		}
	}

	if nodeAfterAction == ActionPause {
		_ = memory.DB.SetNodeState(taskID, node.ID, "pending", "Paused by hook")
		e.getPublisher().PublishEvent("node_paused", taskID, node.ID, "Execution paused by hook")
		return ErrTaskPaused
	}

	// 3. Persist completed state with (possibly hook-modified) output
	_ = memory.DB.SetNodeState(taskID, node.ID, "completed", output)
	_ = memory.DB.SetNodeRawOutput(taskID, node.ID, output)
	e.getPublisher().PublishEvent("node_completed", taskID, node.ID, output)
	e.publishNodeStateStream(taskID, node.ID, "completed", output)

	return nil
}



// publishNodeStateStream publishes a node_state stream chunk. Factored out
// to avoid repeating the JSON marshal + StreamChunk construction everywhere.
func (e *ExecutionEngine) publishNodeStateStream(taskID, nodeID, status, output string) {
	publishNodeState(e.getPublisher(), taskID, nodeID, status, output)
}

// publishNodeState is the standalone version of publishNodeStateStream.
// Strategies use this via their EventPublisher instead of needing *ExecutionEngine.
func publishNodeState(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string) {
	if statePayload, err := json.Marshal(map[string]string{"status": status, "output": output}); err == nil {
		pub.PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  nodeID,
			Type:    "node_state",
			Content: string(statePayload),
		})
	}
}

