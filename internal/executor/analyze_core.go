package executor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// runAnalyzeCore contains the domain-core logic for analyze nodes.
// Returns (synthesis, error) without managing state, hooks, or events.
// Called by AnalyzeOnlyStrategy.Execute → dispatch envelope handles ceremony.
func (e *ExecutionEngine) runAnalyzeCore(
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
		StepBudget:   20,
		CompactEvery: 3,
	}
	if node.ProbeConfig != nil {
		probeConfig = *node.ProbeConfig
	}

	// Runtime expansion of cache tools for analyze nodes that need to query cached data.
	if (node.Type == "list" || node.Type == "analyze") && !isAnalyzeConfig(probeConfig.AllowedTools) {
		shouldExpand := false

		// (a) Read_file in allowedTools — may encounter tabular data at runtime
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
			fmt.Fprintf(os.Stderr, "[Executor] Expanded analyze allowedTools with cache + compound data tools for %s\n", node.ID)
		}
	}

	if probeConfig.TaskContext == "" && graph.GoalPrompt != "" {
		probeConfig.TaskContext = graph.GoalPrompt
	}

	if graph.GoalPrompt != "" {
		goalIsGeneric := isGenericTemplateGoal(probeConfig.Goal)
		if goalIsGeneric {
			fmt.Fprintf(os.Stderr, "[Executor] Analyze %s: overriding generic goal %q with GoalPrompt (%d chars)\n",
				node.ID, probeConfig.Goal, len(graph.GoalPrompt))
			probeConfig.Goal = graph.GoalPrompt
		}
	}

	// Inject accumulated context from completed upstream nodes
	if probeConfig.UpstreamContext == "" {
		upstreamCtx := buildAccumulatedContext(taskID, graph, node.Type)
		if upstreamCtx != "" {
			if isAnalyzeConfig(node.AllowedTools) {
				upstreamCtx = enrichCacheBridgeContext(ctx, upstreamCtx, node.Instructions)
			}
			probeConfig.UpstreamContext = upstreamCtx
			fmt.Fprintf(os.Stderr, "[Executor] Injected %d chars of upstream context into %s node %s\n", len(upstreamCtx), node.Type, node.ID)
		}
	}

	// Collect binding keys that downstream nodes need from this node's output
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
		fmt.Fprintf(os.Stderr, "[Executor] Analyze %s: downstream binding keys: %v\n", node.ID, downstreamBindingKeys)
	}

	probeEngine := ProbeInferenceEngine(&ProbeInference{})
	synthesisEngine := &ProbeInference{}

	probeCtx := context.WithValue(ctx, DispatchRecorderKey, func(toolName string, args map[string]interface{}) {
		e.RecordDispatch(taskID, toolName, args)
	})

	var synthesis string
	var err error

	if (node.Type == "list" || probeConfig.SourceHint == "web") && !probeConfig.DirectSynthesis {
		fmt.Fprintf(os.Stderr, "[Executor] Node %s (%s): dispatching to native ReAct loop (ADR-0089)\n", node.ID, node.Type)

		leanTaskContext := probeConfig.TaskContext
		const maxReActTaskContextChars = 2000
		if len(leanTaskContext) > maxReActTaskContextChars {
			leanTaskContext = leanTaskContext[:maxReActTaskContextChars] + "\n... (context truncated for interactive ReAct exploration)"
		}

		reactCfg := ReActConfig{
			Goal:            probeConfig.Goal,
			AllowedTools:    probeConfig.AllowedTools,
			StepBudget:      probeConfig.StepBudget,
			TaskContext:     leanTaskContext,
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
		fmt.Fprintf(os.Stderr, "[Executor] Analyze %s: dispatching to AnalyzePhases (SourceHint=cache)\n", node.ID)
		synthesis, err = RunAnalyzePhases(probeCtx, taskID, taskID+"_"+node.ID, probeConfig, probeEngine, synthesisEngine, downstreamBindingKeys)
	} else {
		// Fallback to RunAnalyzePhases
		synthesis, err = RunAnalyzePhases(probeCtx, taskID, taskID+"_"+node.ID, probeConfig, probeEngine, synthesisEngine, downstreamBindingKeys)
	}

	if err != nil {
		return "", err
	}

	// Preserve cacheIds for downstream nodes
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

	// Source 2: ephemeral query DB
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
		fmt.Fprintf(os.Stderr, "[Executor] Appended %d cacheIds to analyze %s synthesis for downstream discovery\n", len(allDiscovered), node.ID)
	}

	return synthesis, nil
}

// isGenericTemplateGoal returns true if the goal is a generic template
// default from the plan template registry that the local planner failed to customize.
func isGenericTemplateGoal(goal string) bool {
	if goal == "" {
		return true
	}
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
	if len(goal) < 30 && !strings.ContainsAny(goal, "/\\") {
		return true
	}
	return false
}
