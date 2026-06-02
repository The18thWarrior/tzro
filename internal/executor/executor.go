package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
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

type HookAction string

const (
	ActionContinue HookAction = "continue"
	ActionSkip     HookAction = "skip"
	ActionPause    HookAction = "pause"
	ActionAbort    HookAction = "abort"
)

// ExecutionHook defines synchronous lifecycle hooks that developers can register
// on the ExecutionEngine to intercept and mutate DAG level/node executions.
type ExecutionHook interface {
	BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)
	AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)
	BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error)
	AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error)
}

var ErrTaskPaused = fmt.Errorf("task execution paused by hook")

type ExecutionEngine struct {
	Publisher telemetry.EventPublisher
	hooks     []ExecutionHook
	mutex     sync.Mutex
}

func (e *ExecutionEngine) RegisterHook(h ExecutionHook) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.hooks = append(e.hooks, h)
}

func (e *ExecutionEngine) getHooksUnlocked() []ExecutionHook {
	if len(e.hooks) == 0 {
		return nil
	}
	copied := make([]ExecutionHook, len(e.hooks))
	copy(copied, e.hooks)
	return copied
}

func (e *ExecutionEngine) getHooks() []ExecutionHook {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.getHooksUnlocked()
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

	activeHooks := e.getHooksUnlocked()

	for levelIdx, level := range levels {
		fmt.Fprintf(os.Stderr, "[Executor] Running topological level %d/%d containing %d parallel actions...\n", levelIdx+1, len(levels), len(level))

		// Resolve level node references for hook arguments
		var levelNodes []*compiler.GraphNode
		for _, nodeID := range level {
			var node *compiler.GraphNode
			for i := range graph.Nodes {
				if graph.Nodes[i].ID == nodeID {
					node = &graph.Nodes[i]
					break
				}
			}
			if node != nil {
				levelNodes = append(levelNodes, node)
			}
		}

		// Run BeforeLevel hooks
		var levelAction HookAction = ActionContinue
		for _, h := range activeHooks {
			action, err := h.BeforeLevel(ctx, graph.TaskID, levelNodes)
			if err != nil {
				e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", fmt.Sprintf("BeforeLevel hook failed: %v", err))
				return fmt.Errorf("BeforeLevel hook error: %w", err)
			}
			if action == ActionAbort {
				e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", "BeforeLevel hook requested abort")
				return fmt.Errorf("BeforeLevel hook aborted execution")
			}
			if action == ActionSkip {
				levelAction = ActionSkip
			}
			if action == ActionPause {
				levelAction = ActionPause
			}
		}

		if levelAction == ActionPause {
			e.getPublisher().PublishEvent("task_paused", graph.TaskID, "", "Level execution paused by hook")
			return ErrTaskPaused
		}

		if levelAction == ActionSkip {
			fmt.Fprintf(os.Stderr, "[Executor] BeforeLevel hook requested skip for level %d\n", levelIdx+1)
			for _, node := range levelNodes {
				_ = memory.DB.SetNodeState(graph.TaskID, node.ID, "skipped", "Level skipped by hook")
				e.getPublisher().PublishEvent("node_skipped", graph.TaskID, node.ID, "Level skipped by hook")
				if statePayload, jerr := json.Marshal(map[string]string{"status": "skipped", "output": "Level skipped by hook"}); jerr == nil {
					e.getPublisher().PublishStream(stream.StreamChunk{
						Source:  "executor",
						TaskID:  graph.TaskID,
						NodeID:  node.ID,
						Type:    "node_state",
						Content: string(statePayload),
					})
				}
				e.propagateSkip(graph, node.ID)
			}
			continue
		}

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

				err := e.executeSingleNode(ctx, graph, node, activeHooks)
				if err != nil {
					if err == ErrTaskPaused {
						levelErrors <- err
						return
					}
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
			if err == ErrTaskPaused {
				e.getPublisher().PublishEvent("task_paused", graph.TaskID, "", "Task execution paused by hook")
				return ErrTaskPaused
			}
			e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", err.Error())
			_, _ = notification.Send(ctx, "executor", "error", "Task Execution Failed", fmt.Sprintf("Task '%s' execution aborted due to error: %s", graph.TaskID, err.Error()), notification.WithTaskID(graph.TaskID))
			return fmt.Errorf("level execution error: %w", err)
		}

		// Run AfterLevel hooks
		var afterLevelAction HookAction = ActionContinue
		for _, h := range activeHooks {
			action, err := h.AfterLevel(ctx, graph.TaskID, levelNodes)
			if err != nil {
				e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", fmt.Sprintf("AfterLevel hook failed: %v", err))
				return fmt.Errorf("AfterLevel hook error: %w", err)
			}
			if action == ActionAbort {
				e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", "AfterLevel hook requested abort")
				return fmt.Errorf("AfterLevel hook aborted execution")
			}
			if action == ActionPause {
				afterLevelAction = ActionPause
			}
		}

		if afterLevelAction == ActionPause {
			e.getPublisher().PublishEvent("task_paused", graph.TaskID, "", "Level execution paused after level completion by hook")
			return ErrTaskPaused
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

	// Trigger Two-Tier KV Cache Garbage Collection post-task boundary
	go func() {
		_ = inference.GlobalLocalModel.TriggerGC(context.Background())
		inference.GlobalLocalModel.CheckAndTriggerTier2GC(context.Background())
	}()

	return nil
}

func (e *ExecutionEngine) executeSingleNode(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode, activeHooks []ExecutionHook) error {
	taskID := graph.TaskID

	// 0. Pre-flight Check: Is node already skipped?
	if state, ok := memory.DB.GetNodeState(taskID, node.ID); ok && state.Status == "skipped" {
		fmt.Fprintf(os.Stderr, "[Executor] Node %s is skipped. Skipping execution.\n", node.ID)
		return nil
	}

	// 0.0 Run BeforeNode hooks
	var nodeAction HookAction = ActionContinue
	for _, h := range activeHooks {
		action, err := h.BeforeNode(ctx, taskID, node)
		if err != nil {
			return fmt.Errorf("BeforeNode hook error for node %s: %w", node.ID, err)
		}
		if action == ActionAbort {
			return fmt.Errorf("BeforeNode hook aborted execution for node %s", node.ID)
		}
		if action == ActionSkip {
			nodeAction = ActionSkip
		}
		if action == ActionPause {
			nodeAction = ActionPause
		}
	}

	if nodeAction == ActionPause {
		_ = memory.DB.SetNodeState(taskID, node.ID, "pending", "Paused by hook")
		e.getPublisher().PublishEvent("node_paused", taskID, node.ID, "Execution paused by hook")
		return ErrTaskPaused
	}

	if nodeAction == ActionSkip {
		fmt.Fprintf(os.Stderr, "[Executor] BeforeNode hook requested skip for node %s\n", node.ID)
		_ = memory.DB.SetNodeState(taskID, node.ID, "skipped", "Skipped by hook")
		e.getPublisher().PublishEvent("node_skipped", taskID, node.ID, "Skipped by hook")
		if statePayload, err := json.Marshal(map[string]string{"status": "skipped", "output": "Skipped by hook"}); err == nil {
			e.getPublisher().PublishStream(stream.StreamChunk{
				Source:  "executor",
				TaskID:  taskID,
				NodeID:  node.ID,
				Type:    "node_state",
				Content: string(statePayload),
			})
		}
		e.propagateSkip(graph, node.ID)
		return nil
	}

	// 0.1 Branch / Condition Evaluation Seam
	if node.Condition != "" || node.Type == "branch" {
		satisfied, err := e.evaluateBranchCondition(ctx, graph, node)
		if err != nil {
			return fmt.Errorf("failed to evaluate branch condition for node %s: %w", node.ID, err)
		}

		if satisfied {
			fmt.Fprintf(os.Stderr, "[Executor] Branch node %s condition satisfied!\n", node.ID)
			_ = memory.DB.SetNodeState(taskID, node.ID, "completed", "Condition satisfied")
			e.getPublisher().PublishEvent("node_completed", taskID, node.ID, "Condition satisfied")
			if statePayload, err := json.Marshal(map[string]string{"status": "completed", "output": "Condition satisfied"}); err == nil {
				e.getPublisher().PublishStream(stream.StreamChunk{
					Source:  "executor",
					TaskID:  taskID,
					NodeID:  node.ID,
					Type:    "node_state",
					Content: string(statePayload),
				})
			}
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "[Executor] Branch node %s condition NOT satisfied. Skipping branch and propagating skip...\n", node.ID)
			_ = memory.DB.SetNodeState(taskID, node.ID, "skipped", "Condition not satisfied")
			e.getPublisher().PublishEvent("node_skipped", taskID, node.ID, "Condition not satisfied")
			if statePayload, err := json.Marshal(map[string]string{"status": "skipped", "output": "Condition not satisfied"}); err == nil {
				e.getPublisher().PublishStream(stream.StreamChunk{
					Source:  "executor",
					TaskID:  taskID,
					NodeID:  node.ID,
					Type:    "node_state",
					Content: string(statePayload),
				})
			}
			e.propagateSkip(graph, node.ID)
			return nil
		}
	}

	cfg := config.Get()
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

	// 1. Pre-flight Variable Interpolation
	interpolatedPrompt := InterpolateVariables(node.Instructions, taskID)
	fmt.Fprintf(os.Stderr, "[Executor] Interpolated instruction: %s\n", interpolatedPrompt)

	// 1.1 GBNF Bridge node: parameters extraction ONLY
	if node.Type == "gbnf_bridge" {
		// Update node state to running
		_ = memory.DB.SetNodeState(taskID, node.ID, "running", "")
		e.getPublisher().PublishEvent("node_started", taskID, node.ID, fmt.Sprintf("Started %s Bridge", node.Action))

		if statePayload, err := json.Marshal(map[string]string{"status": "running", "output": ""}); err == nil {
			e.getPublisher().PublishStream(stream.StreamChunk{
				Source:  "executor",
				TaskID:  taskID,
				NodeID:  node.ID,
				Type:    "node_state",
				Content: string(statePayload),
			})
		}

		schemaStr, schemaErr := tools.GetSchema(node.Action)
		if schemaErr != nil {
			fmt.Fprintf(os.Stderr, "[Executor Warning] Failed to get GBNF schema for action %s: %v. Using fallback.\n", node.Action, schemaErr)
			schemaStr = ""
		}

		// P0 Fix (13:00): Use accumulated context architecture instead of flat interpolated prompt.
		// Upstream node outputs are passed as labeled structured blocks, enabling the bridge
		// to extract values by key name rather than re-parsing them from prose.
		accumulatedCtx := buildAccumulatedContext(taskID, graph)
		systemPrompt := buildContextAwareSystemPrompt(node.Action, node.Instructions, schemaStr)
		userPrompt := buildContextAwareUserPrompt(accumulatedCtx, "", interpolatedPrompt)

		req := inference.StructuredInferenceRequest{
			SystemPrompt: systemPrompt,
			UserPrompt:   userPrompt,
			JSONSchema:   schemaStr,
			StreamMeta:   &meta,
			TaskID:       taskID,
		}

		inferenceResult, err := inference.GlobalLocalModel.ExecuteStructured(ctx, req)
		if err != nil {
			return fmt.Errorf("gbnf_bridge execution failed: %w", err)
		}

		// Run AfterNode hooks
		var nodeAfterAction HookAction = ActionContinue
		for _, h := range activeHooks {
			action, err := h.AfterNode(ctx, taskID, node, &inferenceResult)
			if err != nil {
				return fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, err)
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

		time.Sleep(800 * time.Millisecond)

		nodeStatus := fmt.Sprintf("[%s] %s", executionTier, inferenceResult)
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
		return nil
	}

	// 1.2 Deterministic execution node: tool execution ONLY
	if node.Type == "deterministic" {
		// Update node state to running
		_ = memory.DB.SetNodeState(taskID, node.ID, "running", "")
		e.getPublisher().PublishEvent("node_started", taskID, node.ID, fmt.Sprintf("Executing %s", node.Action))

		if statePayload, err := json.Marshal(map[string]string{"status": "running", "output": ""}); err == nil {
			e.getPublisher().PublishStream(stream.StreamChunk{
				Source:  "executor",
				TaskID:  taskID,
				NodeID:  node.ID,
				Type:    "node_state",
				Content: string(statePayload),
			})
		}

		// P0 Fix (13:00): Add inference step for deterministic nodes using accumulated context.
		// Instead of parsing args from interpolated text (fragile), use the same context-aware
		// GBNF bridge extraction that generic action nodes use. Fall back to extractToolArguments on error.
		schemaStr, schemaErr := tools.GetSchema(node.Action)
		if schemaErr != nil {
			fmt.Fprintf(os.Stderr, "[Executor Warning] Failed to get GBNF schema for deterministic action %s: %v\n", node.Action, schemaErr)
			schemaStr = ""
		}

		accumulatedCtx := buildAccumulatedContext(taskID, graph)
		var toolArguments map[string]interface{}

		if accumulatedCtx != "" && schemaStr != "" {
			// Use inference-based parameter extraction with accumulated context
			detSystemPrompt := buildContextAwareSystemPrompt(node.Action, node.Instructions, schemaStr)
			detUserPrompt := buildContextAwareUserPrompt(accumulatedCtx, "", interpolatedPrompt)

			detReq := inference.StructuredInferenceRequest{
				SystemPrompt: detSystemPrompt,
				UserPrompt:   detUserPrompt,
				JSONSchema:   schemaStr,
				StreamMeta:   &meta,
				TaskID:       taskID,
			}

			detResult, detErr := inference.GlobalLocalModel.ExecuteStructured(ctx, detReq)
			if detErr == nil {
				var detToolCall struct {
					ToolArguments map[string]interface{} `json:"tool_arguments"`
				}
				if json.Unmarshal([]byte(detResult), &detToolCall) == nil && len(detToolCall.ToolArguments) > 0 {
					toolArguments = detToolCall.ToolArguments
					fmt.Fprintf(os.Stderr, "[Executor] Deterministic args via context-aware inference: %v\n", toolArguments)
				} else {
					toolArguments = extractToolArguments(detResult)
					fmt.Fprintf(os.Stderr, "[Executor] Deterministic args via inference fallback parse: %v\n", toolArguments)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[Executor Warning] Deterministic inference failed, falling back to interpolation: %v\n", detErr)
				toolArguments = extractToolArguments(interpolatedPrompt)
			}
		} else {
			// No accumulated context or no schema — fall back to legacy extraction
			toolArguments = extractToolArguments(interpolatedPrompt)
		}

		// Safety net: apply coercion pipeline
		coerceNumericArguments(toolArguments, interpolatedPrompt)
		coerceStringArguments(toolArguments, interpolatedPrompt, node.Action)
		resolveInterpolatedArguments(toolArguments, interpolatedPrompt, node.Instructions, taskID)
		fmt.Fprintf(os.Stderr, "[Executor] Deterministic tool arguments extracted: %v\n", toolArguments)

		output, err := tools.Call(ctx, node.Action, toolArguments)
		if err != nil {
			return fmt.Errorf("tool '%s' execution failed: %w", node.Action, err)
		}

		// Run AfterNode hooks
		var nodeAfterAction HookAction = ActionContinue
		for _, h := range activeHooks {
			action, err := h.AfterNode(ctx, taskID, node, &output)
			if err != nil {
				return fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, err)
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

		compactedOutput, cacheID, err := cache.Process(ctx, output, interpolatedPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Executor Compactor Warning] Failed to process payload in cache: %v\n", err)
		} else if cacheID != "" {
			fmt.Fprintf(os.Stderr, "[Executor Compactor] Payload > 12KB. Saved to SQLite and disk cache -> CacheID: %s\n", cacheID)
			e.getPublisher().PublishEvent("cache_envelope_created", taskID, node.ID, fmt.Sprintf("Cached %s output to SQLite and disk (%dKB) -> CacheID: %s", node.Action, len(output)/1024, cacheID))
		}

		time.Sleep(800 * time.Millisecond)

		nodeStatus := fmt.Sprintf("[%s] %s", executionTier, compactedOutput)
		_ = memory.DB.SetNodeState(taskID, node.ID, "completed", nodeStatus)
		// P0 Fix: Store clean tool output separately for downstream interpolation.
		// The display-formatted nodeStatus contains tier prefix + compacted output,
		// which corrupts JSON property lookups in interpolateVariables.
		_ = memory.DB.SetNodeRawOutput(taskID, node.ID, output)
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
		return nil
	}

	// 1.3 Synthesis node: final summary compilation ONLY
	if node.Type == "synthesis" {
		// Update node state to running
		_ = memory.DB.SetNodeState(taskID, node.ID, "running", "")
		e.getPublisher().PublishEvent("node_started", taskID, node.ID, "Synthesizing final response")

		if statePayload, err := json.Marshal(map[string]string{"status": "running", "output": ""}); err == nil {
			e.getPublisher().PublishStream(stream.StreamChunk{
				Source:  "executor",
				TaskID:  taskID,
				NodeID:  node.ID,
				Type:    "node_state",
				Content: string(statePayload),
			})
		}

		systemPrompt := "You are the Local Tactician Node Executor. Summarize and compile all prior action outputs into a final cohesive response."

		req := inference.StructuredInferenceRequest{
			SystemPrompt: systemPrompt,
			UserPrompt:   interpolatedPrompt,
			StreamMeta:   &meta,
			TaskID:       taskID,
		}

		inferenceResult, err := inference.GlobalLocalModel.ExecuteStructured(ctx, req)
		if err != nil {
			return fmt.Errorf("synthesis node execution failed: %w", err)
		}

		// Run AfterNode hooks
		var nodeAfterAction HookAction = ActionContinue
		for _, h := range activeHooks {
			action, err := h.AfterNode(ctx, taskID, node, &inferenceResult)
			if err != nil {
				return fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, err)
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

		time.Sleep(800 * time.Millisecond)

		nodeStatus := fmt.Sprintf("[%s] %s", executionTier, inferenceResult)
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
		return nil
	}

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

	// 2. Dynamic GBNF Schema selection
	schemaStr, schemaErr := tools.GetSchema(node.Action)
	if schemaErr != nil {
		fmt.Fprintf(os.Stderr, "[Executor Warning] Failed to get GBNF schema for action %s: %v. Using fallback.\n", node.Action, schemaErr)
		schemaStr = ""
	}

	// Fetch Graph-RAG context for matched entities
	ragCtx := memory.DB.GetGraphRAGContext(interpolatedPrompt, config.GetMaxRAGContextChars())

	var inferenceResult string
	var err error

	var isCacheExploration = strings.Contains(strings.ToLower(interpolatedPrompt), "cacheid") || strings.Contains(strings.ToLower(interpolatedPrompt), "cache_")

	// P0 Fix (13:00): Use accumulated context architecture instead of flat interpolated prompt.
	// Upstream node outputs are passed as labeled structured blocks, enabling the bridge
	// to extract values by key name rather than re-parsing them from prose.
	accumulatedCtx := buildAccumulatedContext(taskID, graph)

	var systemPrompt string
	if isCacheExploration {
		systemPrompt = fmt.Sprintf(
			"You are the Local Tactician Node Executor. Your job is to convert the dynamic user step instruction into structured tool parameters.\n\nALLOWED TOOLS:\n- %s\n- introspect_cache\n- read_cached_data\n- jq_cached_data%s",
			node.Action,
			CacheExplorationGuide,
		)
	} else {
		systemPrompt = buildContextAwareSystemPrompt(node.Action, node.Instructions, schemaStr)
	}

	userPrompt := buildContextAwareUserPrompt(accumulatedCtx, ragCtx, interpolatedPrompt)

	req := inference.StructuredInferenceRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
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
	// P1 Fix: Apply deterministic numeric coercion on generic action path too.
	// Corrects zero/null values when the instruction contains explicit numeric literals (e.g. negatives).
	coerceNumericArguments(toolCall.ToolArguments, interpolatedPrompt)
	// P0 Fix (11:32): Deterministic string coercion + interpolation-aware argument resolution.
	coerceStringArguments(toolCall.ToolArguments, interpolatedPrompt, node.Action)
	resolveInterpolatedArguments(toolCall.ToolArguments, interpolatedPrompt, node.Instructions, taskID)

	fmt.Fprintf(os.Stderr, "[Executor] Tool arguments extracted: %v\n", toolCall.ToolArguments)

	// 5. Execute tool via the dynamic Tool Registry seam
	var output string
	output, err = tools.Call(ctx, node.Action, toolCall.ToolArguments)
	if err != nil {
		return fmt.Errorf("tool '%s' execution failed: %w", node.Action, err)
	}

	// Run AfterNode hooks
	var nodeAfterAction HookAction = ActionContinue
	for _, h := range activeHooks {
		action, err := h.AfterNode(ctx, taskID, node, &output)
		if err != nil {
			return fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, err)
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

	// P0 Fix: Store clean tool output for downstream variable interpolation.
	// Without this, interpolateVariables() falls back to the display-formatted Output
	// which contains tier prefix + compaction, corrupting JSON property lookups.
	_ = memory.DB.SetNodeRawOutput(taskID, node.ID, output)

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

func InterpolateVariables(instruction string, taskID string) string {
	reProp := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\.([^}]+)\}\}`)
	instruction = reProp.ReplaceAllStringFunc(instruction, func(match string) string {
		submatches := reProp.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		nodeID := submatches[1]
		propertyKey := submatches[2]

		state, ok := GetNodeStateTolerant(taskID, nodeID)
		if !ok {
			return "null"
		}

		// P0 Fix: Prefer RawOutput (clean tool response) over Output (display-formatted with
		// tier prefix + compaction). This ensures JSON property lookups resolve correctly.
		sourceOutput := state.RawOutput
		if sourceOutput == "" {
			// Fallback to legacy Output with tier prefix stripping
			sourceOutput = state.Output
			if idx := strings.Index(sourceOutput, "] "); idx != -1 {
				sourceOutput = sourceOutput[idx+2:]
			}
		}

		var outputMap map[string]interface{}
		if err := json.Unmarshal([]byte(sourceOutput), &outputMap); err != nil {
			// Try parsing as KV lines (compacted object notation)
			outputMap = make(map[string]interface{})
			lines := strings.Split(sourceOutput, "\n")
			for _, line := range lines {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					val := strings.TrimSpace(parts[1])
					if ls := strings.ToLower(val); ls == "true" {
						outputMap[key] = true
					} else if ls == "false" {
						outputMap[key] = false
					} else if num, err := strconv.ParseFloat(val, 64); err == nil {
						outputMap[key] = num
					} else {
						outputMap[key] = val
					}
				}
			}
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
		state, ok := GetNodeStateTolerant(taskID, nodeID)
		if !ok {
			return "null"
		}
		// P0 Fix: Prefer RawOutput for full output interpolation too
		if state.RawOutput != "" {
			return state.RawOutput
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
			// Recursively unwrap tool_arguments nesting to handle double/triple wrapping
			// caused by bridge GBNF schema + exec node interpolation
			for {
				args, ok := parsed["tool_arguments"].(map[string]interface{})
				if !ok {
					break
				}
				parsed = args
			}
			return parsed
		}
	}
	return map[string]interface{}{"query": raw}
}

// numericLiteralRegex matches signed integers and decimals in natural language instruction text.
// It requires a word boundary or whitespace before the number to avoid matching inside identifiers.
var numericLiteralRegex = regexp.MustCompile(`(?:^|[\s,:(=])(-?\d+(?:\.\d+)?)(?:[\s,;.!?)\]]|$)`)

// coerceNumericArguments is a deterministic post-extraction validator for numeric tool arguments.
// When the GBNF bridge extracts a 0 value for a numeric argument, but the instruction text
// contains an explicit non-zero numeric literal, this function substitutes the literal value.
// This avoids the need for prompt injection to fix negative number extraction failures.
func coerceNumericArguments(args map[string]interface{}, instruction string) {
	// Extract all numeric literals from the instruction text
	matches := numericLiteralRegex.FindAllStringSubmatch(instruction, -1)
	if len(matches) == 0 {
		return
	}

	var instructionNums []float64
	for _, m := range matches {
		if len(m) >= 2 {
			if num, err := strconv.ParseFloat(m[1], 64); err == nil {
				instructionNums = append(instructionNums, num)
			}
		}
	}

	if len(instructionNums) == 0 {
		return
	}

	for key, val := range args {
		// Only coerce numeric arguments
		numVal, isNum := val.(float64)
		if !isNum {
			if intVal, ok := val.(int); ok {
				numVal = float64(intVal)
				isNum = true
			} else if int64Val, ok := val.(int64); ok {
				numVal = float64(int64Val)
				isNum = true
			} else if float32Val, ok := val.(float32); ok {
				numVal = float64(float32Val)
				isNum = true
			}
		}
		if !isNum {
			continue
		}

		// Search for a numeric literal in the instruction that contextually relates to this key.
		// Strategy: look for the key name (or a word-boundary variant) near a numeric literal.
		keyLower := strings.ToLower(key)
		keyWords := strings.Split(strings.ReplaceAll(keyLower, "_", " "), " ")

		bestNum := 0.0
		bestDist := len(instruction) + 1

		for _, num := range instructionNums {
			if num == 0 {
				continue // Skip zero literals — they wouldn't correct a zero extraction
			}

			// Find the position of this literal in the instruction
			numStr := strconv.FormatFloat(num, 'f', -1, 64)
			numIdx := strings.Index(instruction, numStr)
			if numIdx == -1 {
				continue
			}

			// Check proximity of any key word to this literal
			for _, word := range keyWords {
				if len(word) < 3 {
					continue // Skip very short words to avoid false matches
				}
				wordIdx := strings.Index(strings.ToLower(instruction), word)
				if wordIdx == -1 {
					continue
				}
				dist := numIdx - wordIdx
				if dist < 0 {
					dist = -dist
				}
				if dist < bestDist {
					bestDist = dist
					bestNum = num
				}
			}
		}

		// Apply coercion if we found a contextually relevant non-zero literal
		// within a reasonable proximity (200 chars accounts for natural language phrasing).
		// We coerce if the current value is 0 OR if there is a sign mismatch (e.g. positive vs negative).
		if bestNum != 0 && bestDist < 200 {
			signMismatch := (numVal > 0 && bestNum < 0) || (numVal < 0 && bestNum > 0)
			if numVal == 0 || signMismatch {
				fmt.Fprintf(os.Stderr, "[Executor Coercion] Correcting argument '%s': %v -> %v (from instruction literal)\n", key, numVal, bestNum)
				args[key] = bestNum
			}
		}
	}
}

// labeledQuotedRegex matches labeled key-value patterns where the value is quoted.
// Patterns: "key: 'value'", "key: \"value\""
var labeledQuotedRegex = regexp.MustCompile(`(?i)(\w[\w\s]*?)\s*[:=]\s*["']([^"'\n]+?)["']`)

// labeledUnquotedRegex matches labeled key-value patterns with unquoted values.
// Patterns: "key: value", "key = value" (value ends at punctuation/comma/newline)
var labeledUnquotedRegex = regexp.MustCompile(`(?i)(\w[\w\s]*?)\s*[:=]\s*([^\s"',;.!?\n][^,;.!?\n]*?)(?:[,;.!?\s]|$)`)

// identifierRegex matches structured identifiers (IDs, codes, emails, version tags)
// that are likely tool argument values rather than natural language.
var identifierRegex = regexp.MustCompile(`(?:^|[\s:=])([A-Z][A-Z0-9_-]{2,}(?:-[A-Za-z0-9]+)*|[a-z][a-z0-9_.+-]+@[a-z0-9.-]+\.[a-z]{2,}|v\d+\.\d+(?:\.\d+)?|#[\w-]+|[a-z]+_[a-z0-9_]+_\d+)(?:[\s,;.!?)]|$)`)

// coerceStringArguments is a deterministic post-extraction validator for string tool arguments.
// When the GBNF bridge extracts an empty string or a hallucinated value for a string argument,
// but the instruction text contains an explicit string value near the argument key name,
// this function substitutes the instruction literal. This mirrors coerceNumericArguments
// but for the string domain, and is the primary fix for the 79% parameter mismatch failures
// discovered in the 2026-05-30 11:32 100-case benchmark.
func coerceStringArguments(args map[string]interface{}, instruction string, toolName string) {
	if len(args) == 0 || instruction == "" {
		return
	}

	// Retrieve tool schema to know expected argument types
	var schemaProps map[string]interface{}
	if schemaStr, err := tools.GetSchema(toolName); err == nil && schemaStr != "" {
		var schemaMap map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schemaMap) == nil {
			if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
				if toolArgs, ok := props["tool_arguments"].(map[string]interface{}); ok {
					if taProps, ok := toolArgs["properties"].(map[string]interface{}); ok {
						schemaProps = taProps
					}
				} else {
					schemaProps = props
				}
			}
		}
	}

	// Extract all labeled value pairs from the instruction using both quoted and unquoted patterns
	type labeledValue struct {
		key   string
		value string
		pos   int
	}
	var labeledPairs []labeledValue

	// Quoted values: key: "value" or key: 'value'
	for _, m := range labeledQuotedRegex.FindAllStringSubmatch(instruction, -1) {
		if len(m) >= 3 {
			key := strings.TrimSpace(m[1])
			val := strings.TrimSpace(m[2])
			if val != "" && len(val) > 1 {
				pos := strings.Index(instruction, m[0])
				labeledPairs = append(labeledPairs, labeledValue{key: key, value: val, pos: pos})
			}
		}
	}
	// Unquoted values: key: value or key = value
	for _, m := range labeledUnquotedRegex.FindAllStringSubmatch(instruction, -1) {
		if len(m) >= 3 {
			key := strings.TrimSpace(m[1])
			val := strings.TrimSpace(m[2])
			if val != "" && len(val) > 1 {
				pos := strings.Index(instruction, m[0])
				labeledPairs = append(labeledPairs, labeledValue{key: key, value: val, pos: pos})
			}
		}
	}

	// Extract identifiers from instruction (IDs, emails, codes, tags)
	idMatches := identifierRegex.FindAllStringSubmatch(instruction, -1)
	var identifiers []struct {
		value string
		pos   int
	}
	for _, m := range idMatches {
		if len(m) >= 2 {
			val := strings.TrimSpace(m[1])
			pos := strings.Index(instruction, val)
			identifiers = append(identifiers, struct {
				value string
				pos   int
			}{value: val, pos: pos})
		}
	}

	instructionLower := strings.ToLower(instruction)

	for key, val := range args {
		// Only coerce string arguments
		strVal, isStr := val.(string)
		if !isStr {
			continue
		}

		// Check if schema expects a string type for this key
		if schemaProps != nil {
			if prop, ok := schemaProps[key].(map[string]interface{}); ok {
				if propType, ok := prop["type"].(string); ok && propType != "string" {
					continue
				}
			}
		}

		// Only coerce if the value is empty OR not found anywhere in the instruction
		valLower := strings.ToLower(strVal)
		isEmpty := strVal == "" || strVal == "null" || strVal == "undefined"
		isHallucinated := !isEmpty && !strings.Contains(instructionLower, valLower)

		if !isEmpty && !isHallucinated {
			continue
		}

		keyLower := strings.ToLower(key)
		keyWords := strings.Split(strings.ReplaceAll(keyLower, "_", " "), " ")

		// Strategy 1: Check labeled pairs for key-name proximity match
		bestValue := ""
		bestScore := -1

		for _, lp := range labeledPairs {
			lpKeyLower := strings.ToLower(lp.key)
			score := 0

			// Direct key match
			if lpKeyLower == keyLower || strings.ReplaceAll(lpKeyLower, " ", "_") == keyLower {
				score = 100
			} else {
				// Partial key word match
				for _, kw := range keyWords {
					if len(kw) >= 3 && strings.Contains(lpKeyLower, kw) {
						score += 30
					}
				}
			}

			if score > bestScore {
				bestScore = score
				bestValue = lp.value
			}
		}

		// Strategy 2: If no labeled match, try identifier proximity
		if bestScore < 30 {
			for _, word := range keyWords {
				if len(word) < 3 {
					continue
				}
				keyIdx := strings.Index(instructionLower, word)
				if keyIdx == -1 {
					continue
				}
				for _, id := range identifiers {
					dist := id.pos - keyIdx
					if dist < 0 {
						dist = -dist
					}
					if dist < 150 {
						proximityScore := 150 - dist
						if proximityScore > bestScore {
							bestScore = proximityScore
							bestValue = id.value
						}
					}
				}
			}
		}

		if bestValue != "" && bestScore >= 30 {
			if isEmpty {
				fmt.Fprintf(os.Stderr, "[Executor StringCoercion] Filling empty argument '%s': '' -> %q (score: %d)\n", key, bestValue, bestScore)
			} else {
				fmt.Fprintf(os.Stderr, "[Executor StringCoercion] Correcting hallucinated argument '%s': %q -> %q (score: %d)\n", key, strVal, bestValue, bestScore)
			}
			args[key] = bestValue
		}
	}
}

// GetNodeStateTolerant retrieves node state by attempting direct lookup,
// followed by suffix fallback mappings (_exec, _bridge) to remain robust
// against SCT graph expansions.
func GetNodeStateTolerant(taskID, nodeID string) (memory.NodeState, bool) {
	// 1. Try finding completed suffix expansions first since they represent actual execution outcomes
	if !strings.HasSuffix(nodeID, "_exec") && !strings.HasSuffix(nodeID, "_bridge") {
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_exec"); ok && (state.Status == "completed" || state.RawOutput != "") {
			return state, true
		}
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_bridge"); ok && (state.Status == "completed" || state.RawOutput != "") {
			return state, true
		}
	}

	// 2. Direct match
	if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
		return state, true
	}

	// 3. Try adding suffixes (even if not completed)
	if !strings.HasSuffix(nodeID, "_exec") && !strings.HasSuffix(nodeID, "_bridge") {
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_exec"); ok {
			return state, true
		}
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_bridge"); ok {
			return state, true
		}
	}

	// 4. Try removing suffixes
	if strings.HasSuffix(nodeID, "_exec") {
		baseID := strings.TrimSuffix(nodeID, "_exec")
		if state, ok := memory.DB.GetNodeState(taskID, baseID); ok {
			return state, true
		}
	}
	if strings.HasSuffix(nodeID, "_bridge") {
		baseID := strings.TrimSuffix(nodeID, "_bridge")
		if state, ok := memory.DB.GetNodeState(taskID, baseID); ok {
			return state, true
		}
	}

	return memory.NodeState{}, false
}

