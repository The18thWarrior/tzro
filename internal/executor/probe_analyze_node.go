package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// runProbeAnalyzeCore contains the domain-core logic for probe/analyze nodes.
// Returns (synthesis, error) without managing state, hooks, or events.
// Called by ProbeAnalyzeStrategy.Execute → dispatch envelope handles ceremony.
func (e *ExecutionEngine) runProbeAnalyzeCore(
	ctx context.Context,
	graph *compiler.ExecutionGraph,
	node *compiler.GraphNode,
	executionTier string,
	meta inference.StreamMeta,
	interpolatedPrompt string,
) (string, error) {
	taskID := graph.TaskID

		probeConfig := compiler.ProbeConfig{
			Goal:         node.Instructions,
			AllowedTools: node.AllowedTools,
			StepBudget:   20, // Unified default — matches probe.go
			CompactEvery: 3,
		}
		if node.ProbeConfig != nil {
			probeConfig = *node.ProbeConfig
		}

		// Fix 4 (Probe allowedTools Enrichment): Runtime expansion of cache tools
		// for probes/analyze nodes that need to query cached data. Three triggers:
		//
		// (a) Probe has read_file in allowedTools — it may encounter tabular data
		//     at runtime, which the Data Profiler will cache automatically.
		//     No need to regex-match file extensions in instructions; the profiler
		//     at filesystem.go:IsTabularExtension detects this natively.
		// (b) Upstream completed nodes contain cacheId + dataProfile markers,
		//     meaning tabular data was already read and cached — the analyze node
		//     needs SQL tools to query it.
		// (c) The node's instructions contain a cacheId pattern directly.
		if (node.Type == "probe" || node.Type == "analyze") && !isAnalyzeConfig(probeConfig.AllowedTools) {
			shouldExpand := false

			// (a) Probe has read_file — may encounter tabular data at runtime
			for _, t := range probeConfig.AllowedTools {
				if t == "read_file" {
					shouldExpand = true
					break
				}
			}

			// (b) Upstream completed nodes already produced cached tabular data
			if !shouldExpand {
				states := memory.DB.GetAllNodeStates(taskID)
				for _, state := range states {
					if state.Status == "completed" {
						raw := state.RawOutput
						if raw == "" {
							raw = state.Output
						}
						if strings.Contains(raw, "cacheId") && strings.Contains(raw, "dataProfile") {
							shouldExpand = true
							break
						}
					}
				}
			}

			// (c) Instructions contain a cacheId pattern (cache_NNNN)
			if !shouldExpand {
				cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
				if cacheIdRe.MatchString(node.Instructions) {
					shouldExpand = true
				}
			}

			if shouldExpand {
				probeConfig.AllowedTools = append(probeConfig.AllowedTools,
					"introspect_cache",
					"count_by", "group_by", "filter_where", "top_n", "describe_cache",
				)
				node.AllowedTools = probeConfig.AllowedTools
				fmt.Fprintf(os.Stderr, "[Executor] Expanded probe allowedTools with cache + compound data tools for %s\n", node.ID)
			}
		}

		// Inject the original task spec/goal so the Probe knows the actual
		// requirements (e.g., target language) even when the workspace context
		// suggests different patterns. Only set if not already provided by planner.
		if probeConfig.TaskContext == "" && graph.GoalPrompt != "" {
			probeConfig.TaskContext = graph.GoalPrompt
		}

		// Fix 5 (Goal Propagation): Override a generic/template probe goal with
		// the full GoalPrompt. The 4B local planner often fails to mutate the
		// template probe goal (e.g., leaves "Explore the target and produce content
		// for documentation" instead of the specific task). This causes the probe
		// to explore with a vague goal → off-topic synthesis (benchmark: docgen
		// avg quality 1.50/5 due to task misunderstanding).
		//
		// Deterministic scaffolding (SOLUTION_APPROACH Principle 3): the Go harness
		// guarantees correct goal propagation instead of relying on the small model.
		if graph.GoalPrompt != "" {
			goalIsGeneric := isGenericTemplateGoal(probeConfig.Goal)
			if goalIsGeneric {
				fmt.Fprintf(os.Stderr, "[Executor] Probe %s: overriding generic goal %q with GoalPrompt (%d chars)\n",
					node.ID, probeConfig.Goal, len(graph.GoalPrompt))
				probeConfig.Goal = graph.GoalPrompt
			}
		}

		// Inject accumulated context from completed upstream nodes so the
		// probe/analyze Thought Chain can see outputs from prior DAG steps
		// (e.g., cacheId from an upstream read_file execution). Without this,
		// analyze nodes have no way to discover the cacheId and must guess,
		// causing futility aborts when all cache lookups fail.
		if probeConfig.UpstreamContext == "" {
			upstreamCtx := buildAccumulatedContext(taskID, graph, node.Type)
			if upstreamCtx != "" {
				// Enrich analyze nodes with introspect_cache schema so the probe
				// sees the actual data shape (flat JSON array) and column names,
				// enabling correct SQL query generation.
				if isAnalyzeConfig(node.AllowedTools) {
					upstreamCtx = enrichCacheBridgeContext(ctx, upstreamCtx, node.Instructions)
				}
				probeConfig.UpstreamContext = upstreamCtx
				fmt.Fprintf(os.Stderr, "[Executor] Injected %d chars of upstream context into %s node %s\n", len(upstreamCtx), node.Type, node.ID)
			}
		}

		// Auto-detect PreloadPaths from probe instructions if not explicitly set.
		// Scans the goal text for directory-like paths (e.g., "internal/cache/",
		// "docs/adr/") and resolves them against the project root. Only existing
		// directories are added. This universal mechanism gives every probe
		// pre-loaded context without requiring the planner to know about PreloadPaths.
		//
		// Skip for web-only probes: when allowedTools is exclusively web tools
		// (web_search, web_browse), injecting local directory content contaminates
		// the synthesis context and causes degenerate output (benchmark R14:
		// technical_deep_dive_gguf 4.75→1.00 regression from preloading docs/).
		//
		// Skip for analyze nodes (SourceHint="cache"): these get data through
		// the cache bridge — preloading directory content produces empty/irrelevant
		// context. Uses SourceHint (authoritative) instead of tool-presence
		// heuristic (isCacheEquippedProbe) which falsely triggers when Fix 4
		// runtime-injects cache tools into regular probes.
		isCacheAnalyzeNode := probeConfig.SourceHint == "cache"
		if len(probeConfig.PreloadPaths) == 0 && !isWebOnlyProbe(probeConfig.AllowedTools) && !isCacheAnalyzeNode {
			probeConfig.PreloadPaths = detectPreloadPaths(probeConfig.Goal, probeConfig.TaskContext)
		} else if isWebOnlyProbe(probeConfig.AllowedTools) {
			fmt.Fprintf(os.Stderr, "[Probe] Skipping PreloadPaths auto-detection for web-only probe %s (allowedTools: %v)\n", node.ID, probeConfig.AllowedTools)
		} else if isCacheAnalyzeNode {
			fmt.Fprintf(os.Stderr, "[Probe] Skipping PreloadPaths auto-detection for cache analyze node %s (SourceHint=cache)\n", node.ID)
		}

		// Collect binding keys that downstream nodes need from this probe's output.
		// Scan all nodes' DynamicBindings for references to this probe node (format:
		// "probeNodeId.output.propertyName") and extract the property names. These
		// keys will be injected into the synthesis schema so the GBNF grammar forces
		// the local model to produce them as structured JSON fields.
		var downstreamBindingKeys []string
		bindingKeySet := make(map[string]bool)
		for _, otherNode := range graph.Nodes {
			for _, rawBinding := range otherNode.DynamicBindings {
				bindingPath := fmt.Sprintf("%v", rawBinding)
				parts := strings.SplitN(bindingPath, ".", 3) // ["nodeId", "output", "propertyName"]
				if len(parts) == 3 && parts[0] == node.ID && parts[1] == "output" {
					key := parts[2]
					if !bindingKeySet[key] && key != "synthesis" {
						bindingKeySet[key] = true
						downstreamBindingKeys = append(downstreamBindingKeys, key)
					}
				}
			}
		}
		if len(downstreamBindingKeys) > 0 {
			fmt.Fprintf(os.Stderr, "[Executor] Probe %s: downstream binding keys: %v\n", node.ID, downstreamBindingKeys)
		}

		probeEngine := ProbeInferenceEngine(&ProbeInference{})
		if config.GetProbeUseWorkerModel() {
			fmt.Fprintf(os.Stderr, "[Executor] Probe %s: using worker model for step inference (probeUseWorkerModel=true)\n", node.ID)
			// When probeUseWorkerModel is set, we still use ProbeInference — the call sites
			// control routing via ModelTarget. The config flag is vestigial; TargetAuto already
			// routes unconstrained calls to worker.
		}
		synthesisEngine := &ProbeInference{}
		// ADR-0055: Inject dispatch recorder so probe tool calls are captured
		probeCtx := context.WithValue(ctx, DispatchRecorderKey, func(toolName string, args map[string]interface{}) {
			e.RecordDispatch(taskID, toolName, args)
		})

		var synthesis string
		var err error

		if config.GetUsePhaseRunner() {
			// Phase Runner dispatch — route by SourceHint.
			//
			// DirectSynthesis promotion (port from RunProbe:374-419):
			// When PreloadPaths content fits within the local model's effective
			// context budget, bypass the PhaseRunner and run single-shot synthesis.
			//
			// Threshold: 28K chars ≈ 8K tokens. The 4B model has a 16K context
			// window; 8K tokens for content leaves 8K for system prompt + output.
			// At 200K (the original value), 52K files consumed 15K tokens and
			// left ~1K for output → hallucination (benchmark: cache_function_index).
			const maxDirectSynthesisChars = 40_000
			if len(probeConfig.PreloadPaths) > 0 && !probeConfig.DirectSynthesis && probeConfig.SourceHint != "web" && probeConfig.SourceHint != "cache" {
				fullContent := preloadDirectoryContext(probeConfig.PreloadPaths, 10*1024*1024)
				if len(fullContent) > 0 && len(fullContent) <= maxDirectSynthesisChars {
					// Promote to DirectSynthesis — write to temp file, bypass PhaseRunner
					contextFile := filepath.Join(probeConfig.PreloadPaths[0], ".preload_context_full.md")
					if writeErr := os.WriteFile(contextFile, []byte(fullContent), 0644); writeErr == nil {
						probeConfig.DirectSynthesis = true
						probeConfig.ContextFile = contextFile
						defer os.Remove(contextFile)
						fmt.Fprintf(os.Stderr, "[Executor] Probe %s: preload content (%d chars) fits DirectSynthesis cap — promoting\n",
							node.ID, len(fullContent))
					}
				} else if len(fullContent) > maxDirectSynthesisChars {
					// Content exceeds DirectSynthesis cap — inject truncated preload
					// as TaskContext for the orient phase to use
					maxChars := probeConfig.PreloadMaxChars
					if maxChars <= 0 {
						maxChars = 32768
					}
					if len(fullContent) > maxChars {
						fullContent = fullContent[:maxChars]
					}
					probeConfig.TaskContext = fmt.Sprintf("%s\n\nPre-loaded source context (%d chars, truncated):\n%s",
						probeConfig.TaskContext, len(fullContent), fullContent)
					fmt.Fprintf(os.Stderr, "[Executor] Probe %s: preload content (%d chars) exceeds DirectSynthesis cap (%d) — injecting %d chars into TaskContext for PhaseRunner\n",
						node.ID, len(fullContent), maxDirectSynthesisChars, len(fullContent))
				}
			}

			// ADR-0086: Check Repository Pre-Index for instant DirectSynthesis promotion
			if !probeConfig.DirectSynthesis && probeConfig.SourceHint != "web" && probeConfig.SourceHint != "cache" {
				if ApplyIndexPreflightToProbe(ctx, &probeConfig, taskID, node.ID) {
					if probeConfig.ContextFile != "" {
						defer os.Remove(probeConfig.ContextFile)
					}
				}
			}
		}

		if node.Type == "probe" || probeConfig.SourceHint == "web" {
			fmt.Fprintf(os.Stderr, "[Executor] Node %s (%s): dispatching to native ReAct loop (ADR-0089)\n", node.ID, node.Type)
			reactCfg := ReActConfig{
				Goal:            probeConfig.Goal,
				AllowedTools:    probeConfig.AllowedTools,
				StepBudget:      probeConfig.StepBudget,
				TaskContext:     probeConfig.TaskContext,
				UpstreamContext: probeConfig.UpstreamContext,
			}
			if reactCfg.StepBudget <= 0 {
				reactCfg.StepBudget = 15
			}
			liveInf := NewLiveReActInference()
			reactRes, reactErr := RunReActLoop(probeCtx, reactCfg, liveInf)
			if reactErr != nil {
				return "", fmt.Errorf("react loop failed on node %s: %w", node.ID, reactErr)
			}
			synthesis = reactRes.FinalOutput
		} else if config.GetUsePhaseRunner() {
			switch probeConfig.SourceHint {
			case "cache":
				fmt.Fprintf(os.Stderr, "[Executor] Probe %s: dispatching to AnalyzePhases (SourceHint=cache)\n", node.ID)
				synthesis, err = RunAnalyzePhases(probeCtx, taskID, taskID+"_"+node.ID, probeConfig, probeEngine, synthesisEngine, downstreamBindingKeys)
			default:
				fmt.Fprintf(os.Stderr, "[Executor] Probe %s: dispatching to ProbePhases\n", node.ID)
				synthesis, err = RunProbePhases(probeCtx, taskID, taskID+"_"+node.ID, probeConfig, probeEngine, synthesisEngine, downstreamBindingKeys)
			}
		} else {
			// Legacy flat Thought Chain loop
			synthesis, err = RunProbe(probeCtx, taskID, taskID+"_"+node.ID, probeConfig, probeEngine, synthesisEngine, downstreamBindingKeys)
		}

		if err != nil {
			return "", err
		}

		// Preserve cacheIds for downstream nodes. When a probe reads a CSV,
		// the Data Profiler caches the data and returns a cacheId in the tool
		// result, but the probe's synthesis is prose that strips this.
		// Downstream analyze nodes need the cacheId to query via sql_cached_data.
		//
		// Sources checked:
		// 1. Upstream context (passed to this probe)
		// 2. Ephemeral query DB's _cache_tables (materialized during this task)
		cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
		synthesisCacheIds := cacheIdRe.FindAllString(synthesis, -1)
		synthesisCacheSet := make(map[string]bool)
		for _, id := range synthesisCacheIds {
			synthesisCacheSet[id] = true
		}

		var allDiscovered []string
		seen := make(map[string]bool)

		// Source 1: upstream context
		if probeConfig.UpstreamContext != "" {
			for _, id := range cacheIdRe.FindAllString(probeConfig.UpstreamContext, -1) {
				if !synthesisCacheSet[id] && !seen[id] {
					allDiscovered = append(allDiscovered, id)
					seen[id] = true
				}
			}
		}

		// Source 2: ephemeral query DB — tables materialized recently
		// (the probe may have created them internally via read_file →
		// Data Profiler). Scoped to last 60 seconds to avoid picking up
		// stale tables from prior task runs.
		if qdb := cache.QueryDB(); qdb != nil {
			cutoff := time.Now().Add(-60 * time.Second).Format(time.RFC3339)
			rows, err := qdb.Query("SELECT table_name FROM _cache_tables WHERE created_at > ? ORDER BY created_at DESC LIMIT 3", cutoff)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var tableName string
					if rows.Scan(&tableName) == nil {
						if !synthesisCacheSet[tableName] && !seen[tableName] && cacheIdRe.MatchString(tableName) {
							allDiscovered = append(allDiscovered, tableName)
							seen[tableName] = true
						}
					}
				}
				if err := rows.Err(); err != nil {
					fmt.Fprintf(os.Stderr, "[Executor] Cache table discovery rows error: %v\n", err)
				}
			}
		}

		if len(allDiscovered) > 0 {
			synthesis += "\n\n## Data Cache Reference\n"
			for _, id := range allDiscovered {
				synthesis += fmt.Sprintf("cacheId: %s (use sql_cached_data to query)\n", id)
				synthesis += "dataProfile: available via introspect_cache\n"
			}
			fmt.Fprintf(os.Stderr, "[Executor] Appended %d cacheIds to probe %s synthesis for downstream discovery\n", len(allDiscovered), node.ID)
		}

	return synthesis, nil
}

// isGenericTemplateGoal returns true if the probe goal is a generic template
// default from the plan template registry that the local planner failed to
// customize with the specific task prompt. These vague goals cause off-topic
// exploration and synthesis.
func isGenericTemplateGoal(goal string) bool {
	if goal == "" {
		return true
	}
	// Known template defaults from internal/templates/registry.go
	genericGoals := []string{
		"Explore the target and produce a comprehensive analysis.",
		"Explore the target and produce content for documentation.",
		"Research the topic using web search and browsing.",
		"Explore the first source.",
		"Explore the second source.",
		"Explore the codebase to gather context for code generation.",
	}
	goalLower := strings.ToLower(strings.TrimSpace(goal))
	for _, g := range genericGoals {
		if goalLower == strings.ToLower(g) {
			return true
		}
	}
	// Also detect very short goals that are likely poorly mutated
	if len(goal) < 30 && !strings.ContainsAny(goal, "/\\") {
		return true
	}
	return false
}
