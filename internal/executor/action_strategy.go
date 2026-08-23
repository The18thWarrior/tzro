package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/skills"
	"tzro/internal/strategy"
	"tzro/internal/stream"
	"tzro/internal/tools"
)

// ActionStrategy handles tool dispatch action nodes — the final inline execution
// path extracted from executor.go. This completes the strategy registry migration
// (Plan Phase 6, ADR-0069).
//
// Action nodes resolve tool arguments via GBNF-constrained inference, apply
// deterministic coercion, execute the tool, compact/cache output, and persist
// state. The strategy is SelfManaged because it handles its own state lifecycle,
// hooks, and compaction internally.
type ActionStrategy struct {
	strategy.BaseStrategy

	// Injected functions — no *ExecutionEngine pointer
	publishState   func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
	recordDispatch func(taskID, toolName string, args map[string]interface{})
}

// NewActionStrategy wires the action strategy with injected engine functions.
// Accepts a base strategy from the registry to preserve metadata (PlannerCard, ContextRole).
func NewActionStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *ActionStrategy {
	return &ActionStrategy{
		BaseStrategy:   *base,
		publishState:   publishNodeState,
		recordDispatch: engine.RecordDispatch,
	}
}

// Execute runs the action node's tool dispatch pipeline.
func (s *ActionStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	taskID := nr.TaskID()
	node := nr.Node()
	graph := nr.Graph()
	interpolatedPrompt := nr.InterpolatedPrompt()
	executionTier := nr.ExecutionTier()
	meta := nr.Meta()

	// Update node state to running
	_ = memory.DB.SetNodeState(taskID, node.ID, "running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, fmt.Sprintf("Started %s", node.Action))
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	// Tool-existence validation with classification fallback.
	if tools.GetTool(node.Action) == nil {
		resolved := classifyToolName(ctx, node.Action, node.Instructions)
		if resolved != "" {
			fmt.Fprintf(os.Stderr, "[ActionStrategy] Tool validation: hallucinated '%s' → classified as '%s'\n", node.Action, resolved)
			node.Action = resolved
		} else {
			return nil, fmt.Errorf("tool '%s' is not registered and could not be classified to a known tool", node.Action)
		}
	}

	// Dynamic GBNF Schema selection
	schemaStr, schemaErr := tools.GetSchema(node.Action)
	if schemaErr != nil {
		fmt.Fprintf(os.Stderr, "[ActionStrategy Warning] Failed to get GBNF schema for action %s: %v. Using fallback.\n", node.Action, schemaErr)
		schemaStr = ""
	}

	// Fast path: zero-arg tools skip inference entirely
	if tools.IsZeroArgSchema(schemaStr) {
		return s.executeZeroArgTool(ctx, nr, interpolatedPrompt, executionTier)
	}

	// Build inference request for argument extraction
	req, inferenceResult, err := s.runArgumentExtraction(ctx, nr, schemaStr, interpolatedPrompt, meta)
	if err != nil {
		return nil, err
	}

	// Extract and coerce structured arguments
	toolArgs := s.extractAndCoerceArgs(inferenceResult, interpolatedPrompt, node, taskID, graph)

	// Schema validation gate (ADR-0020)
	isBenchmark := ctx.Value("is_benchmark") != nil
	if !isBenchmark {
		if validationErr := validateAgainstSchema(node.Action, toolArgs); validationErr != nil {
			if isCloudEscalationBlocked() {
				return nil, fmt.Errorf("schema validation failed for tool '%s': %w (cloud escalation blocked)", node.Action, validationErr)
			}
			fmt.Fprintf(os.Stderr, "[RetryPolicy] Schema validation failed for %s: %v — retrying with cloud\n", node.Action, validationErr)
			nr.Publisher().PublishEvent("schema_validation_failed", taskID, node.ID, validationErr.Error())

			cloudResult, cloudErr := retryWithCloud(ctx, req.Messages, schemaStr, taskID)
			if cloudErr == nil {
				go func() {
					_, _ = skills.ExtractCorrectiveSkill(context.Background(), node.Action, inferenceResult, cloudResult, node.Instructions)
				}()
				var cloudToolCall struct {
					ToolArguments map[string]interface{} `json:"tool_arguments"`
				}
				if json.Unmarshal([]byte(cloudResult), &cloudToolCall) == nil && len(cloudToolCall.ToolArguments) > 0 {
					toolArgs = cloudToolCall.ToolArguments
				}
			}
		}
	}

	// Execute tool
	output, err := tools.Call(ctx, node.Action, toolArgs)
	if err != nil {
		if isBenchmark {
			return nil, fmt.Errorf("tool '%s' execution failed: %w", node.Action, err)
		}
		if isCloudEscalationBlocked() {
			return nil, fmt.Errorf("tool '%s' execution failed: %w (cloud escalation blocked)", node.Action, err)
		}
		fmt.Fprintf(os.Stderr, "[RetryPolicy] Tool '%s' execution failed: %v — retrying with cloud\n", node.Action, err)
		nr.Publisher().PublishEvent("tool_execution_failed", taskID, node.ID, err.Error())

		cloudResult, cloudErr := retryWithCloud(ctx, req.Messages, schemaStr, taskID)
		if cloudErr == nil {
			go func() {
				_, _ = skills.ExtractCorrectiveSkill(context.Background(), node.Action, inferenceResult, cloudResult, node.Instructions)
			}()
			var cloudToolCall struct {
				ToolArguments map[string]interface{} `json:"tool_arguments"`
			}
			if json.Unmarshal([]byte(cloudResult), &cloudToolCall) == nil && len(cloudToolCall.ToolArguments) > 0 {
				output, err = tools.Call(ctx, node.Action, cloudToolCall.ToolArguments)
				if err != nil {
					return nil, fmt.Errorf("tool '%s' execution failed after cloud retry: %w", node.Action, err)
				}
			} else {
				output = cloudResult
			}
		} else {
			return nil, fmt.Errorf("tool '%s' execution failed (cloud retry also failed): %w", node.Action, err)
		}
	}

	// Run AfterNode hooks
	p := getExecutionParams(ctx)
	if p != nil {
		var nodeAfterAction HookAction = ActionContinue
		for _, h := range p.activeHooks {
			action, hookErr := h.AfterNode(ctx, taskID, node, &output)
			if hookErr != nil {
				return nil, fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, hookErr)
			}
			if action == ActionAbort {
				return nil, fmt.Errorf("AfterNode hook aborted execution for node %s", node.ID)
			}
			if action == ActionPause {
				nodeAfterAction = ActionPause
			}
		}
		if nodeAfterAction == ActionPause {
			_ = memory.DB.SetNodeState(taskID, node.ID, "pending", "Paused by hook")
			nr.Publisher().PublishEvent("node_paused", taskID, node.ID, "Execution paused by hook")
			return &strategy.ExecutionResult{SelfManaged: true, Directive: strategy.DirectivePause}, nil
		}
	}

	// Record dispatch for Execution Envelope assembly (ADR-0055)
	s.recordDispatch(taskID, node.Action, toolArgs)

	// Compact output & cache
	compactedOutput, derivedCacheID := s.compactAndCache(ctx, nr, output, interpolatedPrompt)

	// Store raw output for downstream interpolation
	rawOutputToStore := output
	if derivedCacheID != "" {
		rawOutputToStore = injectCacheIdIntoRawOutput(output, derivedCacheID)
	}
	_ = memory.DB.SetNodeRawOutput(taskID, node.ID, rawOutputToStore)

	if p != nil && p.nodeDelay > 0 {
		time.Sleep(p.nodeDelay)
	}

	// Persist completed state
	nodeStatus := fmt.Sprintf("[%s] %s", executionTier, compactedOutput)
	_ = memory.DB.SetNodeState(taskID, node.ID, "completed", nodeStatus)
	nr.Publisher().PublishEvent("node_completed", taskID, node.ID, nodeStatus)
	s.publishState(nr.Publisher(), taskID, node.ID, "completed", nodeStatus)

	fmt.Fprintf(os.Stderr, "[ActionStrategy] Completed Action Node: %s -> Status: Completed\n", node.ID)
	return &strategy.ExecutionResult{SelfManaged: true}, nil
}

