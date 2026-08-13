package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// SynthesisStrategy — strategy-owned Execute for synthesis nodes (ADR-0069)
// ---------------------------------------------------------------------------

// SynthesisStrategy compiles upstream action outputs into a final cohesive
// response using the local model, with optional VTE verification for
// terminal synthesis nodes (ADR-0067).
type SynthesisStrategy struct {
	strategy.BaseStrategy
	engine *ExecutionEngine
}

// NewSynthesisStrategy creates a SynthesisStrategy.
func NewSynthesisStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *SynthesisStrategy {
	return &SynthesisStrategy{
		BaseStrategy: *base,
		engine:       engine,
	}
}

// Execute builds context, runs inference, optionally verifies via VTE, and
// returns the synthesis output. The dispatch envelope handles state
// persistence, AfterNode hooks, and events.
func (s *SynthesisStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, "Synthesizing final response")
	s.engine.publishNodeStateStream(taskID, node.ID, "running", "")

	systemPrompt := "You are the Local Tactician Node Executor. " +
		"Compile all prior action outputs and query results into a final cohesive response. " +
		"The accumulated context below contains data retrieved by prior nodes — use it directly. " +
		"If query results are provided, include the actual data values in your response."
	accumulatedCtx := buildAccumulatedContext(taskID, graph, "synthesis")
	userPrompt := buildContextAwareUserPrompt(accumulatedCtx, "", nr.InterpolatedPrompt())

	req := inference.NewSimpleRequest(systemPrompt, userPrompt, "")
	// ADR-0045: Token-level streaming gated by compiler-set StreamOutput flag.
	meta := nr.Meta()
	if node.StreamOutput && meta != (inference.StreamMeta{}) {
		req.StreamMeta = &meta
	}
	req.TaskID = taskID

	// Full context window for synthesis generation
	synthCtx := context.WithValue(ctx, inference.MaxTokensKey, 65536)
	inferenceResult, err := inference.ExecuteWorkerStructured(synthCtx, req)
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("synthesis node execution failed: %v", err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	// ADR-0067: Verified Task Execution for terminal synthesis nodes.
	if graph.GoalPrompt != "" {
		finalSynthesis, _, vErr := VerifyTaskOutput(
			ctx,
			&DefaultCloudVerifier{},
			graph.GoalPrompt,
			inferenceResult,
			accumulatedCtx,
			false,
		)
		if vErr == nil {
			inferenceResult = finalSynthesis
		} else {
			fmt.Fprintf(os.Stderr, "[SynthesisStrategy] VTE error (non-fatal): %v\n", vErr)
		}
	}

	// Format output with execution tier prefix
	executionTier := "Local"
	if nr.ExecutionTier() != "" {
		executionTier = nr.ExecutionTier()
	}
	output := fmt.Sprintf("[%s] %s", executionTier, inferenceResult)

	// Return output — envelope handles hooks + state
	return &strategy.ExecutionResult{
		Output:    output,
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Type returns the node type identifier.
func (s *SynthesisStrategy) Type() string { return s.BaseStrategy.Type() }

// PlannerCard delegates to embedded BaseStrategy.
func (s *SynthesisStrategy) PlannerCard() *strategy.PlannerCard { return s.BaseStrategy.PlannerCard() }

// CompilationRules delegates to embedded BaseStrategy.
func (s *SynthesisStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *SynthesisStrategy) ContextRole() *strategy.ContextRole { return s.BaseStrategy.ContextRole() }

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *SynthesisStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — synthesis uses imperative Execute.
func (s *SynthesisStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef { return nil }

// Compile-time interface check.
var _ strategy.NodeStrategy = (*SynthesisStrategy)(nil)