// getUpstreamValue fetches the exact property value from an upstream completed node state in SQLite.
func getUpstreamValue(taskID, nodeID, propertyKey string) string {
	state, ok := GetNodeStateTolerant(taskID, nodeID)
	if !ok {
		return ""
	}

	// Prefer RawOutput (clean tool response) when available, fall back to
	// Output with tier prefix stripped
	sourceOutput := state.RawOutput
	if sourceOutput == "" {
		sourceOutput = state.Output
		if idx := strings.Index(sourceOutput, "] "); idx != -1 {
			sourceOutput = sourceOutput[idx+2:]
		}
	}

	var outputMap map[string]interface{}
	if err := json.Unmarshal([]byte(sourceOutput), &outputMap); err != nil {
		// Try parsing as KV lines (compacted object notation)
		outputMap = make(map[string]interface{})
		lines := strings.Split(sourceOutput, "\n")
		for _, line := range lines {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if ls := strings.ToLower(val); ls == "true" {
					outputMap[key] = true
				} else if ls == "false" {
					outputMap[key] = false
				} else if num, err := strconv.ParseFloat(val, 64); err == nil {
					outputMap[key] = num
				} else {
					outputMap[key] = val
				}
			}
		}
	}

	val, found := outputMap[propertyKey]
	if !found {
		return ""
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
}

