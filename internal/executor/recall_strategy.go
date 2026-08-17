package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/strategy"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// RecallStrategy — strategy-owned Execute for recall nodes (ADR-0069)
// ---------------------------------------------------------------------------

// RecallStrategy aligns upstream probe findings with the original task
// requirements via the recall engine. Handles VTE verification (ADR-0067)
// and scatter probe spawning (ADR-0071) when gaps are detected.
type RecallStrategy struct {
	strategy.BaseStrategy
	runRecall          func(ctx context.Context, taskID, recallNodeID string, upstreamNodeIDs []string, goal string, engine ProbeInferenceEngine) (RecallResult, error)
	publishState       func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
	stashVerification  func(taskID string, result *VerificationResult)
}

// NewRecallStrategy creates a RecallStrategy with injected dependencies.
func NewRecallStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *RecallStrategy {
	return &RecallStrategy{
		BaseStrategy:      *base,
		runRecall:         engine.RunRecall,
		publishState:      publishNodeState,
		stashVerification: engine.stashVerificationResult,
	}
}

// Execute identifies upstream probes, runs recall synthesis, VTE verification,
// and optionally spawns scatter probes. Returns the synthesis output.
//
// Special case: when scatter probes are spawned, the strategy handles state
// directly (SelfManaged=true) because the recall node completes immediately
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
	recallResult, err := s.runRecall(ctx, taskID, node.ID, upstreamNodeIDs, node.Instructions, recallEngine)
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("recall node %s execution failed: %v", node.ID, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	// ADR-0067 / ADR-0078 / ADR-0079: Verified Task Execution — evaluate with mode awareness
	synthesis := recallResult.Synthesis
	var verificationResult *VerificationResult

	isTerminal := isTerminalRecall(graph, node.ID)
	feedsSink := recallFeedsToolSink(graph, node.ID)

	vMode := ModeMilestone
	vGoal := node.Instructions
	if isTerminal {
		vMode = ModeTerminal
		vGoal = graph.GoalPrompt
	}

	finalSynthesis, vResult, vErr := VerifyTaskOutputWithOptions(
		ctx,
		&DefaultCloudVerifier{},
		vGoal,
		recallResult.Synthesis,
		recallResult.RefinedContext,
		VerificationOpts{
			Mode:          vMode,
			FeedsToolSink: feedsSink,
		},
	)
	if vErr == nil {
		synthesis = finalSynthesis
		verificationResult = vResult
	} else {
		fmt.Fprintf(os.Stderr, "[RecallStrategy] VTE error (non-fatal): %v\n", vErr)
	}

	// ADR-0078: In-place Re-Explore recovery if signaled
	if verificationResult != nil && verificationResult.ReExplore && len(upstreamNodeIDs) > 0 {
		fmt.Fprintf(os.Stderr, "[RecallStrategy] In-place re-exploration triggered for upstream nodes %v with hint: %s\n",
			upstreamNodeIDs, verificationResult.ReExploreHint)

		for _, upstreamID := range upstreamNodeIDs {
			for _, upstreamNode := range graph.Nodes {
				if upstreamNode.ID == upstreamID && (upstreamNode.Type == "probe" || upstreamNode.Type == "research") {
					stepBudget := 10
					if upstreamNode.ProbeConfig != nil && upstreamNode.ProbeConfig.StepBudget > 0 {
						stepBudget = upstreamNode.ProbeConfig.StepBudget
					}
					reProbeConfig := compiler.ProbeConfig{
						Goal:         upstreamNode.Instructions + "\nHint: " + verificationResult.ReExploreHint,
						TaskContext:  graph.GoalPrompt,
						AllowedTools: upstreamNode.AllowedTools,
						StepBudget:   stepBudget,
					}
					reSynth, reErr := RunProbePhases(ctx, taskID, upstreamID, reProbeConfig, recallEngine, recallEngine, nil)
					if reErr == nil && reSynth != "" {
						fmt.Fprintf(os.Stderr, "[RecallStrategy] Upstream probe %s re-exploration succeeded (%d chars)\n",
							upstreamID, len(reSynth))
						freshRecall, freshErr := s.runRecall(ctx, taskID, node.ID, upstreamNodeIDs, node.Instructions, recallEngine)
						if freshErr == nil && freshRecall.Synthesis != "" {
							if len(freshRecall.Synthesis) >= 200 && !strings.Contains(freshRecall.Synthesis, "[GENERATION_ABORTED]") {
								synthesis = freshRecall.Synthesis
								verificationResult.Accepted = true
								break
							}
						}
					} else {
						fmt.Fprintf(os.Stderr, "[RecallStrategy] Upstream probe %s re-exploration failed: %v — falling back to safety re-synthesis\n",
							upstreamID, reErr)
					}
				}
			}
		}

		// Ensure we never return rejected broken local synthesis
		if !verificationResult.Accepted && verificationResult.ReSynthesis != "" {
			synthesis = verificationResult.ReSynthesis
		}
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
				s.publishState(nr.Publisher(), taskID, node.ID, "completed", nodeStatus)

				return &strategy.ExecutionResult{
					Output:      nodeStatus,
					Directive:   strategy.DirectiveContinue,
					SelfManaged: true, // Signal envelope to skip ceremony
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
		s.stashVerification(taskID, verificationResult)
	}

	// Normal completion — envelope handles hooks + state
	return &strategy.ExecutionResult{
		Output:    fmt.Sprintf("[Recall] %s", synthesis),
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*RecallStrategy)(nil)

func isToolSinkAction(action string) bool {
	switch action {
	case "write_file", "save_memory", "postgres_insert", "db_insert", "db_query_exec":
		return true
	default:
		return false
	}
}

func recallFeedsToolSink(graph *compiler.ExecutionGraph, recallNodeID string) bool {
	if graph == nil {
		return false
	}
	visited := make(map[string]bool)
	var queue []string

	for _, edge := range graph.Edges {
		if edge.SourceID == recallNodeID {
			queue = append(queue, edge.TargetID)
		}
	}

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]
		if visited[currID] {
			continue
		}
		visited[currID] = true

		for _, n := range graph.Nodes {
			if n.ID == currID {
				if isToolSinkAction(n.Action) {
					return true
				}
				// Also check if node is an exec node for a tool sink
				if strings.HasSuffix(n.ID, "_exec") && isToolSinkAction(n.Action) {
					return true
				}
				// Add downstream targets
				for _, edge := range graph.Edges {
					if edge.SourceID == currID {
						queue = append(queue, edge.TargetID)
					}
				}
			}
		}
	}
	return false
}

func isTerminalRecall(graph *compiler.ExecutionGraph, recallNodeID string) bool {
	if graph == nil {
		return true
	}
	for _, edge := range graph.Edges {
		if edge.SourceID == recallNodeID {
			return false
		}
	}
	return true
}


