package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// DeterministicStrategy — strategy-owned Execute (ADR-0069)
// ---------------------------------------------------------------------------

// DeterministicStrategy executes a single known tool call with predetermined
// parameters. It resolves dynamic bindings, extracts tool arguments via
// GBNF-constrained inference, executes the tool, and applies cache compaction.
type DeterministicStrategy struct {
	strategy.BaseStrategy
	engine *ExecutionEngine
}

// NewDeterministicStrategy creates a DeterministicStrategy.
func NewDeterministicStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *DeterministicStrategy {
	return &DeterministicStrategy{
		BaseStrategy: *base,
		engine:       engine,
	}
}

// Execute runs the tool dispatch pipeline and returns the compacted output.
// The dispatch envelope handles state persistence, AfterNode hooks, and events.
func (s *DeterministicStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID,
		fmt.Sprintf("Executing %s", node.Action))
	s.engine.publishNodeStateStream(taskID, node.ID, "running", "")

	// Run the domain-core tool dispatch logic
	result, err := s.engine.runDeterministicCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("deterministic %s failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[DeterministicStrategy] %s completed (%d chars)\n", node.ID, len(result.compactedOutput))

	executionTier := "Local"
	if nr.ExecutionTier() != "" {
		executionTier = nr.ExecutionTier()
	}
	output := fmt.Sprintf("[%s] %s", executionTier, result.compactedOutput)

	return &strategy.ExecutionResult{
		Output:    output,
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Type returns the node type identifier.
func (s *DeterministicStrategy) Type() string { return s.BaseStrategy.Type() }

// PlannerCard delegates to embedded BaseStrategy.
func (s *DeterministicStrategy) PlannerCard() *strategy.PlannerCard {
	return s.BaseStrategy.PlannerCard()
}

// CompilationRules delegates to embedded BaseStrategy.
func (s *DeterministicStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *DeterministicStrategy) ContextRole() *strategy.ContextRole {
	return s.BaseStrategy.ContextRole()
}

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *DeterministicStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — deterministic uses imperative Execute.
func (s *DeterministicStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef {
	return nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*DeterministicStrategy)(nil)
