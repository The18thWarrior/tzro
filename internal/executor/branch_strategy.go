package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// BranchStrategy — first strategy-owned Execute implementation (ADR-0069)
// ---------------------------------------------------------------------------

// BranchStrategy is the first node type to own its Execute logic directly,
// replacing the DelegateFunc pattern. It evaluates branch conditions and
// returns DirectiveContinue (satisfied) or DirectiveSkipDownstream (not satisfied).
//
// State management is done through NodeRuntime capabilities (StatePersister,
// EventPublisher), not through direct memory.DB or engine.getPublisher() calls.
type BranchStrategy struct {
	strategy.BaseStrategy
	engine *ExecutionEngine
}

// NewBranchStrategy creates a BranchStrategy with the engine reference needed
// for evaluateBranchCondition. The BaseStrategy provides PlannerCard, ContextRole,
// and other metadata.
func NewBranchStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *BranchStrategy {
	return &BranchStrategy{
		BaseStrategy: *base,
		engine:       engine,
	}
}

// Execute evaluates the branch condition and returns the appropriate directive.
// The dispatch envelope handles all ceremony: state persistence, events,
// hooks, and downstream propagation.
func (s *BranchStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()

	satisfied, err := s.engine.evaluateBranchCondition(ctx, graph, node)
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("Branch evaluation failed: %v", err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	if satisfied {
		fmt.Fprintf(os.Stderr, "[BranchStrategy] Node %s condition satisfied!\n", node.ID)
		return &strategy.ExecutionResult{
			Output:    "Condition satisfied",
			Directive: strategy.DirectiveContinue,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[BranchStrategy] Node %s condition NOT satisfied. Skipping downstream.\n", node.ID)
	return &strategy.ExecutionResult{
		Output:    "Condition not satisfied",
		Directive: strategy.DirectiveSkipDownstream,
	}, nil
}


// mustMarshalState returns a JSON state payload, falling back to a simple string on error.
func mustMarshalState(status, output string) string {
	return fmt.Sprintf(`{"status":"%s","output":"%s"}`, status, output)
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*BranchStrategy)(nil)

// findBaseStrategy looks up a strategy in the registry and returns the BaseStrategy
// if it's a BaseStrategy type. Used during InitRegistry to extract metadata
// (PlannerCard, ContextRole) from the builtin stub before replacing it.
func findBaseStrategy(reg *strategy.StrategyRegistry, nodeType string) *strategy.BaseStrategy {
	s, ok := reg.Get(nodeType)
	if !ok {
		return nil
	}
	if bs, ok := s.(*strategy.BaseStrategy); ok {
		return bs
	}
	return nil
}

// replaceBranchWithOwnedStrategy replaces the builtin branch stub with
// a strategy-owned BranchStrategy. Called from wireStrategyDelegates.
func (e *ExecutionEngine) replaceBranchWithOwnedStrategy(reg *strategy.StrategyRegistry) {
	base := findBaseStrategy(reg, "branch")
	if base == nil {
		return
	}
	// Create the owned strategy, preserving all metadata from the builtin stub
	owned := NewBranchStrategy(e, base)
	// Replace the builtin stub with the owned strategy
	_ = reg.Replace(owned)
}


// Type returns the node type identifier, delegating to the embedded BaseStrategy.
func (s *BranchStrategy) Type() string { return s.BaseStrategy.Type() }

// PlannerCard delegates to embedded BaseStrategy.
func (s *BranchStrategy) PlannerCard() *strategy.PlannerCard { return s.BaseStrategy.PlannerCard() }

// CompilationRules delegates to embedded BaseStrategy.
func (s *BranchStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *BranchStrategy) ContextRole() *strategy.ContextRole { return s.BaseStrategy.ContextRole() }

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *BranchStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — branch uses imperative Execute.
func (s *BranchStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef { return nil }
