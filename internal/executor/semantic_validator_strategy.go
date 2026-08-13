package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/strategy"
	"tzro/internal/stream"
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
	runCore      func(ctx context.Context, graph *compiler.ExecutionGraph, node *compiler.GraphNode, executionTier string, meta inference.StreamMeta, interpolatedPrompt string) (string, error)
	publishState func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
}

// NewSemanticValidatorStrategy creates a SemanticValidatorStrategy.
func NewSemanticValidatorStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *SemanticValidatorStrategy {
	return &SemanticValidatorStrategy{
		BaseStrategy: *base,
		runCore:      engine.runSemanticValidatorCore,
		publishState: publishNodeState,
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
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	// Run the domain-core parameter extraction logic
	inferenceResult, err := s.runCore(ctx, graph, node, nr.ExecutionTier(), nr.Meta(), nr.InterpolatedPrompt())
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

// Compile-time interface check.
var _ strategy.NodeStrategy = (*SemanticValidatorStrategy)(nil)