// executeZeroArgTool handles tools with no required arguments — skips inference.
func (s *ActionStrategy) executeZeroArgTool(
	ctx context.Context,
	nr *strategy.NodeRuntime,
	interpolatedPrompt string,
	executionTier string,
) (*strategy.ExecutionResult, error) {
	taskID := nr.TaskID()
	node := nr.Node()

	fmt.Fprintf(os.Stderr, "[ActionStrategy] Zero-arg tool '%s' — skipping inference, calling directly.\n", node.Action)
	output, callErr := tools.Call(ctx, node.Action, map[string]interface{}{})
	if callErr != nil {
		return nil, fmt.Errorf("tool '%s' execution failed: %w", node.Action, callErr)
	}

	// Run AfterNode hooks
	p := getExecutionParams(ctx)
	if p != nil {
		var nodeAfterAction HookAction = ActionContinue
		for _, h := range p.activeHooks {
			action, hookErr := h.AfterNode(ctx, taskID, node, &output)
			if hookErr != nil {
				return nil, fmt.Errorf("AfterNode hook error for node %s: %w", node.ID, hookErr)
			}
			if action == ActionAbort {
				return nil, fmt.Errorf("AfterNode hook aborted execution for node %s", node.ID)
			}
			if action == ActionPause {
				nodeAfterAction = ActionPause
			}
		}
		if nodeAfterAction == ActionPause {
			_ = memory.DB.SetNodeState(taskID, node.ID, "pending", "Paused by hook")
			nr.Publisher().PublishEvent("node_paused", taskID, node.ID, "Execution paused by hook")
			return &strategy.ExecutionResult{SelfManaged: true, Directive: strategy.DirectivePause}, nil
		}
	}

	_ = memory.DB.SetNodeRawOutput(taskID, node.ID, output)

	compactedOutput, _ := s.compactAndCache(ctx, nr, output, interpolatedPrompt)

	if p != nil && p.nodeDelay > 0 {
		time.Sleep(p.nodeDelay)
	}

	nodeStatus := fmt.Sprintf("[%s] %s", executionTier, compactedOutput)
	_ = memory.DB.SetNodeState(taskID, node.ID, "completed", nodeStatus)
	nr.Publisher().PublishEvent("node_completed", taskID, node.ID, nodeStatus)
	s.publishState(nr.Publisher(), taskID, node.ID, "completed", nodeStatus)

	fmt.Fprintf(os.Stderr, "[ActionStrategy] Completed Zero-Arg Action Node: %s -> Status: Completed\n", node.ID)
	return &strategy.ExecutionResult{SelfManaged: true}, nil
}

