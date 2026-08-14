package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"tzro/internal/strategy"
	"tzro/internal/telemetry"
)

type HookAction string

type SubTaskSpawner func(ctx context.Context, action string, inputs map[string]interface{}, parentTaskID, parentNodeID string) (string, error)

var SpawnSubTask SubTaskSpawner

// ---------------------------------------------------------------------------
// Execution-scoped parameters — carried via context.Context to delegates
// ---------------------------------------------------------------------------

// executionParamsKey is the context key for executionParams.
type executionParamsKey struct{}

// executionParams bundles pre-computed values shared across node dispatch.
// Stored in context.Context so delegates can extract them without signature changes.
type executionParams struct {
	activeHooks        []ExecutionHook
	executionTier      string
	meta               inference.StreamMeta
	interpolatedPrompt string
	nodeDelay          time.Duration
}

// getExecutionParams extracts executionParams from context, returning zero-value
// params if not set (safe for branch nodes which skip pre-flight).
func getExecutionParams(ctx context.Context) *executionParams {
	if p, ok := ctx.Value(executionParamsKey{}).(*executionParams); ok {
		return p
	}
	return &executionParams{}
}


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
	// OnEdgeTraversal fires when an edge is traversed in the ready-queue execution model (ADR-0024).
	// Called after a source node completes and before the target node begins execution.
	// The edgeThought may be nil if the target has ActivationThreshold 0.0.
	OnEdgeTraversal(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (HookAction, error)
}

var ErrTaskPaused = fmt.Errorf("task execution paused by hook")

type ExecutionEngine struct {
	Publisher      telemetry.EventPublisher
	EdgeThoughtGen EdgeThoughtInference // optional: enables neural edge traversal (ADR-0024)
	ProgressGuard  *GoalProgressGuard   // optional: prevents sufficiency hallucinations
	hooks          []ExecutionHook
	mutex          sync.Mutex
	Sequential     bool // If true, execute nodes one by one (ADR-0040)

	// ADR-0069: Strategy Registry for composable node types.
	Registry *strategy.StrategyRegistry

	// ADR-0055: In-memory tool dispatch accumulator for Execution Envelope assembly.
	// Keyed by taskID. Populated at tool call sites, drained at task completion.
	dispatches map[string][]ToolDispatch
	dispatchMu sync.Mutex

	// ADR-0067: In-memory VTE result stash for Execution Envelope population.
	// Keyed by taskID. Populated after VerifyTaskOutput, drained at envelope assembly.
	verificationResults map[string]*VerificationResult
	verificationMu      sync.Mutex
}

// activeRegistry is the package-level strategy registry, set during InitRegistry.
// Used by standalone functions (e.g., executor_context.go) that need ContextRole
// lookups without holding an ExecutionEngine reference.
var activeRegistry *strategy.StrategyRegistry

// InitRegistry creates and populates the strategy registry with built-in
// strategies, then replaces each metadata-only BaseStrategy stub with the
// concrete strategy implementation that owns its Execute method.
func (e *ExecutionEngine) InitRegistry() {
	reg := strategy.NewStrategyRegistry()
	strategy.RegisterBuiltins(reg)

	// Replace BaseStrategy stubs with concrete, strategy-owned implementations.
	e.wireStrategies(reg)

	e.Registry = reg
	activeRegistry = reg

	// ADR-0069: Wire ActiveExpander so custom strategy CompilationRules
	// participate in graph expansion.
	compiler.ActiveExpander = &strategyNodeExpander{registry: reg}
}

// strategyNodeExpander implements compiler.NodeExpander by delegating to
// the strategy registry's CompilationRules.
type strategyNodeExpander struct {
	registry *strategy.StrategyRegistry
}

// Expand checks if the strategy for this node type has CompilationRules.
// Returns nil to fall through to the compiler's built-in logic.
func (e *strategyNodeExpander) Expand(node *compiler.GraphNode, graph *compiler.ExecutionGraph) (*compiler.NodeExpansionResult, error) {
	s, ok := e.registry.Get(node.Type)
	if !ok {
		return nil, nil
	}
	rules := s.CompilationRules()
	if rules == nil || rules.Expand == nil {
		return nil, nil
	}
	result, err := rules.Expand(node, graph)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	// Convert strategy.ExpansionResult → compiler.NodeExpansionResult
	return &compiler.NodeExpansionResult{
		ReplacementNodes: result.ReplacementNodes,
		AdditionalNodes:  result.AdditionalNodes,
		AdditionalEdges:  result.AdditionalEdges,
		ModifiedNode:     result.ModifiedNode,
	}, nil
}

