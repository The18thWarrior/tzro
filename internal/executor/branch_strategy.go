package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// BranchStrategy — strategy-owned Execute for branch nodes (ADR-0069)
// ---------------------------------------------------------------------------

// BranchStrategy evaluates branch conditions and returns DirectiveContinue
// (satisfied) or DirectiveSkipDownstream (not satisfied).
//
// State management is done through NodeRuntime capabilities (StatePersister,
// EventPublisher), not through direct memory.DB or engine.getPublisher() calls.
type BranchStrategy struct {
	strategy.BaseStrategy
	evaluateCondition func(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode) (bool, error)
}

// NewBranchStrategy creates a BranchStrategy with the injected condition evaluator.
func NewBranchStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *BranchStrategy {
	return &BranchStrategy{
		BaseStrategy:      *base,
		evaluateCondition: engine.evaluateBranchCondition,
	}
}

// Execute evaluates the branch condition and returns the appropriate directive.
// The dispatch envelope handles all ceremony: state persistence, events,
// hooks, and downstream propagation.
func (s *BranchStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()

	satisfied, err := s.evaluateCondition(ctx, graph, node)
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
