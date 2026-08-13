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
// ProbeAnalyzeStrategy — strategy-owned Execute for probe/analyze (ADR-0069)
// ---------------------------------------------------------------------------

// ProbeAnalyzeStrategy runs autonomous Thought Chain exploration for both
// "probe" (filesystem/codebase) and "analyze" (structured/tabular data) nodes.
// The Execute method calls the injected core function and returns the
// synthesis output. The dispatch envelope handles state, hooks, and events.
type ProbeAnalyzeStrategy struct {
	strategy.BaseStrategy
	runCore      func(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode, executionTier string, meta inference.StreamMeta, interpolatedPrompt string) (string, error)
	publishState func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
	nodeType     string // "probe" or "analyze"
}

// NewProbeAnalyzeStrategy creates a ProbeAnalyzeStrategy for the given node type.
func NewProbeAnalyzeStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy, nodeType string) *ProbeAnalyzeStrategy {
	return &ProbeAnalyzeStrategy{
		BaseStrategy: *base,
		runCore:      engine.runProbeAnalyzeCore,
		publishState: publishNodeState,
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
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	// Run the domain-core probe logic (config, expansion, Thought Chain, cacheId preservation)
	synthesis, err := s.runCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
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
	return strings.ToUpper(s[:1]) + s[1:]
}

// Type returns the node type identifier (overrides BaseStrategy for polymorphism).
func (s *ProbeAnalyzeStrategy) Type() string { return s.nodeType }

// Compile-time interface check.
var _ strategy.NodeStrategy = (*ProbeAnalyzeStrategy)(nil)
