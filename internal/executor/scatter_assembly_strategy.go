package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
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
	engine *ExecutionEngine
}

// NewScatterAssemblyStrategy creates a ScatterAssemblyStrategy.
func NewScatterAssemblyStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *ScatterAssemblyStrategy {
	return &ScatterAssemblyStrategy{
		BaseStrategy: *base,
		engine:       engine,
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
			if state, ok := s.engine.getNodeStateForScatter(taskID, edge.SourceID); ok && state.status == "completed" {
				// Extract the goal item from the probe node's config
				goalItem := ""
				for _, n := range graph.Nodes {
					if n.ID == edge.SourceID && n.ProbeConfig != nil {
						goalItem = n.ProbeConfig.Goal
						break
					}
				}
				rawOutput := state.rawOutput
				if rawOutput == "" {
					rawOutput = state.output
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
			s.engine.stashVerificationResult(taskID, vResult)
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

// scatterNodeState holds the minimal state needed for scatter assembly.
type scatterNodeState struct {
	status    string
	output    string
	rawOutput string
}

// getNodeStateForScatter retrieves node state for scatter assembly.
// This bridges memory.DB access without exposing the full memory package.
func (e *ExecutionEngine) getNodeStateForScatter(taskID, nodeID string) (scatterNodeState, bool) {
	state, ok := memory.DB.GetNodeState(taskID, nodeID)
	if !ok {
		return scatterNodeState{}, false
	}
	return scatterNodeState{
		status:    state.Status,
		output:    state.Output,
		rawOutput: state.RawOutput,
	}, true
}

// Type returns the node type identifier.
func (s *ScatterAssemblyStrategy) Type() string { return s.BaseStrategy.Type() }

// PlannerCard delegates to embedded BaseStrategy.
func (s *ScatterAssemblyStrategy) PlannerCard() *strategy.PlannerCard {
	return s.BaseStrategy.PlannerCard()
}

// CompilationRules delegates to embedded BaseStrategy.
func (s *ScatterAssemblyStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *ScatterAssemblyStrategy) ContextRole() *strategy.ContextRole {
	return s.BaseStrategy.ContextRole()
}

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *ScatterAssemblyStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — scatter_assembly uses imperative Execute.
func (s *ScatterAssemblyStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef {
	return nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*ScatterAssemblyStrategy)(nil)