// runArgumentExtraction builds inference request and runs GBNF-constrained generation.
func (s *ActionStrategy) runArgumentExtraction(
	ctx context.Context,
	nr *strategy.NodeRuntime,
	schemaStr string,
	interpolatedPrompt string,
	meta inference.StreamMeta,
) (inference.StructuredInferenceRequest, string, error) {
	node := nr.Node()
	taskID := nr.TaskID()
	graph := nr.Graph()

	ragCtx := memory.DB.GetGraphRAGContext(interpolatedPrompt, config.GetMaxRAGContextChars())

	var cacheIdRe = regexp.MustCompile(`(?i)(cacheId|cache_[a-zA-Z0-9]{8,})`)
	var isCacheExploration = cacheIdRe.MatchString(interpolatedPrompt)

	accumulatedCtx := buildAccumulatedContext(taskID, graph, node.Type)
	if isCacheExploration {
		accumulatedCtx = enrichCacheBridgeContext(ctx, accumulatedCtx, interpolatedPrompt)
	}

	var systemPrompt string
	if isCacheExploration {
		systemPrompt = fmt.Sprintf(
			"You are the Local Tactician Node Executor. Your job is to convert the dynamic user step instruction into structured tool parameters.\n\nALLOWED TOOLS:\n- %s\n- introspect_cache\n- sql_cached_data%s",
			node.Action,
			CacheExplorationGuide,
		)
	} else {
		systemPrompt = buildContextAwareSystemPrompt(node.Action, node.Instructions, schemaStr)
	}

	var req inference.StructuredInferenceRequest
	if !isCacheExploration && accumulatedCtx != "" {
		staticBase := buildStaticBaseInstruction(false)
		instruction := fmt.Sprintf("Extract structured tool parameters for '%s'.\n\n", node.Action) + node.Instructions + "\n\nResolved reference:\n" + interpolatedPrompt
		if ragCtx != "" {
			instruction = "## Additional Context\n\n" + ragCtx + "\n\n" + instruction
		}
		msgs := buildSegmentedMessages(staticBase, accumulatedCtx, schemaStr, instruction, false)
		req = inference.StructuredInferenceRequest{
			Messages:    msgs,
			JSONSchema:  schemaStr,
			StreamMeta:  &meta,
			TaskID:      taskID,
			IsLowStakes: true,
		}
	} else {
		goalPrompt := ""
		if graph != nil {
			goalPrompt = graph.GoalPrompt
		}
		userPrompt := buildContextAwareUserPromptWithGoal(goalPrompt, accumulatedCtx, ragCtx, interpolatedPrompt)
		req = inference.NewSimpleRequest(systemPrompt, userPrompt, schemaStr)
		req.StreamMeta = &meta
		req.TaskID = taskID
	}

	inferenceResult, err := inference.ExecuteWorkerStructured(ctx, req)
	if err != nil {
		return req, "", fmt.Errorf("node execution failed: %w", err)
	}

	return req, inferenceResult, nil
}

