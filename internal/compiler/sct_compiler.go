package compiler

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// tabularExtRe detects tabular file extension references in node instructions.
var tabularExtRe = regexp.MustCompile(`\.(csv|tsv|xlsx|xls)\b`)

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

	discoveryNodesCount := 0
	for _, n := range graph.Nodes {
		if n.Type == "probe" || (n.Type == "action" && n.Action != "write_file") {
			discoveryNodesCount++
		}
	}

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
			if node.Type == "probe" || node.Type == "analyze" {
				// Analyze nodes: auto-provision cache tools and ProbeConfig if not already set.
				// The planner emits analyze nodes without knowing about cache internals —
				// the compiler deterministically injects the right tool set.
				if node.Type == "analyze" {
					if node.ProbeConfig == nil {
						node.ProbeConfig = &ProbeConfig{
							Goal:            node.Instructions,
							AllowedTools:    cacheTools,
							StepBudget:      15,
							CompactEvery:    3,
							CompactionLevel: CompactPreserve,
						}
					}
					// Ensure cache tools are always present in AllowedTools
					if !hasCacheToolsInAllowed(node.AllowedTools) {
						node.AllowedTools = append(node.AllowedTools, cacheTools...)
					}
					if !hasCacheToolsInAllowed(node.ProbeConfig.AllowedTools) {
						node.ProbeConfig.AllowedTools = append(node.ProbeConfig.AllowedTools, cacheTools...)
					}
				}

				if node.ProbeConfig != nil && node.ProbeConfig.CompactionLevel == "" {
					node.ProbeConfig.CompactionLevel = CompactPreserve
				}

				// ADR-0060 Breadth Detection: auto-detect breadth tasks and inject
				// directory manifest + scale step budget. This allows depth-optimized
				// probes to efficiently navigate broad directory structures by providing
				// a top-level map before exploration begins.
				if node.ProbeConfig != nil && len(node.ProbeConfig.PreloadPaths) > 0 {
					isBreadth, subdirCount, manifest := DetectBreadthMode(node.ProbeConfig.PreloadPaths)
					if isBreadth {
						// Inject manifest into TaskContext
						manifestBlock := BuildBreadthManifest(node.ProbeConfig.PreloadPaths[0], manifest)
						if node.ProbeConfig.TaskContext != "" {
							node.ProbeConfig.TaskContext = manifestBlock + "\n\n" + node.ProbeConfig.TaskContext
						} else {
							node.ProbeConfig.TaskContext = manifestBlock
						}

						// Scale step budget
						defaultMax := 60
						if node.ProbeConfig.StepBudget > 0 {
							node.ProbeConfig.StepBudget = ScaleStepBudget(node.ProbeConfig.StepBudget, subdirCount, defaultMax)
						} else {
							node.ProbeConfig.StepBudget = ScaleStepBudget(24, subdirCount, defaultMax) // Default base
						}

						fmt.Fprintf(os.Stderr, "[KahnCompiler] Breadth mode detected for %s: %d subdirs, budget=%d\n",
							node.ID, subdirCount, node.ProbeConfig.StepBudget)
					}
				}

				sctNodes = append(sctNodes, node)

				// Planning Awareness: Check if this probe/analyze already has a planned synthesis-type child.
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

				if !hasPlannedSynthesisChild && (discoveryNodesCount > 1 || node.Type == "analyze") {
					// Inject Recall Node to align discovery findings (ADR-0038)
					// ADR-0053: Analyze nodes ALWAYS get a Recall Node, even as sole
					// discovery nodes. Their internal probe synthesis is insufficient
					// for data analysis results — the Recall Node's Map-Reduce
					// strategy and downstream terminal_synthesis are required.
					recallID := node.ID + "_recall"
					recallThreshold := 0.9
					if isBenchmark {
						recallThreshold = 0.0
					}
					sctNodes = append(sctNodes, GraphNode{
						ID:                  recallID,
						Type:                "recall",
						Action:              "synthesize",
						Instructions:        fmt.Sprintf("Traverse the execution history of %s node '%s', recall all discovered facts, and synthesize them into a cohesive aligned response.", node.Type, node.ID),
						Status:              "pending",
						ActivationThreshold: recallThreshold, // High skepticism for synthesis
						DynamicBindings:     node.DynamicBindings,
					})

					// Probe/Analyze -> Recall edge
					sctEdges = append(sctEdges, GraphEdge{
						SourceID: node.ID,
						TargetID: recallID,
					})

					execNodeMap[node.ID] = recallID
					bridgeNodeMap[node.ID] = node.ID // Target high-level dependencies to the probe/analyze first, then the recall handles synthesis
				} else {
					if discoveryNodesCount <= 1 {
						fmt.Printf("[Compiler] Probe %s is the sole discovery node in the graph (discoveryNodesCount=%d). Skipping automatic Recall injection.\n", node.ID, discoveryNodesCount)
					} else {
						fmt.Printf("[Compiler] Probe %s already has a planned synthesis child. Skipping automatic Recall injection.\n", node.ID)
					}
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

	// ── Cache Bridge Node injection ──
	// For each node whose instructions reference a tabular file extension,
	// inject a deterministic cache bridge node between it and its downstream targets.
	sctNodes, sctEdges = injectCacheBridgeNodes(graph.Nodes, sctNodes, sctEdges, execNodeMap)

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
	if !hasSynthesisLeaf && discoveryNodesCount <= 1 {
		hasProbeLeaf := false
		hasAnalyzeLeaf := false
		for _, node := range sctNodes {
			if !isSourceMap[node.ID] {
				if node.Type == "probe" {
					hasProbeLeaf = true
				}
				if node.Type == "analyze" {
					hasAnalyzeLeaf = true
				}
			}
		}
		// ADR-0053: Analyze nodes always get a downstream synthesis step.
		// Their internal probe synthesis is insufficient for data analysis
		// results — the Recall Node's Map-Reduce strategy is required.
		// Only regular probe nodes skip when they're sole leaves.
		if hasProbeLeaf && !hasAnalyzeLeaf {
			fmt.Printf("[Compiler] Graph has a sole probe leaf. Skipping automatic terminal_synthesis injection.\n")
			hasSynthesisLeaf = true
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
			if (node.Type == "deterministic" || node.Type == "action" || node.Type == "probe" || node.Type == "analyze" || node.Type == "sub_dag" || node.Type == "recall") && !isSourceMap[node.ID] {
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

// cacheTools are the tools available to cache bridge and analyze nodes.
var cacheTools = []string{"introspect_cache", "sql_cached_data"}

// referencesTabularFile returns true if the instructions contain a tabular file extension.
func referencesTabularFile(instructions string) bool {
	return tabularExtRe.MatchString(instructions)
}

// hasCacheToolsInAllowed returns true if any of the node's allowedTools overlap with cache tools.
func hasCacheToolsInAllowed(tools []string) bool {
	for _, tool := range tools {
		for _, ct := range cacheTools {
			if tool == ct {
				return true
			}
		}
	}
	return false
}

// injectCacheBridgeNodes scans the high-level nodes for tabular file references
// and injects CacheBridgeNodes between them and their downstream targets.
// Also expands probe node allowedTools when referencing tabular files.
func injectCacheBridgeNodes(originalNodes []GraphNode, sctNodes []GraphNode, sctEdges []GraphEdge, execNodeMap map[string]string) ([]GraphNode, []GraphEdge) {
	// Build a lookup of original nodes by ID
	originalNodeMap := make(map[string]GraphNode)
	for _, n := range originalNodes {
		originalNodeMap[n.ID] = n
	}

	// Expand probe allowedTools for tabular references (spec §5.1)
	// Trigger: probe has read_file in allowedTools (may encounter tabular at runtime)
	// OR probe instructions explicitly reference tabular file extensions
	for i, node := range sctNodes {
		if node.Type == "probe" && node.ProbeConfig != nil {
			hasReadFile := false
			for _, tool := range node.ProbeConfig.AllowedTools {
				if tool == "read_file" {
					hasReadFile = true
					break
				}
			}
			if (hasReadFile || referencesTabularFile(node.Instructions)) && !hasCacheToolsInAllowed(node.ProbeConfig.AllowedTools) {
				sctNodes[i].ProbeConfig.AllowedTools = append(sctNodes[i].ProbeConfig.AllowedTools, cacheTools...)
			}
		}
	}

	// For each original node that references tabular files, check if a bridge is needed
	for _, origNode := range originalNodes {
		if !referencesTabularFile(origNode.Instructions) {
			continue
		}
		if origNode.Type == "probe" || origNode.Type == "synthesis" || origNode.Type == "analyze" {
			continue // Probes handle cache tools via expansion; synthesis doesn't produce profiles;
			// analyze nodes query SQL directly
		}

		// Resolve the exec node ID for this original node
		execID, exists := execNodeMap[origNode.ID]
		if !exists {
			continue
		}

		// Check if any downstream node already has cache tools or is an analyze node
		hasDownstreamCacheTools := false
		for _, edge := range sctEdges {
			if edge.SourceID == execID {
				for _, node := range sctNodes {
					if node.ID == edge.TargetID {
						if hasCacheToolsInAllowed(node.AllowedTools) || node.Type == "analyze" {
							hasDownstreamCacheTools = true
							break
						}
					}
				}
			}
		}
		if hasDownstreamCacheTools {
			continue
		}

		// Inject cache bridge node
		bridgeID := "cache_bridge_" + origNode.ID
		bridgeNode := GraphNode{
			ID:     bridgeID,
			Type:   "action",
			Action: "sql_cached_data",
			Instructions: "Query the cached tabular data from the upstream node's Data Profile. " +
				"Use the cacheId from the upstream output. " +
				"Execute: SELECT * FROM cache_<id> LIMIT 100 to return a representative sample.",
			AllowedTools:        cacheTools,
			Status:              "pending",
			ActivationThreshold: 0.0, // Deterministic — no Edge Thought overhead
		}
		sctNodes = append(sctNodes, bridgeNode)

		// Re-wire edges: find all edges leaving execID and re-route through bridge
		var newEdges []GraphEdge
		bridgeConnected := false
		for _, edge := range sctEdges {
			if edge.SourceID == execID {
				// Replace: execID → target becomes execID → bridge, bridge → target
				if !bridgeConnected {
					newEdges = append(newEdges, GraphEdge{
						SourceID: execID,
						TargetID: bridgeID,
					})
					bridgeConnected = true
				}
				newEdges = append(newEdges, GraphEdge{
					SourceID: bridgeID,
					TargetID: edge.TargetID,
				})
			} else {
				newEdges = append(newEdges, edge)
			}
		}

		// If execID had no outgoing edges (it's a leaf), just connect it to the bridge
		if !bridgeConnected {
			newEdges = append(newEdges, GraphEdge{
				SourceID: execID,
				TargetID: bridgeID,
			})
		}

		sctEdges = newEdges
	}

	return sctNodes, sctEdges
}
