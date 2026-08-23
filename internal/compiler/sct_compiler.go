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

// ActiveExpander is the strategy-aware node expander, set during executor
// initialization when the strategy registry is populated. When non-nil,
// ExpandToSCTGraph consults it for each node before falling back to built-in
// expansion logic. This allows custom Agent App strategies to inject
// compilation rules without modifying the compiler.
var ActiveExpander NodeExpander

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
		// Normalize node type synonyms from LLM mutation
		switch strings.ToLower(strings.TrimSpace(node.Type)) {
		case "synthesize", "summarize", "summarizer", "reducer", "reduce":
			node.Type = "synthesis"
		case "validator", "validation":
			node.Type = "semantic_validator"
		case "explore", "discover", "crawler", "scanner":
			node.Type = "probe"
		case "analyzer", "data_analysis", "sql":
			node.Type = "analyze"
		case "tool", "tool_call", "exec":
			node.Type = "action"
		}

		// ADR-0069: Custom strategy compilation rules take precedence over
		// built-in type switching. If ActiveExpander handles a node type,
		// its result replaces the built-in expansion logic entirely.
		if ActiveExpander != nil {
			if result, err := ActiveExpander.Expand(&node, graph); err != nil {
				return nil, fmt.Errorf("custom expansion for node %q failed: %w", node.ID, err)
			} else if result != nil {
				if len(result.ReplacementNodes) > 0 {
					sctNodes = append(sctNodes, result.ReplacementNodes...)
					// Map original ID to last replacement node for edge rewiring
					lastID := result.ReplacementNodes[len(result.ReplacementNodes)-1].ID
					execNodeMap[node.ID] = lastID
					if len(result.ReplacementNodes) > 1 {
						bridgeNodeMap[node.ID] = result.ReplacementNodes[0].ID
					}
				} else if result.ModifiedNode != nil {
					sctNodes = append(sctNodes, *result.ModifiedNode)
					execNodeMap[node.ID] = result.ModifiedNode.ID
				} else {
					sctNodes = append(sctNodes, node)
					execNodeMap[node.ID] = node.ID
				}
				sctNodes = append(sctNodes, result.AdditionalNodes...)
				sctEdges = append(sctEdges, result.AdditionalEdges...)
				continue
			}
		}

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

			// Auto-wire DynamicBindings for document sink tools (e.g. write_file)
			// when following upstream probe, analyze, recall, or synthesis nodes.
			// The content parameter binds directly to upstream synthesis output,
			// allowing ADR-0030 Proactive Splice to strip content from the validator schema
			// and splice the full text deterministically after path extraction.
			if node.Action == "write_file" {
				if node.DynamicBindings == nil {
					node.DynamicBindings = make(map[string]interface{})
				}
				if _, hasContent := node.DynamicBindings["content"]; !hasContent {
					for _, edge := range graph.Edges {
						if edge.TargetID == node.ID {
							upstreamID := edge.SourceID
							for _, orig := range graph.Nodes {
								if orig.ID == upstreamID {
									if orig.Type == "probe" || orig.Type == "analyze" || orig.Type == "recall" || orig.Type == "synthesis" || isSynthesisGoal(orig.Instructions) {
										// If the upstream is a probe/analyze that gets an auto-injected recall node, bind to the recall node output
										targetBindingNode := upstreamID
										if orig.Type == "probe" || orig.Type == "analyze" {
											targetBindingNode = upstreamID + "_recall"
										}
										node.DynamicBindings["content"] = fmt.Sprintf("%s.output", targetBindingNode)
										fmt.Fprintf(os.Stderr, "[KahnCompiler] Auto-wired DynamicBinding content for %s -> %s.output\n", node.ID, targetBindingNode)
									}
									break
								}
							}
							break
						}
					}
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
							Goal:                node.Instructions,
							AllowedTools:        cacheTools,
							StepBudget:          15,
							CompactEvery:        3,
							CompactionLevel:     CompactPreserve,
							SourceHint:          "cache", // Phase gate discriminator — only "cache" hint activates the sql_cached_data requirement
							RequiredToolDispatch: []string{"sql_cached_data"}, // ADR-0068: deterministic dispatch gate
						}
					}
					// ADR-0068: Ensure RequiredToolDispatch is always set for analyze nodes,
					// even when ProbeConfig was pre-populated by the planner.
					if len(node.ProbeConfig.RequiredToolDispatch) == 0 {
						node.ProbeConfig.RequiredToolDispatch = []string{"sql_cached_data"}
					}
					if node.ProbeConfig.SourceHint == "" {
						node.ProbeConfig.SourceHint = "cache"
					}
					// Ensure cache tools are always present in AllowedTools
					if !hasCacheToolsInAllowed(node.AllowedTools) {
						node.AllowedTools = append(node.AllowedTools, cacheTools...)
					}
					if !hasCacheToolsInAllowed(node.ProbeConfig.AllowedTools) {
						node.ProbeConfig.AllowedTools = append(node.ProbeConfig.AllowedTools, cacheTools...)
					}
				}

				// SourceHint-driven tool provisioning (primary): the planner sets
				// sourceHint on probe nodes to declaratively control tool injection.
				if node.Type == "probe" && node.ProbeConfig != nil && node.ProbeConfig.SourceHint == "web" {
					webTools := []string{"web_search", "web_browse"}
					if !hasWebToolsInAllowed(node.AllowedTools) {
						node.AllowedTools = append(node.AllowedTools, webTools...)
						fmt.Fprintf(os.Stderr, "[KahnCompiler] SourceHint=web: injected web tools into %s\n", node.ID)
					}
					if !hasWebToolsInAllowed(node.ProbeConfig.AllowedTools) {
						node.ProbeConfig.AllowedTools = append(node.ProbeConfig.AllowedTools, webTools...)
					}
				}

				// Research tool propagation (fallback heuristic): ensure web tools reach
				// probe nodes whose instructions indicate web research intent, even when
				// the planner omitted sourceHint. Mirrors the cache tool auto-injection
				// pattern above.
				if node.Type == "probe" && looksLikeResearchNode(node.Instructions) {
					webTools := []string{"web_search", "web_browse"}
					if !hasWebToolsInAllowed(node.AllowedTools) {
						node.AllowedTools = append(node.AllowedTools, webTools...)
						fmt.Fprintf(os.Stderr, "[KahnCompiler] Research heuristic fallback: injected web tools into %s\n", node.ID)
					}
					if node.ProbeConfig != nil && !hasWebToolsInAllowed(node.ProbeConfig.AllowedTools) {
						node.ProbeConfig.AllowedTools = append(node.ProbeConfig.AllowedTools, webTools...)
					}
				}

				// F2 guardrail: web_browse without web_search causes FUTILITY aborts.
				// You can't browse URLs you haven't searched for. Ensure web_search
				// is always present when web_browse is in allowedTools.
				if node.Type == "probe" {
					hasBrowse := false
					hasSearch := false
					for _, t := range node.AllowedTools {
						if t == "web_browse" {
							hasBrowse = true
						}
						if t == "web_search" {
							hasSearch = true
						}
					}
					if hasBrowse && !hasSearch {
						node.AllowedTools = append(node.AllowedTools, "web_search")
						fmt.Fprintf(os.Stderr, "[KahnCompiler] PlanGuardrail: injected web_search (web_browse requires web_search) into %s\n", node.ID)
					}
					// Mirror into ProbeConfig
					if node.ProbeConfig != nil {
						hasBrowse, hasSearch = false, false
						for _, t := range node.ProbeConfig.AllowedTools {
							if t == "web_browse" {
								hasBrowse = true
							}
							if t == "web_search" {
								hasSearch = true
							}
						}
						if hasBrowse && !hasSearch {
							node.ProbeConfig.AllowedTools = append(node.ProbeConfig.AllowedTools, "web_search")
						}
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

				if !hasPlannedSynthesisChild {
					// Check if this is a terminal analyze node (v3 data passthrough).
					// Analyze nodes with SourceHint="cache" that have NO outgoing edges
					// are terminal — their data passthrough output IS the answer.
					// Injecting a Recall node would re-synthesize structured data into
					// bad prose (FM-21 regression). Skip Recall for these nodes.
					isTerminalAnalyze := false
					if node.ProbeConfig != nil && node.ProbeConfig.SourceHint == "cache" {
						hasOutgoingEdge := false
						for _, edge := range graph.Edges {
							if edge.SourceID == node.ID {
								hasOutgoingEdge = true
								break
							}
						}
						if !hasOutgoingEdge {
							isTerminalAnalyze = true
							fmt.Fprintf(os.Stderr, "[Compiler] Analyze node %s is terminal (no outgoing edges). Skipping Recall injection — data passthrough is the final output.\n", node.ID)
						}
					}

					shouldInjectRecall := true
					if isTerminalAnalyze {
						shouldInjectRecall = false
					} else if node.ProbeConfig != nil && node.ProbeConfig.DirectSynthesis {
						shouldInjectRecall = false
						fmt.Fprintf(os.Stderr, "[Compiler] Probe node %s is DirectSynthesis. Skipping Recall injection.\n", node.ID)
					} else if discoveryNodesCount > 1 {
						// ADR-0079: Dependency-Gated Recall Injection for multi-probe graphs.
						// In multi-probe exploration graphs, only inject intermediate Recall if
						// this probe directly feeds an intermediate Tool Sink or dynamic branch.
						feedsToolSink := probeFeedsToolSinkInGraph(graph, node.ID)
						if !feedsToolSink {
							shouldInjectRecall = false
							fmt.Fprintf(os.Stderr, "[Compiler] Multi-probe graph (%d discovery nodes): probe %s does not feed a tool sink. Skipping intermediate Recall injection.\n", discoveryNodesCount, node.ID)
						}
					}

					if !shouldInjectRecall {
						// Terminal analyze or intermediate probe in multi-probe exploration fan-out — no Recall. Map directly.
						execNodeMap[node.ID] = node.ID
						bridgeNodeMap[node.ID] = node.ID
					} else {
						// Inject Recall Node to align discovery findings (ADR-0038, ADR-0079)
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
					}
				} else {
					// Probe has a planned synthesis child — skip Recall injection
					fmt.Fprintf(os.Stderr, "[Compiler] Probe %s already has a planned synthesis child. Skipping automatic Recall injection.\n", node.ID)
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

	// ── Auto-inject Analyze Nodes ──
	// For read_file action nodes referencing tabular files with no downstream
	// analyze/probe node, inject an analyze node to reason about the data.
	sctNodes, sctEdges = injectAnalyzeNodes(graph.Nodes, sctNodes, sctEdges, execNodeMap)

	// ── Enforce Analyze Node for data tasks (validation pass) ──
	// Catches cases where the planner skips the Analyze Node for data tasks
	// that don't come through read_file → tabular path (e.g., direct cacheId references).
	sctNodes, sctEdges = ensureAnalyzeForDataTasks(graph.Nodes, sctNodes, sctEdges, execNodeMap, graph.GoalPrompt)

	// Link all execution endpoints (leaves in the original graph) to the terminal synthesis node
	// A node is an endpoint if it is an execution node and has no outbound edges to other high-level steps.
	isSourceMap := make(map[string]bool)
	for _, edge := range sctEdges {
		isSourceMap[edge.SourceID] = true
	}

	// 3. Inject terminal synthesis node
	// Per-leaf classification: inject terminal_synthesis iff any terminal leaf
	// is neither a write/save sink nor already a synthesis/recall type node.
	// ADR-run32: hasSynthesisLeaf + isSynthesisGoal(instructions) caused false
	// positives when an analyze node with "synthesize" in its instructions became
	// a leaf, suppressing synthesis injection for actual exec leaves.
	var leafNodes []GraphNode
	for _, node := range sctNodes {
		if !isSourceMap[node.ID] {
			leafNodes = append(leafNodes, node)
		}
	}

	needsSynthesis := false
	for _, leaf := range leafNodes {
		isSink := isToolSinkNode(leaf)
		isSynthType := leaf.Type == "synthesis" || leaf.Type == "recall"
		isCacheAnalyze := leaf.Type == "analyze" && leaf.ProbeConfig != nil && leaf.ProbeConfig.SourceHint == "cache"
		if !isSink && !isSynthType && !isCacheAnalyze {
			needsSynthesis = true
			break
		}
	}

	allLeavesAreToolSinks := len(leafNodes) > 0 && !needsSynthesis
	for _, leaf := range leafNodes {
		if !isToolSinkNode(leaf) {
			allLeavesAreToolSinks = false
			break
		}
	}

	if needsSynthesis {
		synthID := "terminal_synthesis"
		synthThreshold := 0.7
		if isBenchmark {
			synthThreshold = 0.0
		}
		sctNodes = append(sctNodes, GraphNode{
			ID:                  synthID,
			Type:                "synthesis",
			Instructions:        "Summarize and compile all prior action outputs into a final cohesive response. IMPORTANT: If you did not successfully find or read the relevant information, state that you did not find it. Do NOT guess or invent implementation details. You MUST answer using ONLY the data provided in context. Do NOT ask for more information, request clarification, or say you don't have access to data. Produce your best answer from what is available.",
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
	} else if allLeavesAreToolSinks {
		fmt.Printf("[Compiler] All terminal leaves are tool sinks (%d leaves). Skipping automatic terminal_synthesis injection.\n", len(leafNodes))
	} else {
		fmt.Printf("[Compiler] Graph already has a synthesis leaf. Skipping automatic terminal_synthesis injection.\n")
	}

	// ── Terminal File Sink injection (ADR-0088) ──
	// For tasks requesting file output where no write_file node exists,
	// append a deterministic write_file Action Node bound to the terminal synthesis leaf.
	sctNodes, sctEdges = injectTerminalFileSink(graph, sctNodes, sctEdges)

	return &ExecutionGraph{
		TaskID:     graph.TaskID,
		GoalPrompt: graph.GoalPrompt,
		Nodes:      sctNodes,
		Edges:      sctEdges,
		MaxCycles:  graph.MaxCycles,
		CreatedAt:  time.Now().Unix(),
	}, nil
}

var targetFilePathRe = regexp.MustCompile(`(?i)(?:save|write|output|export)\s+(?:the\s+)?(?:results?|findings?|docs?|documentation|report|readme|summary|file)?\s*(?:to|into|in)\s+[` + "`" + `"]?([a-zA-Z0-9_\-\.\/]+\.[a-zA-Z0-9]+)[` + "`" + `"]?`)

func extractTargetFilePath(prompt string) string {
	matches := targetFilePathRe.FindStringSubmatch(prompt)
	if len(matches) > 1 {
		path := strings.TrimSpace(matches[1])
		if !strings.HasSuffix(path, ".") && strings.Contains(path, ".") {
			return path
		}
	}
	return ""
}

// injectTerminalFileSink checks if the task requests saving/writing to a file,
// and injects a deterministic write_file Action Node bound to the terminal synthesis output if none exists (ADR-0088).
func injectTerminalFileSink(graph *ExecutionGraph, sctNodes []GraphNode, sctEdges []GraphEdge) ([]GraphNode, []GraphEdge) {
	if graph == nil || strings.TrimSpace(graph.GoalPrompt) == "" {
		return sctNodes, sctEdges
	}

	// Check if write_file is already present in graph.Nodes or sctNodes
	for _, n := range graph.Nodes {
		if n.Action == "write_file" {
			return sctNodes, sctEdges
		}
	}
	for _, n := range sctNodes {
		if n.Action == "write_file" {
			return sctNodes, sctEdges
		}
	}

	// Extract target file path from goal prompt
	targetPath := extractTargetFilePath(graph.GoalPrompt)
	if targetPath == "" {
		return sctNodes, sctEdges
	}

	// Find the terminal synthesis / leaf node to bind to
	var synthLeafID string
	for _, n := range sctNodes {
		if n.ID == "terminal_synthesis" {
			synthLeafID = n.ID
			break
		}
		if n.Type == "synthesis" || n.Type == "recall" {
			synthLeafID = n.ID
		}
	}

	if synthLeafID == "" && len(sctNodes) > 0 {
		synthLeafID = sctNodes[len(sctNodes)-1].ID
	}

	if synthLeafID == "" {
		return sctNodes, sctEdges
	}

	sinkID := "terminal_write_file"
	sinkNode := GraphNode{
		ID:           sinkID,
		Type:         "action",
		Action:       "write_file",
		Instructions: fmt.Sprintf("Write the generated content to %s", targetPath),
		AllowedTools: []string{"write_file"},
		Status:       "pending",
		StaticArgs:   fmt.Sprintf(`{"path": "%s"}`, targetPath),
		DynamicBindings: map[string]interface{}{
			"path":    targetPath,
			"content": synthLeafID + ".output",
		},
	}

	sctNodes = append(sctNodes, sinkNode)
	sctEdges = append(sctEdges, GraphEdge{
		SourceID: synthLeafID,
		TargetID: sinkID,
	})

	fmt.Printf("[Compiler] Injected terminal write_file sink %q bound to %q for target %q (ADR-0088)\n", sinkID, synthLeafID, targetPath)
	return sctNodes, sctEdges
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
// Includes both low-level SQL tools and compound data tools that translate
// structured parameters to SQL (preventing 4B model syntax errors).
var cacheTools = []string{
	"introspect_cache", "sql_cached_data",
	"count_by", "group_by", "filter_where", "top_n", "describe_cache",
}

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
				"Execute: SELECT * FROM cache_<id> LIMIT 5 to return a representative sample.",
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

// dataIntentPatterns are phrases that indicate the task involves data analysis
// or aggregation. When matched AND no analyze node exists in the graph, the
// compiler injects one to prevent raw data from entering the synthesis context.
var dataIntentPatterns = []string{
	"count by", "group by", "aggregate", "breakdown", "distribution",
	"top n", "top 5", "top 10", "top 20",
	"how many", "total number", "sum of",
	"average", "median",
	"filter by", "filter where",
	"sort by", "order by", "rank",
	"analyze the data", "analyze this data", "data analysis",
	"sector breakdown", "by country", "by company", "by owner",
	"lookup", "look up",
}

// ensureAnalyzeForDataTasks is a validation pass that fires after cache bridge
// and analyze node injection. It checks if the task goal or any node instructions
// indicate data analysis intent, and if so, verifies that at least one Analyze
// Node exists in the compiled DAG. If not, injects one between the last
// cache-related node and the terminal synthesis.
func ensureAnalyzeForDataTasks(
	originalNodes []GraphNode,
	sctNodes []GraphNode,
	sctEdges []GraphEdge,
	execNodeMap map[string]string,
	goalPrompt string,
) ([]GraphNode, []GraphEdge) {
	// Check 1: Does the task have data intent?
	if !hasDataIntent(goalPrompt, originalNodes) {
		return sctNodes, sctEdges
	}

	// Check 2: Does the graph already have an analyze node?
	for _, node := range sctNodes {
		if node.Type == "analyze" {
			return sctNodes, sctEdges // already has one — no injection needed
		}
	}

	// Check 3: Find the best node to attach the analyze node after.
	// Priority: cache_bridge > read_file exec > any node with cache tools
	var attachAfterID string
	for _, node := range sctNodes {
		if strings.HasPrefix(node.ID, "cache_bridge_") {
			attachAfterID = node.ID
			break
		}
	}
	if attachAfterID == "" {
		for _, node := range sctNodes {
			if hasCacheToolsInAllowed(node.AllowedTools) {
				attachAfterID = node.ID
				break
			}
		}
	}
	if attachAfterID == "" {
		// No cache-related node found — the planner may have a completely
		// different graph structure. Don't inject blindly.
		return sctNodes, sctEdges
	}

	// Inject analyze node
	analyzeID := "analyze_enforced"
	analyzeNode := GraphNode{
		ID:           analyzeID,
		Type:         "analyze",
		Instructions: "Analyze the cached data to answer: " + goalPrompt,
		AllowedTools: append([]string{}, cacheTools...),
		ProbeConfig: &ProbeConfig{
			Goal:                "Analyze the cached data to answer: " + goalPrompt,
			AllowedTools:        append([]string{}, cacheTools...),
			StepBudget:          15,
			CompactEvery:        3,
			CompactionLevel:     CompactPreserve,
			TaskContext:         goalPrompt,
			SourceHint:          "cache",
			RequiredToolDispatch: []string{"sql_cached_data"}, // ADR-0068
		},
		Status:              "pending",
		ActivationThreshold: 0.0,
	}
	sctNodes = append(sctNodes, analyzeNode)

	// Re-wire edges: route edges leaving attachAfterID through the analyze node
	var newEdges []GraphEdge
	analyzeConnected := false
	for _, edge := range sctEdges {
		if edge.SourceID == attachAfterID {
			if !analyzeConnected {
				newEdges = append(newEdges, GraphEdge{
					SourceID: attachAfterID,
					TargetID: analyzeID,
				})
				analyzeConnected = true
			}
			newEdges = append(newEdges, GraphEdge{
				SourceID: analyzeID,
				TargetID: edge.TargetID,
			})
		} else {
			newEdges = append(newEdges, edge)
		}
	}

	if !analyzeConnected {
		newEdges = append(newEdges, GraphEdge{
			SourceID: attachAfterID,
			TargetID: analyzeID,
		})
	}

	fmt.Fprintf(os.Stderr, "[KahnCompiler] Enforced analyze node %s for data task (attached after %s)\n", analyzeID, attachAfterID)

	return sctNodes, newEdges
}

// hasDataIntent returns true if the goal prompt or any node instructions
// indicate the task involves data analysis or aggregation.
func hasDataIntent(goalPrompt string, nodes []GraphNode) bool {
	lower := strings.ToLower(goalPrompt)
	for _, pattern := range dataIntentPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	for _, node := range nodes {
		lower = strings.ToLower(node.Instructions)
		for _, pattern := range dataIntentPatterns {
			if strings.Contains(lower, pattern) {
				return true
			}
		}
	}
	return false
}


// that have no downstream analyze or probe node. For each such node, injects
// an analyze node with cache-prefixed instructions.
//
// The existing hasDownstreamCacheTools guard in injectCacheBridgeNodes prevents
// cache bridge injection when the analyze node is already downstream — the
// analyze node gets cache tools auto-provisioned by the SCT expander (lines 94-110).
func injectAnalyzeNodes(originalNodes []GraphNode, sctNodes []GraphNode, sctEdges []GraphEdge, execNodeMap map[string]string) ([]GraphNode, []GraphEdge) {
	for _, origNode := range originalNodes {
		// Only target read_file action nodes referencing tabular files
		if origNode.Type != "action" || origNode.Action != "read_file" {
			continue
		}
		if !referencesTabularFile(origNode.Instructions) {
			continue
		}

		// Resolve the exec node ID for this original node
		execID, exists := execNodeMap[origNode.ID]
		if !exists {
			continue
		}

		// Check if any downstream node is already an analyze or probe
		hasDownstreamAnalyze := false
		for _, edge := range sctEdges {
			if edge.SourceID == execID {
				for _, node := range sctNodes {
					if node.ID == edge.TargetID {
						if node.Type == "analyze" || node.Type == "probe" {
							hasDownstreamAnalyze = true
							break
						}
					}
				}
			}
			// Also check nodes reachable through cache bridges
			if !hasDownstreamAnalyze {
				for _, edge2 := range sctEdges {
					if strings.HasPrefix(edge2.SourceID, "cache_bridge_") && edge.SourceID == execID && edge.TargetID == edge2.SourceID {
						for _, node := range sctNodes {
							if node.ID == edge2.TargetID && (node.Type == "analyze" || node.Type == "probe") {
								hasDownstreamAnalyze = true
								break
							}
						}
					}
				}
			}
		}
		if hasDownstreamAnalyze {
			continue
		}

		// Inject analyze node
		analyzeID := "analyze_" + origNode.ID
		analyzeInstructions := fmt.Sprintf(
			"Using the cached tabular data from the upstream node, answer: %s",
			origNode.Instructions,
		)
		analyzeNode := GraphNode{
			ID:           analyzeID,
			Type:         "analyze",
			Instructions: analyzeInstructions,
			AllowedTools: append([]string{}, cacheTools...),
			ProbeConfig: &ProbeConfig{
				Goal:                analyzeInstructions,
				AllowedTools:        append([]string{}, cacheTools...),
				StepBudget:          15,
				CompactEvery:        3,
				CompactionLevel:     CompactPreserve,
				SourceHint:          "cache",
				RequiredToolDispatch: []string{"sql_cached_data"}, // ADR-0068
			},
			Status:              "pending",
			ActivationThreshold: 0.0,
		}
		sctNodes = append(sctNodes, analyzeNode)

		// Wire: find edges leaving execID (or its cache bridge) and re-route through analyze
		// If execID has no outgoing edges (leaf node), just connect execID → analyze
		var newEdges []GraphEdge
		analyzeConnected := false

		// Find the last node in the chain (either execID or its cache_bridge)
		lastNodeID := execID
		bridgeID := "cache_bridge_" + origNode.ID
		for _, edge := range sctEdges {
			if edge.SourceID == execID && edge.TargetID == bridgeID {
				lastNodeID = bridgeID
				break
			}
		}

		for _, edge := range sctEdges {
			if edge.SourceID == lastNodeID && edge.TargetID != analyzeID {
				// Re-route through analyze
				if !analyzeConnected {
					newEdges = append(newEdges, GraphEdge{
						SourceID: lastNodeID,
						TargetID: analyzeID,
					})
					analyzeConnected = true
				}
				newEdges = append(newEdges, GraphEdge{
					SourceID: analyzeID,
					TargetID: edge.TargetID,
				})
			} else {
				newEdges = append(newEdges, edge)
			}
		}

		// If the last node had no outgoing edges (leaf), connect it to analyze
		if !analyzeConnected {
			newEdges = append(newEdges, GraphEdge{
				SourceID: lastNodeID,
				TargetID: analyzeID,
			})
		}

		sctEdges = newEdges
		fmt.Fprintf(os.Stderr, "[KahnCompiler] Auto-injected analyze node %s for tabular read_file %s\n", analyzeID, origNode.ID)
	}

	// Red-team FM-10 fix: Convert probe nodes that reference tabular files
	// directly into analyze nodes. When the planner emits a probe (type: "probe")
	// instead of an action node for CSV analysis, the task bypasses the structured
	// query pipeline and falls into generic file exploration where the 4B model fails.
	// This conversion ensures tabular data always goes through AnalyzePhases.
	for i, node := range sctNodes {
		if node.Type != "probe" || node.ProbeConfig == nil {
			continue
		}
		// Check if probe instructions reference a tabular file
		if !referencesTabularFile(node.Instructions) && !referencesTabularFile(node.ProbeConfig.Goal) {
			continue
		}
		// Skip if already configured as a cache analyze node
		if node.ProbeConfig.SourceHint == "cache" {
			continue
		}
		// Convert probe → analyze in-place
		sctNodes[i].Type = "analyze"
		sctNodes[i].AllowedTools = append([]string{}, cacheTools...)
		sctNodes[i].ProbeConfig.AllowedTools = append([]string{}, cacheTools...)
		sctNodes[i].ProbeConfig.SourceHint = "cache"
		sctNodes[i].ProbeConfig.CompactionLevel = CompactPreserve
		if sctNodes[i].ProbeConfig.StepBudget < 15 {
			sctNodes[i].ProbeConfig.StepBudget = 15
		}
		fmt.Fprintf(os.Stderr, "[KahnCompiler] Converted probe %s to analyze (tabular file detected in instructions)\n", node.ID)
	}

	return sctNodes, sctEdges
}

// researchPatterns are phrases that indicate a probe node is intended for
// web research rather than codebase exploration.
var researchPatterns = []string{
	"web_search",
	"web_browse",
	"search the web",
	"internet",
	"find sources",
	"web research",
	"online",
	"authoritative sources",
	"urls",
	"search for",
	"browse",
	"websites",
}

// looksLikeResearchNode returns true if the node's instructions indicate
// web research intent rather than codebase exploration.
func looksLikeResearchNode(instructions string) bool {
	lower := strings.ToLower(instructions)
	for _, pattern := range researchPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// hasWebToolsInAllowed returns true if any of the tools are web research tools.
func hasWebToolsInAllowed(tools []string) bool {
	for _, tool := range tools {
		if tool == "web_search" || tool == "web_browse" {
			return true
		}
	}
	return false
}

func isToolSinkAction(action string) bool {
	switch action {
	case "write_file", "patch_file", "edit_file", "replace_file_content", "append_file",
		"save_memory", "save_artifact", "postgres_insert", "db_insert", "db_query_exec",
		"git_commit":
		return true
	default:
		return false
	}
}

func isToolSinkNode(node GraphNode) bool {
	if isToolSinkAction(node.Action) {
		return true
	}
	if node.Type == "deterministic" || node.Type == "action" {
		if strings.HasPrefix(node.Action, "write_") || strings.HasPrefix(node.Action, "save_") || strings.HasPrefix(node.Action, "patch_") || strings.HasPrefix(node.Action, "edit_") {
			return true
		}
	}
	return false
}

func probeFeedsToolSinkInGraph(graph *ExecutionGraph, probeNodeID string) bool {
	if graph == nil {
		return false
	}
	visited := make(map[string]bool)
	var queue []string

	for _, edge := range graph.Edges {
		if edge.SourceID == probeNodeID {
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
				if n.Type == "action" {
					for _, edge := range graph.Edges {
						if edge.SourceID == currID {
							queue = append(queue, edge.TargetID)
						}
					}
				}
			}
		}
	}
	return false
}

