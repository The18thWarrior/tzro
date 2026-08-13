package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// RecallStrategy — strategy-owned Execute for recall nodes (ADR-0069)
// ---------------------------------------------------------------------------

// RecallStrategy aligns upstream probe findings with the original task
// requirements via the recall engine. Handles VTE verification (ADR-0067)
// and scatter probe spawning (ADR-0071) when gaps are detected.
type RecallStrategy struct {
	strategy.BaseStrategy
	engine *ExecutionEngine
}

// NewRecallStrategy creates a RecallStrategy.
func NewRecallStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *RecallStrategy {
	return &RecallStrategy{
		BaseStrategy: *base,
		engine:       engine,
	}
}

// Execute identifies upstream probes, runs recall synthesis, VTE verification,
// and optionally spawns scatter probes. Returns the synthesis output.
//
// Special case: when scatter probes are spawned, the strategy handles state
// directly (DelegateHandled=true) because the recall node completes immediately
// while the scatter_assembly node handles the final output.
func (s *RecallStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, "Recall: "+node.Instructions)

	// Identify all upstream probe nodes recursively (ADR-0041)
	var upstreamNodeIDs []string
	visited := make(map[string]bool)
	var findProbes func(string)
	findProbes = func(currentID string) {
		if visited[currentID] {
			return
		}
		visited[currentID] = true
		for _, edge := range graph.Edges {
			if edge.TargetID == currentID {
				parentID := edge.SourceID
				for _, n := range graph.Nodes {
					if n.ID == parentID && (n.Type == "probe" || n.Type == "analyze") {
						upstreamNodeIDs = append(upstreamNodeIDs, parentID)
					}
				}
				findProbes(parentID)
			}
		}
	}
	findProbes(node.ID)

	recallEngine := &ProbeInference{}
	recallResult, err := s.engine.RunRecall(ctx, taskID, node.ID, upstreamNodeIDs, node.Instructions, recallEngine)
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("recall node %s execution failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	// ADR-0067: Verified Task Execution — evaluate and optionally re-synthesize
	synthesis := recallResult.Synthesis
	var verificationResult *VerificationResult
	finalSynthesis, vResult, vErr := VerifyTaskOutput(
		ctx,
		&DefaultCloudVerifier{},
		graph.GoalPrompt,
		recallResult.Synthesis,
		recallResult.RefinedContext,
		false, // first pass, scatter not yet attempted
	)
	if vErr == nil {
		synthesis = finalSynthesis
		verificationResult = vResult
	} else {
		fmt.Fprintf(os.Stderr, "[RecallStrategy] VTE error (non-fatal): %v\n", vErr)
	}

	// ADR-0071: Item-Level Scatter — if VTE detects missing items,
	// spawn targeted scatter probes to fill the gaps.
	if verificationResult != nil && len(verificationResult.ScatterItems) > 0 && graph.MutationBudget != nil {
		tmpFile, tmpErr := os.CreateTemp("", "scatter_ctx_*.txt")
		if tmpErr == nil {
			_, _ = tmpFile.WriteString(recallResult.RefinedContext)
			_ = tmpFile.Close()
			ctxPath := tmpFile.Name()

			for i := range verificationResult.ScatterItems {
				verificationResult.ScatterItems[i].ContextFilePath = ctxPath
			}

			assemblyID, scatterIDs, spawnErr := SpawnScatterProbes(
				graph, node.ID, verificationResult.ScatterItems, graph.MutationBudget,
			)
			if spawnErr == nil && assemblyID != "" {
				fmt.Fprintf(os.Stderr, "[RecallStrategy] Scatter spawned: %d probes + assembly %s\n",
					len(scatterIDs), assemblyID)

				// Early completion: recall finishes with original synthesis,
				// the scatter_assembly node will produce the final output.
				// We handle state directly since the envelope shouldn't
				// overwrite our "completed" with another "completed".
				nodeStatus := fmt.Sprintf("[Recall] %s", synthesis)
				_ = memory.DB.SetNodeState(taskID, node.ID, "completed", nodeStatus)
				_ = memory.DB.SetNodeRawOutput(taskID, node.ID, synthesis)
				nr.Publisher().PublishEvent("node_completed", taskID, node.ID, nodeStatus)
				s.engine.publishNodeStateStream(taskID, node.ID, "completed", nodeStatus)

				return &strategy.ExecutionResult{
					Output:          nodeStatus,
					Directive:       strategy.DirectiveContinue,
					DelegateHandled: true, // Signal envelope to skip ceremony
				}, nil
			}
			if spawnErr != nil {
				fmt.Fprintf(os.Stderr, "[RecallStrategy] Scatter spawn failed: %v\n", spawnErr)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[RecallStrategy] Failed to create scatter context temp file: %v\n", tmpErr)
		}
	}

	// Stash verification result for envelope assembly
	if verificationResult != nil {
		s.engine.stashVerificationResult(taskID, verificationResult)
	}

	// Normal completion — envelope handles hooks + state
	return &strategy.ExecutionResult{
		Output:    fmt.Sprintf("[Recall] %s", synthesis),
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Type returns the node type identifier.
func (s *RecallStrategy) Type() string { return s.BaseStrategy.Type() }

// PlannerCard delegates to embedded BaseStrategy.
func (s *RecallStrategy) PlannerCard() *strategy.PlannerCard { return s.BaseStrategy.PlannerCard() }

// CompilationRules delegates to embedded BaseStrategy.
func (s *RecallStrategy) CompilationRules() *strategy.CompilationRules {
	return s.BaseStrategy.CompilationRules()
}

// ContextRole delegates to embedded BaseStrategy.
func (s *RecallStrategy) ContextRole() *strategy.ContextRole { return s.BaseStrategy.ContextRole() }

// EdgeThoughtPolicy delegates to embedded BaseStrategy.
func (s *RecallStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig {
	return s.BaseStrategy.EdgeThoughtPolicy()
}

// StagePlan returns nil — recall uses imperative Execute.
func (s *RecallStrategy) StagePlan(node *compiler.GraphNode) *strategy.StagePlanDef { return nil }

// Compile-time interface check.
var _ strategy.NodeStrategy = (*RecallStrategy)(nil)