// resolveInterpolatedArguments resolves tool arguments that were derived from upstream node outputs.
// When the original instruction template contained {{nodes.X.output.Y}} references (now resolved
// to actual values in interpolatedInstruction), this function extracts those resolved values and
// overrides any incorrect GBNF-extracted arguments.
//
// This is the primary fix for the 0% pass rate on devops_incident and hr_onboarding categories,
// where nearly all arguments are dynamic upstream dependencies that the GBNF bridge hallucinates.
func resolveInterpolatedArguments(args map[string]interface{}, interpolatedInstruction string, originalInstruction string, taskID string) {
	if len(args) == 0 || originalInstruction == "" || taskID == "" {
		return
	}

	// Check if the original instruction contained interpolation variables
	if !strings.Contains(originalInstruction, "{{nodes.") {
		return
	}

	// Find all interpolation references in the ORIGINAL instruction template
	reProp := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\.([^}]+)\}\}`)
	matches := reProp.FindAllStringSubmatch(originalInstruction, -1)
	if len(matches) == 0 {
		return
	}

	type resolvedRef struct {
		propertyKey   string // e.g. "customer_id"
		resolvedValue string // the actual value after interpolation
		contextBefore string // text before the reference for key matching
	}

	var refs []resolvedRef

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		nodeID := match[1]
		propKey := match[2]

		resolvedValue := getUpstreamValue(taskID, nodeID, propKey)
		if resolvedValue == "" || resolvedValue == "null" {
			continue
		}

		// Find index of this match in originalInstruction for contextBefore
		fullMatchStr := match[0]
		matchIdx := strings.Index(originalInstruction, fullMatchStr)
		contextBefore := ""
		if matchIdx != -1 {
			contextStart := matchIdx - 80
			if contextStart < 0 {
				contextStart = 0
			}
			contextBefore = strings.ToLower(originalInstruction[contextStart:matchIdx])
		}

		refs = append(refs, resolvedRef{
			propertyKey:   propKey,
			resolvedValue: resolvedValue,
			contextBefore: contextBefore,
		})
	}

	if len(refs) == 0 {
		return
	}

	// Match resolved references to argument keys
	for key, val := range args {
		var strVal string
		isStr := false
		var isNum bool
		var numVal float64

		switch v := val.(type) {
		case string:
			strVal = v
			isStr = true
		case float64:
			numVal = v
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		case int:
			numVal = float64(v)
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		case int64:
			numVal = float64(v)
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		case float32:
			numVal = float64(v)
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		}

		if !isStr && !isNum {
			continue
		}

		keyLower := strings.ToLower(key)
		keyWords := strings.Split(strings.ReplaceAll(keyLower, "_", " "), " ")

		for _, ref := range refs {
			// Match 1: Direct property key to argument key match
			refKeyLower := strings.ToLower(ref.propertyKey)
			refKeyNorm := strings.ReplaceAll(refKeyLower, "_", " ")

			matched := false

			// Exact key match (e.g. "customer_id" arg matches {{...output.customer_id}})
			if keyLower == refKeyLower {
				matched = true
			}

			// Semantic key overlap (e.g. "service" arg matches context containing "service")
			if !matched {
				for _, kw := range keyWords {
					if len(kw) >= 3 && (strings.Contains(refKeyNorm, kw) || strings.Contains(ref.contextBefore, kw)) {
						matched = true
						break
					}
				}
			}

			if matched {
				if isNum {
					refNum, err := strconv.ParseFloat(ref.resolvedValue, 64)
					if err == nil {
						if numVal != refNum {
							fmt.Fprintf(os.Stderr, "[Executor InterpolationResolver] Correcting numeric argument '%s': %v -> %v (from upstream {{nodes.*.output.%s}})\n", key, numVal, refNum, ref.propertyKey)
							args[key] = refNum
							break
						}
					}
				} else if isStr {
					if strVal != ref.resolvedValue {
						// Only override if the current value is wrong (not in instruction)
						if strVal == "" || strVal == "null" || !strings.Contains(strings.ToLower(interpolatedInstruction), strings.ToLower(strVal)) {
							fmt.Fprintf(os.Stderr, "[Executor InterpolationResolver] Correcting argument '%s': %q -> %q (from upstream {{nodes.*.output.%s}})\n", key, strVal, ref.resolvedValue, ref.propertyKey)
							args[key] = ref.resolvedValue
							break
						}
					}
				}
			}
		}
	}
}

var comparisonRegex = regexp.MustCompile(`^\s*(.*?)\s*(==|!=|>=|<=|>|<)\s*(.*?)\s*$`)

func (e *ExecutionEngine) propagateSkip(graph *compiler.ExecutionGraph, skippedNodeID string) {
	for _, edge := range graph.Edges {
		if edge.SourceID == skippedNodeID {
			childID := edge.TargetID

			// Get current status of child
			state, ok := memory.DB.GetNodeState(graph.TaskID, childID)
			if !ok || state.Status != "skipped" {
				fmt.Fprintf(os.Stderr, "[Executor] Propagating skip from %s to %s\n", skippedNodeID, childID)
				_ = memory.DB.SetNodeState(graph.TaskID, childID, "skipped", "Parent node was skipped")
				e.getPublisher().PublishEvent("node_skipped", graph.TaskID, childID, "Parent node was skipped")

				// Send stream update
				if statePayload, err := json.Marshal(map[string]string{"status": "skipped", "output": "Parent node was skipped"}); err == nil {
					e.getPublisher().PublishStream(stream.StreamChunk{
						Source:  "executor",
						TaskID:  graph.TaskID,
						NodeID:  childID,
						Type:    "node_state",
						Content: string(statePayload),
					})
				}

				// Recurse downstream
				e.propagateSkip(graph, childID)
			}
		}
	}
}

func (e *ExecutionEngine) evaluateBranchCondition(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode) (bool, error) {
	// 1. Interpolate variables in the condition expression
	interpolated := InterpolateVariables(node.Condition, graph.TaskID)
	fmt.Fprintf(os.Stderr, "[Executor Branch] Evaluating branch condition: '%s' (Interpolated: '%s')\n", node.Condition, interpolated)

	// 2. Deterministic JSONPath / simple variable comparison check first
	if val, ok := evaluateDeterministicCondition(interpolated); ok {
		fmt.Fprintf(os.Stderr, "[Executor Branch] Deterministic evaluation succeeded: %v\n", val)
		// If it is true, return true immediately (no fallback).
		// If it is false, we fallback to the local model to double-check and prevent false negatives!
		if val {
			return true, nil
		}
		fmt.Fprintf(os.Stderr, "[Executor Branch] Deterministic comparison returned false; falling back to semantic Local Model to prevent false negative.\n")
	}

	// 3. Fallback to semantic Local Model (The Tactician decision seam)
	fmt.Fprintf(os.Stderr, "[Executor Branch] Initiating semantic evaluation for branch condition '%s'\n", node.Condition)

	// Collect completed nodes outputs to build semantic context
	var historyBuilder strings.Builder
	for _, n := range graph.Nodes {
		if state, ok := memory.DB.GetNodeState(graph.TaskID, n.ID); ok && (state.Status == "completed" || state.Status == "skipped") {
			historyBuilder.WriteString(fmt.Sprintf("Node: %s (Type: %s, Action: %s)\nOutput: %s\n\n", n.ID, n.Type, n.Action, state.Output))
		}
	}

	userPrompt := fmt.Sprintf(
		"CONDITION TO EVALUATE:\n%s\n\nEXECUTION HISTORY / CONTEXT:\n%s\nDetermine if the condition is satisfied based on the execution history above. Respond strictly in the required JSON format.",
		node.Condition,
		historyBuilder.String(),
	)

	schema := `{
		"type": "object",
		"properties": {
			"satisfied": {
				"type": "boolean"
			}
		},
		"required": ["satisfied"]
	}`

	req := inference.StructuredInferenceRequest{
		SystemPrompt: "You are the Branch Condition Evaluator. Your job is to evaluate if a given condition is satisfied based on the provided execution history and context. Respond strictly with JSON.",
		UserPrompt:   userPrompt,
		JSONSchema:   schema,
		TaskID:       graph.TaskID,
	}

	resStr, err := inference.GlobalLocalModel.ExecuteStructured(ctx, req)
	if err != nil {
		return false, fmt.Errorf("local model branch semantic evaluation call failed: %w", err)
	}

	var response struct {
		Satisfied bool `json:"satisfied"`
	}
	if err := json.Unmarshal([]byte(resStr), &response); err != nil {
		if strings.Contains(strings.ToLower(resStr), `"satisfied": true`) {
			return true, nil
		}
		return false, fmt.Errorf("failed to parse semantic branch response: %w (raw: %s)", err, resStr)
	}

	fmt.Fprintf(os.Stderr, "[Executor Branch] Semantic evaluation returned: %v\n", response.Satisfied)
	return response.Satisfied, nil
}

func evaluateDeterministicCondition(cond string) (bool, bool) {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return false, false
	}

	matches := comparisonRegex.FindStringSubmatch(cond)
	if len(matches) < 4 {
		lowerCond := strings.ToLower(cond)
		if lowerCond == "true" {
			return true, true
		}
		if lowerCond == "false" {
			return false, true
		}
		return false, false
	}

	lhsRaw := strings.TrimSpace(matches[1])
	op := matches[2]
	rhsRaw := strings.TrimSpace(matches[3])

	// Strip quotes if present
	stripQuotes := func(s string) string {
		if len(s) >= 2 {
			if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
				return s[1 : len(s)-1]
			}
		}
		return s
	}

	lhsClean := stripQuotes(lhsRaw)
	rhsClean := stripQuotes(rhsRaw)

	// Check if they are booleans
	parseBool := func(s string) (bool, bool) {
		ls := strings.ToLower(s)
		if ls == "true" {
			return true, true
		}
		if ls == "false" {
			return false, true
		}
		return false, false
	}

	lhsBool, lhsIsBool := parseBool(lhsClean)
	rhsBool, rhsIsBool := parseBool(rhsClean)

	if lhsIsBool && rhsIsBool {
		switch op {
		case "==":
			return lhsBool == rhsBool, true
		case "!=":
			return lhsBool != rhsBool, true
		default:
			return false, false
		}
	}

	// Check if they are numbers
	lhsNum, errL := strconv.ParseFloat(lhsClean, 64)
	rhsNum, errR := strconv.ParseFloat(rhsClean, 64)

	if errL == nil && errR == nil {
		switch op {
		case "==":
			return lhsNum == rhsNum, true
		case "!=":
			return lhsNum != rhsNum, true
		case ">":
			return lhsNum > rhsNum, true
		case ">=":
			return lhsNum >= rhsNum, true
		case "<":
			return lhsNum < rhsNum, true
		case "<=":
			return lhsNum <= rhsNum, true
		}
	}

	// String comparison fallback
	switch op {
	case "==":
		return lhsClean == rhsClean, true
	case "!=":
		return lhsClean != rhsClean, true
	}

	return false, false
}
