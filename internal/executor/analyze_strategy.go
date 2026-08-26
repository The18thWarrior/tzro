package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/strategy"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// AnalyzeOnlyStrategy — strategy-owned Execute for analyze nodes (ADR-0069, ADR-0091)
// ---------------------------------------------------------------------------

// AnalyzeOnlyStrategy runs autonomous Thought Chain exploration for "analyze"
// (structured/tabular data) nodes. Previously shared with probe nodes as
// ProbeAnalyzeStrategy; probe was deleted in ADR-0091.
type AnalyzeOnlyStrategy struct {
	strategy.BaseStrategy
	runCore      func(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode, executionTier string, meta inference.StreamMeta, interpolatedPrompt string) (string, error)
	publishState func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
}

// NewAnalyzeOnlyStrategy creates an AnalyzeOnlyStrategy.
func NewAnalyzeOnlyStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *AnalyzeOnlyStrategy {
	return &AnalyzeOnlyStrategy{
		BaseStrategy: *base,
		runCore:      engine.runProbeAnalyzeCore,
		publishState: publishNodeState,
	}
}

// Execute configures the analyze node, runs the Thought Chain, and returns the synthesis.
// The dispatch envelope handles state persistence, AfterNode hooks, and events.
func (s *AnalyzeOnlyStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, "Analyze: "+node.Instructions)
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	// Run the domain-core analyze logic (config, expansion, Thought Chain, cacheId preservation)
	synthesis, err := s.runCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("analyze node %s execution failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[AnalyzeOnlyStrategy] analyze %s completed (%d chars)\n", node.ID, len(synthesis))

	// Return output — envelope handles hooks + state
	return &strategy.ExecutionResult{
		Output:    fmt.Sprintf("[Analyze] %s", synthesis),
		Directive: strategy.DirectiveContinue,
	}, nil
}

// capitalize returns the string with the first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Type returns the node type identifier.
func (s *AnalyzeOnlyStrategy) Type() string { return "analyze" }

// Compile-time interface check.
var _ strategy.NodeStrategy = (*AnalyzeOnlyStrategy)(nil)
