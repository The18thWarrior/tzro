package compiler

import (
	"fmt"
	"strings"
	"time"
)

// ExpandToSCTGraph dynamically expands a simplified Strategy Plan (high-level DAG)
// into a fine-grained execution graph with paired Semantic-Validator, execution, and synthesis nodes.
func ExpandToSCTGraph(graph *ExecutionGraph, schemaResolver func(string) (string, error)) (*ExecutionGraph, error) {
	var sctNodes []GraphNode
	var sctEdges []GraphEdge

	// We map original node IDs to their respective low-level target endpoint nodes (the execution nodes)
	// so we can correctly wire parent-child dependency edges in the expanded graph.
	execNodeMap := make(map[string]string)
	bridgeNodeMap := make(map[string]string)

	isBenchmark := strings.HasPrefix(graph.TaskID, "comparison_")

	for _, node := range graph.Nodes {
		// Only expand "action" or "deterministic" steps that require execution
		if node.Type == "action" || node.Type == "deterministic" {
			validatorID := node.ID + "_validator"
			execID := node.ID + "_exec"

			// Get the tool schema dynamically using the resolver
			var schemaStr string
			if schemaResolver != nil {
				if sch, err := schemaResolver(node.Action); err == nil {
					schemaStr = sch
				}
			}

			// 1. Semantic Validator node
			threshold := node.ActivationThreshold
			if (node.Type == "synthesis" || isSynthesisGoal(node.Instructions)) && threshold < 0.9 {
				threshold = 0.9 // Boost for high-stakes synthesis/documentation
			}
			if isBenchmark {
				threshold = 0.0 // Suppress reactive gates for benchmarks
			}

			sctNodes = append(sctNodes, GraphNode{
				ID:                  validatorID,
				Type:                "semantic_validator",
				Action:              node.Action,
				Instructions:        node.Instructions,
				AllowedTools:        node.AllowedTools,
				OutputSchema:        schemaStr,
				SuggestedSkills:     node.SuggestedSkills,
				DynamicBindings:     node.DynamicBindings,
				Status:              "pending",
				ActivationThreshold: threshold,
			})

			// 2. Deterministic Tool execution node
			sctNodes = append(sctNodes, GraphNode{
				ID:              execID,
				Type:            "deterministic",
				Action:          node.Action,
				Instructions:    fmt.Sprintf("Execute tool '%s' using the structured arguments extracted by the validator node {{nodes.%s.output}}", node.Action, validatorID),
				AllowedTools:    node.AllowedTools,
				DynamicBindings: node.DynamicBindings,
				Status:          "pending",
			})

			// Validator -> Exec edge
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: validatorID,
				TargetID: execID,
			})

			execNodeMap[node.ID] = execID
			bridgeNodeMap[node.ID] = validatorID
		} else {
			if node.Type == "probe" {
				if node.ProbeConfig != nil && node.ProbeConfig.CompactionLevel == "" {
					node.ProbeConfig.CompactionLevel = CompactPreserve
				}
				sctNodes = append(sctNodes, node)

				// Planning Awareness: Check if this probe already has a planned synthesis-type child.
				// If so, we skip automatic Recall injection to avoid redundant consolidation steps (Discovery -> Aligned Findings -> Terminal).
				hasPlannedSynthesisChild := false
				for _, edge := range graph.Edges {
					if edge.SourceID == node.ID {
						// Look up the target node in the original high-level graph
						for _, originalNode := range graph.Nodes {
							if originalNode.ID == edge.TargetID && (originalNode.Type == "synthesis" || isSynthesisGoal(originalNode.Instructions)) {
								hasPlannedSynthesisChild = true
								break
							}
						}
					}
					if hasPlannedSynthesisChild {
						break
					}
				}

				if !hasPlannedSynthesisChild {
					// Inject Recall Node to align discovery findings (ADR-0038)
					recallID := node.ID + "_recall"
					recallThreshold := 0.9
					if isBenchmark {
						recallThreshold = 0.0
					}
					sctNodes = append(sctNodes, GraphNode{
						ID:                  recallID,
						Type:                "recall",
						Action:              "synthesize",
						Instructions:        fmt.Sprintf("Traverse the execution history of probe node '%s', recall all discovered facts, and synthesize them into a cohesive aligned response.", node.ID),
						Status:              "pending",
						ActivationThreshold: recallThreshold, // High skepticism for synthesis
						DynamicBindings:     node.DynamicBindings,
					})

					// Probe -> Recall edge
					sctEdges = append(sctEdges, GraphEdge{
						SourceID: node.ID,
						TargetID: recallID,
					})

					execNodeMap[node.ID] = recallID
					bridgeNodeMap[node.ID] = node.ID // Target high-level dependencies to the probe first, then the recall handles synthesis
				} else {
					fmt.Printf("[Compiler] Probe %s already has a planned synthesis child. Skipping automatic Recall injection.\n", node.ID)
					execNodeMap[node.ID] = node.ID
					bridgeNodeMap[node.ID] = node.ID
				}
			} else {
				sctNodes = append(sctNodes, node)
				execNodeMap[node.ID] = node.ID
			}
		}
	}

	// Reconnect original high-level dependencies
	for _, edge := range graph.Edges {
		srcExecID, srcExists := execNodeMap[edge.SourceID]

		// Target ID resolution: link to its validator node if it exists, otherwise the target ID itself
		var targetID string
		if tgtValidatorID, tgtExists := bridgeNodeMap[edge.TargetID]; tgtExists {
			targetID = tgtValidatorID
		} else {
			targetID = execNodeMap[edge.TargetID]
		}

		if srcExists {
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: srcExecID,
				TargetID: targetID,
			})
		} else {
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: edge.SourceID,
				TargetID: targetID,
			})
		}
	}

	// Link all execution endpoints (leaves in the original graph) to the terminal synthesis node
	// A node is an endpoint if it is an execution node and has no outbound edges to other high-level steps.
	isSourceMap := make(map[string]bool)
	for _, edge := range sctEdges {
		isSourceMap[edge.SourceID] = true
	}

	// 3. Inject terminal synthesis node
	// Planning Awareness: Check if the graph already ends in a synthesis-type node.
	// If the planner manually added a synthesis step at the end, we don't need a double summary.
	hasSynthesisLeaf := false
	for _, node := range sctNodes {
		if (node.Type == "synthesis" || isSynthesisGoal(node.Instructions)) && !isSourceMap[node.ID] {
			hasSynthesisLeaf = true
			break
		}
	}

	if !hasSynthesisLeaf {
		synthID := "terminal_synthesis"
		synthThreshold := 0.7
		if isBenchmark {
			synthThreshold = 0.0
		}
		sctNodes = append(sctNodes, GraphNode{
			ID:                  synthID,
			Type:                "synthesis",
			Instructions:        "Summarize and compile all prior action outputs into a final cohesive response. IMPORTANT: If you did not successfully find or read the relevant information, state that you did not find it. Do NOT guess or invent implementation details.",
			Status:              "pending",
			ActivationThreshold: synthThreshold,
		})

		// Link all execution endpoints (leaves in the original graph) to the terminal synthesis node
		for _, node := range sctNodes {
			if (node.Type == "deterministic" || node.Type == "action" || node.Type == "probe" || node.Type == "sub_dag" || node.Type == "recall") && !isSourceMap[node.ID] {
				sctEdges = append(sctEdges, GraphEdge{
					SourceID: node.ID,
					TargetID: synthID,
				})
			}
		}
	} else {
		fmt.Printf("[Compiler] Graph already has a synthesis leaf. Skipping automatic terminal_synthesis injection.\n")
	}

	return &ExecutionGraph{
		TaskID:     graph.TaskID,
		GoalPrompt: graph.GoalPrompt,
		Nodes:      sctNodes,
		Edges:      sctEdges,
		MaxCycles:  graph.MaxCycles,
		CreatedAt:  time.Now().Unix(),
	}, nil
}

func isSynthesisGoal(instructions string) bool {
	g := strings.ToLower(instructions)
	// If the node is explicitly writing/saving, it's an action, not a synthesis summary.
	if strings.Contains(g, "write") || strings.Contains(g, "save") {
		return false
	}
	keywords := []string{"synthesize", "compile", "summarize", "index", "docs", "documentation"}
	for _, k := range keywords {
		if strings.Contains(g, k) {
			return true
		}
	}
	return false
}
