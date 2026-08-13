package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/memory"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// ScatterAssemblyStrategy — strategy-owned Execute for scatter_assembly (ADR-0069)
// ---------------------------------------------------------------------------

// ScatterAssemblyStrategy collects outputs from scatter probe nodes spawned by
// a recall node's VTE verification (ADR-0071), assembles them with the original
// recall synthesis, runs a smoothing inference pass, and re-verifies.
type ScatterAssemblyStrategy struct {
	strategy.BaseStrategy
	stashVerification func(taskID string, result *VerificationResult)
}

// NewScatterAssemblyStrategy creates a ScatterAssemblyStrategy.
func NewScatterAssemblyStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *ScatterAssemblyStrategy {
	return &ScatterAssemblyStrategy{
		BaseStrategy:      *base,
		stashVerification: engine.stashVerificationResult,
	}
}

// Execute collects scatter outputs, assembles, smooths, and re-verifies.
// The dispatch envelope handles state persistence and events.
func (s *ScatterAssemblyStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state (specific to scatter_assembly semantics)
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, "Scatter Assembly: "+node.Instructions)

	recallNodeID := node.Instructions // Instructions stores the recall node ID

	// 1. Read recall node's original synthesis
	recallSynthesis := ""
	if ups := nr.Upstream(); ups != nil {
		if output, err := ups.GetUpstreamOutput(recallNodeID); err == nil {
			recallSynthesis = output
		}
	}

	// 2. Find all upstream scatter probe outputs
	scatterOutputs := make(map[string]string)
	for _, edge := range graph.Edges {
		if edge.TargetID == node.ID && strings.HasPrefix(edge.SourceID, "scatter_probe_") {
			state, ok := memory.DB.GetNodeState(taskID, edge.SourceID)
			if ok && state.Status == "completed" {
				// Extract the goal item from the probe node's config
				goalItem := ""
				for _, n := range graph.Nodes {
					if n.ID == edge.SourceID && n.ProbeConfig != nil {
						goalItem = n.ProbeConfig.Goal
						break
					}
				}
				rawOutput := state.RawOutput
				if rawOutput == "" {
					rawOutput = state.Output
				}
				if goalItem != "" && strings.TrimSpace(rawOutput) != "" {
					scatterOutputs[goalItem] = rawOutput
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[ScatterAssemblyStrategy] Assembling %d scatter outputs with recall synthesis (%d chars)\n",
		len(scatterOutputs), len(recallSynthesis))

	// 3. Deterministic assembly
	assembled := assembleScatterOutput(recallSynthesis, scatterOutputs)

	// 4. Smoothing pass (single inference)
	smoothingEngine := &ProbeInference{}
	smoothed, err := smoothAssembly(ctx, assembled, smoothingEngine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ScatterAssemblyStrategy] Smoothing error (non-fatal): %v\n", err)
		smoothed = assembled
	}

	synthesis := smoothed

	// 5. Re-run VTE with scatterAttempted=true (prevents re-scatter)
	finalSynthesis, vResult, vErr := VerifyTaskOutput(
		ctx,
		&DefaultCloudVerifier{},
		graph.GoalPrompt,
		smoothed,
		recallSynthesis,
		true, // scatterAttempted: second pass
	)
	if vErr == nil {
		synthesis = finalSynthesis
		if vResult != nil {
			s.stashVerification(taskID, vResult)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[ScatterAssemblyStrategy] VTE error (non-fatal): %v\n", vErr)
	}

	// Clean up scatter context temp file if it exists
	for _, edge := range graph.Edges {
		if edge.TargetID == node.ID && strings.HasPrefix(edge.SourceID, "scatter_probe_") {
			for _, n := range graph.Nodes {
				if n.ID == edge.SourceID && n.ProbeConfig != nil && n.ProbeConfig.ContextFile != "" {
					if strings.HasPrefix(n.ProbeConfig.ContextFile, os.TempDir()) {
						_ = os.Remove(n.ProbeConfig.ContextFile)
						fmt.Fprintf(os.Stderr, "[ScatterAssemblyStrategy] Cleaned up temp context file: %s\n", n.ProbeConfig.ContextFile)
					}
					break // All scatter probes share the same context file
				}
			}
			break
		}
	}

	// Return output — envelope handles state persistence and events
	return &strategy.ExecutionResult{
		Output:    fmt.Sprintf("[ScatterAssembly] %s", synthesis),
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*ScatterAssemblyStrategy)(nil)