// wireStrategies replaces each builtin BaseStrategy stub with a concrete
// strategy implementation that owns its Execute method. Each strategy
// preserves the metadata (PlannerCard, ContextRole) from its BaseStrategy
// stub while providing its own execution logic.
func (e *ExecutionEngine) wireStrategies(reg *strategy.StrategyRegistry) {
	// Branch
	if base := findBaseStrategy(reg, "branch"); base != nil {
		_ = reg.Replace(NewBranchStrategy(e, base))
	}

	// Semantic Validator
	if base := findBaseStrategy(reg, "semantic_validator"); base != nil {
		_ = reg.Replace(NewSemanticValidatorStrategy(e, base))
	}

	// Deterministic
	if base := findBaseStrategy(reg, "deterministic"); base != nil {
		_ = reg.Replace(NewDeterministicStrategy(e, base))
	}

	// Probe
	if base := findBaseStrategy(reg, "probe"); base != nil {
		_ = reg.Replace(NewProbeAnalyzeStrategy(e, base, "probe"))
	}

	// Analyze (shares ProbeAnalyzeStrategy with probe)
	if base := findBaseStrategy(reg, "analyze"); base != nil {
		_ = reg.Replace(NewProbeAnalyzeStrategy(e, base, "analyze"))
	}

	// Recall
	if base := findBaseStrategy(reg, "recall"); base != nil {
		_ = reg.Replace(NewRecallStrategy(e, base))
	}

	// Synthesis
	if base := findBaseStrategy(reg, "synthesis"); base != nil {
		_ = reg.Replace(NewSynthesisStrategy(e, base))
	}

	// Sub-DAG
	if base := findBaseStrategy(reg, "sub_dag"); base != nil {
		_ = reg.Replace(NewSubDAGStrategy(e, base))
	}

	// Scatter Assembly
	if base := findBaseStrategy(reg, "scatter_assembly"); base != nil {
		_ = reg.Replace(NewScatterAssemblyStrategy(e, base))
	}

	// Action
	if base := findBaseStrategy(reg, "action"); base != nil {
		_ = reg.Replace(NewActionStrategy(e, base))
	}
}

// GetNodeTypeReferenceCard returns the dynamic NodeTypeReferenceCard built from
// all registered strategies' PlannerCards. Falls back to the static template
// constant when the registry hasn't been initialized (e.g., during tests).
func (e *ExecutionEngine) RecordDispatch(taskID, toolName string, args map[string]interface{}) {
	e.dispatchMu.Lock()
	defer e.dispatchMu.Unlock()
	if e.dispatches == nil {
		e.dispatches = make(map[string][]ToolDispatch)
	}
	e.dispatches[taskID] = append(e.dispatches[taskID], ToolDispatch{ToolName: toolName, Args: args})
}

// DrainDispatches returns all dispatches for a task and removes them from the map.
// Called once at task completion for envelope assembly; the slice is GC'd after.
func (e *ExecutionEngine) DrainDispatches(taskID string) []ToolDispatch {
	e.dispatchMu.Lock()
	defer e.dispatchMu.Unlock()
	if e.dispatches == nil {
		return nil
	}
	result := e.dispatches[taskID]
	delete(e.dispatches, taskID)
	return result
}

// stashVerificationResult stores the VTE result for later envelope assembly (ADR-0067).
func (e *ExecutionEngine) stashVerificationResult(taskID string, result *VerificationResult) {
	e.verificationMu.Lock()
	defer e.verificationMu.Unlock()
	if e.verificationResults == nil {
		e.verificationResults = make(map[string]*VerificationResult)
	}
	e.verificationResults[taskID] = result
}

// DrainVerificationResult returns and removes the VTE result for a task.
// Called at envelope assembly time.
func (e *ExecutionEngine) DrainVerificationResult(taskID string) *VerificationResult {
	e.verificationMu.Lock()
	defer e.verificationMu.Unlock()
	if e.verificationResults == nil {
		return nil
	}
	result := e.verificationResults[taskID]
	delete(e.verificationResults, taskID)
	return result
}

func (e *ExecutionEngine) RegisterHook(h ExecutionHook) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.hooks = append(e.hooks, h)
}

// UnregisterHook removes a specific hook by pointer identity.
// Used for task-scoped hooks that should be cleaned up after execution.
func (e *ExecutionEngine) UnregisterHook(h ExecutionHook) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	for i, existing := range e.hooks {
		if existing == h {
			e.hooks = append(e.hooks[:i], e.hooks[i+1:]...)
			return
		}
	}
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

var GlobalEngine = &ExecutionEngine{
	EdgeThoughtGen: &DefaultEdgeThoughtInference{},
	ProgressGuard:  &GoalProgressGuard{},
}

func init() {
	GlobalEngine.InitRegistry()
}

