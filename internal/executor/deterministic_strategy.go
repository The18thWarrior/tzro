package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/strategy"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// DeterministicStrategy — strategy-owned Execute (ADR-0069)
// ---------------------------------------------------------------------------

// DeterministicStrategy executes a single known tool call with predetermined
// parameters. It resolves dynamic bindings, extracts tool arguments via
// GBNF-constrained inference, executes the tool, and applies cache compaction.
type DeterministicStrategy struct {
	strategy.BaseStrategy
	runCore        func(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode, executionTier string, meta inference.StreamMeta, interpolatedPrompt string) (*deterministicResult, error)
	publishState   func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
}

// NewDeterministicStrategy creates a DeterministicStrategy with injected dependencies.
func NewDeterministicStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *DeterministicStrategy {
	return &DeterministicStrategy{
		BaseStrategy: *base,
		runCore:      engine.runDeterministicCore,
		publishState: publishNodeState,
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
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	// Run the domain-core tool dispatch logic
	result, err := s.runCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("deterministic %s failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[DeterministicStrategy] %s completed (%d chars)\n", node.ID, len(result.compactedOutput))

	// Persist raw output as StructuredOutput so comparison framework's Fix 7
	// can extract write_file content even when the file escapes the sandbox.
	if result.rawOutput != "" && len(result.rawOutput) > 100 {
		_ = memory.DB.SetNodeStructuredOutput(taskID, node.ID, result.rawOutput)
	}

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

// Compile-time interface check.
var _ strategy.NodeStrategy = (*DeterministicStrategy)(nil)
