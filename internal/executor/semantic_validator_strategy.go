package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// SemanticValidatorStrategy — strategy-owned Execute (ADR-0069)
// ---------------------------------------------------------------------------

// SemanticValidatorStrategy extracts structured tool parameters from natural
// language using a two-pass approach: free-form XML extraction followed by
// GBNF-constrained JSON refinement. Includes confidence-based cloud
// escalation and F1 gate for schema satisfaction.
type SemanticValidatorStrategy struct {
	strategy.BaseStrategy
	engine *ExecutionEngine
}

// NewSemanticValidatorStrategy creates a SemanticValidatorStrategy.
func NewSemanticValidatorStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *SemanticValidatorStrategy {
	return &SemanticValidatorStrategy{
		BaseStrategy: *base,
		engine:       engine,
	}
}

// Execute runs the parameter extraction chain and returns the validated output.
// The dispatch envelope handles state persistence, AfterNode hooks, and events.
func (s *SemanticValidatorStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID,
		fmt.Sprintf("Started %s Validator", node.Action))
	s.engine.publishNodeStateStream(taskID, node.ID, "running", "")

	// Run the domain-core parameter extraction logic
	inferenceResult, err := s.engine.runSemanticValidatorCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("semantic validator %s failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[SemanticValidatorStrategy] %s completed (%d chars)\n", node.ID, len(inferenceResult))

	executionTier := "Local"
	if nr.ExecutionTier() != "" {
		executionTier = nr.ExecutionTier()
	}
	output := fmt.Sprintf("[%s] %s", executionTier, inferenceResult)

	return &strategy.ExecutionResult{
		Output:    output,
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Type returns the node type identifier.
func (s *SemanticValidatorStrategy) Type() string { return s.BaseStrategy.Type() }

// PlannerCard delegates to embedded BaseStrategy.
func (s *SemanticValidatorStrategy) PlannerCard() *strategy.PlannerCard {
	return s.BaseStrategy.PlannerCard()
}

// CompilationRules delegates to embedded BaseStrategy.
func (s *SemanticValidatorStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *SemanticValidatorStrategy) ContextRole() *strategy.ContextRole {
	return s.BaseStrategy.ContextRole()
}

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *SemanticValidatorStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — semantic_validator uses imperative Execute.
func (s *SemanticValidatorStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef {
	return nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*SemanticValidatorStrategy)(nil)