const CacheExplorationGuide = `

### DISK-BACKED CACHE EXPLORATION GUIDE
A previous step resulted in a large payload that has been cached on disk to protect the context window.
You have access to the following special tools to explore and query this cached data:

1. 'introspect_cache': Retrieve schema, field lists, types, and sample record of the cached payload.
   Format: {"tool_arguments": {"cacheId": "cache_..."}}
2. 'sql_cached_data': Query the cached data using standard SQL. The table name is the cacheId.
   Format: {"tool_arguments": {"cacheId": "cache_...", "sql": "SELECT Sector, COUNT(*) as cnt FROM cache_... GROUP BY Sector ORDER BY cnt DESC"}}

If you need to analyze, filter, paginate, or count records from the cache, you MUST use one of these tools.`

// ExecuteGraph runs the compiled topological execution levels.
// It executes nodes at the same Kahn level in parallel via goroutines,
// writing states to memory and pushing audit events to the observer.
func (e *ExecutionEngine) ExecuteGraph(ctx context.Context, graph *compiler.ExecutionGraph, levels [][]string) error {
	if e.Registry == nil {
		e.InitRegistry()
	}

	e.mutex.Lock()
	defer e.mutex.Unlock()

	_, levelDelay := getDelays(ctx)

	// P2: Weighted circuit breaker budget
	// Compute time budget from node composition and apply config multiplier.
	budget := compiler.ComputeTimeBudget(graph)
	multiplier := config.Get().CircuitBreakerMultiplier
	if multiplier <= 0 {
		multiplier = 1.0 // default
	}
	budget = time.Duration(float64(budget) * multiplier)
	budgetCtx, budgetCancel := context.WithTimeout(ctx, budget)
	defer budgetCancel()

	fmt.Fprintf(os.Stderr, "[Executor] Starting execution for Task %s with %d topological levels (budget: %s, multiplier: %.1fx)...\n", graph.TaskID, len(levels), budget, multiplier)
	e.getPublisher().PublishEvent("task_started", graph.TaskID, "", fmt.Sprintf("Task execution initiated (budget: %s)", budget))

	// Pre-task GC: Clear KV cache slots from previous tasks to prevent
	// memory pressure degradation in sequential runs (e.g., benchmarks).
	_ = inference.GlobalLocalModel.TriggerGC(budgetCtx)

	// Resilient task resumption: Cache the execution graph for recovery/resume
	db := memory.DB.RawDB()
	if db != nil && graph != nil {
		graphBytes, err := json.Marshal(graph)
		if err == nil {
			createdAt := time.Now().Unix()
			_, _ = db.Exec("INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
				"graph_"+graph.TaskID, string(graphBytes), "", createdAt)
		}
	}

	// Pre-populate states as pending only if not already completed or skipped (for resilient resumes)
	for _, node := range graph.Nodes {
		if state, ok := memory.DB.GetNodeState(graph.TaskID, node.ID); !ok || (state.Status != "completed" && state.Status != "skipped") {
			_ = memory.DB.SetNodeState(graph.TaskID, node.ID, "pending", "")
		}
	}

	var allCompletedStates []memory.NodeState
	startTime := time.Now() // ADR-0055: Capture for Execution Envelope duration

	activeHooks := e.getHooksUnlocked()

	for levelIdx, level := range levels {
		// P2: Circuit breaker timeout check
		if budgetCtx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(os.Stderr, "[Executor] Task %s timed out (budget: %s). Marking remaining nodes and forcing terminal synthesis.\n",
				graph.TaskID, budget)
			e.getPublisher().PublishEvent("task_circuit_breaker", graph.TaskID, "",
				fmt.Sprintf("Circuit breaker triggered after %s budget. Remaining levels: %d", budget, len(levels)-levelIdx))

			// Mark all remaining pending nodes as timed_out
			for _, remainingLevel := range levels[levelIdx:] {
				for _, nodeID := range remainingLevel {
					if state, ok := memory.DB.GetNodeState(graph.TaskID, nodeID); ok && state.Status == "pending" {
						// Don't mark terminal_synthesis as timed_out — let it run
						if nodeID == "terminal_synthesis" {
							continue
						}
						_ = memory.DB.SetNodeState(graph.TaskID, nodeID, "timed_out", "Circuit breaker triggered")
					}
				}
			}
			break
		}

		// Context cancellation check (e.g., from /api/tasks/cancel)
		if ctx.Err() == context.Canceled {
			fmt.Fprintf(os.Stderr, "[Executor] Task %s cancelled by user. Marking remaining nodes.\n", graph.TaskID)
			e.getPublisher().PublishEvent("task_cancelled", graph.TaskID, "",
				fmt.Sprintf("Task cancelled by user. Remaining levels: %d", len(levels)-levelIdx))

			for _, remainingLevel := range levels[levelIdx:] {
				for _, nodeID := range remainingLevel {
					if state, ok := memory.DB.GetNodeState(graph.TaskID, nodeID); ok && state.Status == "pending" {
						_ = memory.DB.SetNodeState(graph.TaskID, nodeID, "cancelled", "Task cancelled by user")
					}
				}
			}
			return fmt.Errorf("task %s cancelled by user", graph.TaskID)
		}

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

		// Brief delay between levels for visual representation in GUI (500ms default)
		if levelDelay > 0 {
			time.Sleep(levelDelay)
		}
	}

	// Synthesis SOP skill on successful completion
	fmt.Fprintf(os.Stderr, "[Executor] Task %s completed successfully. Synthesizing SOP...\n", graph.TaskID)
	e.getPublisher().PublishEvent("task_completed", graph.TaskID, "", "Task execution completed successfully")
	_, _ = notification.Send(ctx, "executor", "info", "Task Completed Successfully", fmt.Sprintf("Task '%s' completed all topological levels successfully.", graph.TaskID), notification.WithTaskID(graph.TaskID))

	// ADR-0055: Assemble and persist the Execution Envelope
	dispatches := e.DrainDispatches(graph.TaskID)
	envelope := AssembleEnvelope(graph, allCompletedStates, dispatches, startTime)

	// ADR-0067: Populate verification rubric from VTE if available
	if vResult := e.DrainVerificationResult(graph.TaskID); vResult != nil {
		envelope.Verification = vResult
	}

	if envJSON, err := json.Marshal(envelope); err == nil {
		// Find the effective terminal node to persist the envelope on
		terminalNodeID := findTerminalNodeID(graph, allCompletedStates)
		if terminalNodeID != "" {
			_ = memory.DB.SetNodeStructuredOutput(graph.TaskID, terminalNodeID, string(envJSON))
		}
		fmt.Fprintf(os.Stderr, "[Executor] Assembled Execution Envelope for task %s (%d tools, %d files read, %d files modified)\n",
			graph.TaskID, len(envelope.ToolsUsed), len(envelope.FilesRead), len(envelope.FilesModified))
	}

	// Clean up ephemeral cache tables for this task
	cache.DropTaskTables(graph.TaskID)

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
	nodeDelay, _ := getDelays(ctx)

	// 0. Pre-flight Check: Is node already completed or skipped?
	if state, ok := memory.DB.GetNodeState(taskID, node.ID); ok {
		if state.Status == "completed" {
			fmt.Fprintf(os.Stderr, "[Executor] Node %s is already completed. Skipping execution.\n", node.ID)
			return nil
		}
		if state.Status == "skipped" {
			fmt.Fprintf(os.Stderr, "[Executor] Node %s is skipped. Skipping execution.\n", node.ID)
			return nil
		}
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
	// Branch is special: evaluated BEFORE pre-flight to avoid unnecessary
	// interpolation and tier detection for condition-only nodes.
	if node.Condition != "" || node.Type == "branch" {
		if s, ok := e.Registry.Get("branch"); ok {
			return e.dispatchViaStrategy(ctx, graph, node, s, activeHooks, nodeDelay)
		}
		return fmt.Errorf("branch strategy not registered for node %s", node.ID)
	}


	// 1. Pre-flight: compute shared execution parameters
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

	// ADR-0060: Inject GenerationGuard into context for streaming inference.
	// RepetitionGuard detects degenerate repetition during generation and
	// aborts early, preventing 100K+ token waste on loops.
	ctx = context.WithValue(ctx, inference.GenerationGuardKey, inference.NewRepetitionGuard())

	// 1. Pre-flight Variable Interpolation
	interpolatedPrompt := InterpolateVariables(node.Instructions, taskID)
	fmt.Fprintf(os.Stderr, "[Executor] Interpolated instruction: %s\n", interpolatedPrompt)

	// 2. Registry-based Strategy Dispatch (ADR-0069)
	// Store pre-computed params in context for delegate extraction.
	ctx = context.WithValue(ctx, executionParamsKey{}, &executionParams{
		activeHooks:        activeHooks,
		executionTier:      executionTier,
		meta:               meta,
		interpolatedPrompt: interpolatedPrompt,
		nodeDelay:          nodeDelay,
	})

	if e.Registry != nil {
		if s, ok := e.Registry.Get(node.Type); ok {
			return e.dispatchViaStrategy(ctx, graph, node, s, activeHooks, nodeDelay)
		}
	}

	// No strategy registered for this node type — this should not happen.
	return fmt.Errorf("no strategy registered for node type %q (node %s)", node.Type, node.ID)
}


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

	req := inference.NewSimpleRequest("You are the Branch Condition Evaluator. Your job is to evaluate if a given condition is satisfied based on the provided execution history and context. Respond strictly with JSON.", userPrompt, schema)
	req.TaskID = graph.TaskID

	resStr, err := inference.ExecuteRouterStructured(ctx, req)
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