// extractAndCoerceArgs parses inference output and applies deterministic coercion.
func (s *ActionStrategy) extractAndCoerceArgs(
	inferenceResult string,
	interpolatedPrompt string,
	node *compiler.GraphNode,
	taskID string,
	graph *compiler.ExecutionGraph,
) map[string]interface{} {
	var toolCall struct {
		ToolArguments map[string]interface{} `json:"tool_arguments"`
	}
	if err := json.Unmarshal([]byte(inferenceResult), &toolCall); err != nil {
		toolCall.ToolArguments = extractToolArguments(inferenceResult)
	}
	coerceNumericArguments(toolCall.ToolArguments, interpolatedPrompt)
	coerceStringArguments(toolCall.ToolArguments, interpolatedPrompt, node.Action)
	resolveInterpolatedArguments(toolCall.ToolArguments, interpolatedPrompt, node.Instructions, taskID, graph)

	fmt.Fprintf(os.Stderr, "[ActionStrategy] Tool arguments extracted: %v\n", toolCall.ToolArguments)
	return toolCall.ToolArguments
}

// compactAndCache runs output compaction and cache storage.
func (s *ActionStrategy) compactAndCache(
	ctx context.Context,
	nr *strategy.NodeRuntime,
	output string,
	interpolatedPrompt string,
) (string, string) {
	taskID := nr.TaskID()
	node := nr.Node()

	if isCompactionDisabled(ctx) {
		return output, ""
	}

	compactedOutput, cacheID, err := cache.Process(ctx, output, interpolatedPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ActionStrategy Compactor Warning] Failed to process payload in cache: %v\n", err)
		return output, ""
	}
	if cacheID != "" {
		fmt.Fprintf(os.Stderr, "[ActionStrategy Compactor] Payload > 12KB. Saved to SQLite and disk cache -> CacheID: %s\n", cacheID)
		nr.Publisher().PublishEvent("cache_envelope_created", taskID, node.ID, fmt.Sprintf("Cached %s output to SQLite and disk (%dKB) -> CacheID: %s", node.Action, len(output)/1024, cacheID))
	}
	return compactedOutput, cacheID
}
