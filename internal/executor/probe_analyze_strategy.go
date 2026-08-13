package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// ProbeAnalyzeStrategy — strategy-owned Execute for probe/analyze (ADR-0069)
// ---------------------------------------------------------------------------

// ProbeAnalyzeStrategy runs autonomous Thought Chain exploration for both
// "probe" (filesystem/codebase) and "analyze" (structured/tabular data) nodes.
// The Execute method calls the engine's domain-core function and returns the
// synthesis output. The dispatch envelope handles state, hooks, and events.
type ProbeAnalyzeStrategy struct {
	strategy.BaseStrategy
	engine   *ExecutionEngine
	nodeType string // "probe" or "analyze"
}

// NewProbeAnalyzeStrategy creates a ProbeAnalyzeStrategy for the given node type.
func NewProbeAnalyzeStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy, nodeType string) *ProbeAnalyzeStrategy {
	return &ProbeAnalyzeStrategy{
		BaseStrategy: *base,
		engine:       engine,
		nodeType:     nodeType,
	}
}

// Execute configures the probe, runs the Thought Chain, and returns the synthesis.
// The dispatch envelope handles state persistence, AfterNode hooks, and events.
func (s *ProbeAnalyzeStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, capitalize(s.nodeType)+": "+node.Instructions)
	s.engine.publishNodeStateStream(taskID, node.ID, "running", "")

	// Run the domain-core probe logic (config, expansion, Thought Chain, cacheId preservation)
	synthesis, err := s.engine.runProbeAnalyzeCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("probe node %s execution failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[ProbeAnalyzeStrategy] %s %s completed (%d chars)\n", s.nodeType, node.ID, len(synthesis))

	// Return output — envelope handles hooks + state
	return &strategy.ExecutionResult{
		Output:    fmt.Sprintf("[Probe] %s", synthesis),
		Directive: strategy.DirectiveContinue,
	}, nil
}

// capitalize returns the string with the first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

// Type returns the node type identifier.
func (s *ProbeAnalyzeStrategy) Type() string { return s.nodeType }

// PlannerCard delegates to embedded BaseStrategy.
func (s *ProbeAnalyzeStrategy) PlannerCard() *strategy.PlannerCard {
	return s.BaseStrategy.PlannerCard()
}

// CompilationRules delegates to embedded BaseStrategy.
func (s *ProbeAnalyzeStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *ProbeAnalyzeStrategy) ContextRole() *strategy.ContextRole {
	return s.BaseStrategy.ContextRole()
}

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *ProbeAnalyzeStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — probe uses imperative Execute.
func (s *ProbeAnalyzeStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef {
	return nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*ProbeAnalyzeStrategy)(nil)
