package executor

// analyze_phases.go — Analyze Node phase template for the Phase Runner.
//
// Replaces the flat Thought Chain loop for Analyze Nodes with a structured
// 4-phase pipeline: Schema-Orient → Query-Dev → Compute → Synthesize.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
)

// RunAnalyzePhases executes an Analyze Node using the Phase Runner with the
// Analyze-specific 4-phase template: Schema-Orient → Query-Dev → Compute → Synthesize.
func RunAnalyzePhases(
	ctx context.Context,
	taskID, probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	runner := buildAnalyzePhaseRunner(config)

	results, err := runner.Run(ctx, taskID, probeID, engine, synthesisEngine)
	if err != nil {
		return "", fmt.Errorf("analyze phases failed: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("analyze phases produced no results")
	}

	manifest := runner.BuildManifest(results)
	finalSynthesis := results[len(results)-1].Summary

	fmt.Fprintf(os.Stderr, "[AnalyzePhases] Completed %d phases, %d total steps, %d backtracks\n",
		len(manifest.Phases), manifest.TotalStepsUsed, manifest.TotalBacktracks)

	return finalSynthesis, nil
}

// buildAnalyzePhaseRunner constructs a PhaseRunner with the Analyze-specific template.
func buildAnalyzePhaseRunner(config compiler.ProbeConfig) *PhaseRunner {
	var schemaIntrospected bool
	var analyticalQueries int

	// ADR-0058 port: State for SQL-specific guardrails.
	// Extract known cacheIds from upstream context so we can auto-populate
	// empty introspect_cache and sql_cached_data calls.
	knownCacheIds := extractCacheIdsFromContext(config.TaskContext)
	dispatchedHashes := make(map[string]bool)

	runner := &PhaseRunner{
		ToolFixup: func(phaseName, toolName string, args map[string]interface{}, reasoning string) (string, map[string]interface{}) {
			switch toolName {
			case "introspect_cache":
				// Auto-populate empty cacheId
				cacheId, _ := args["cacheId"].(string)
				if strings.TrimSpace(cacheId) == "" && len(knownCacheIds) > 0 {
					args["cacheId"] = knownCacheIds[0]
					fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: auto-populated introspect_cache cacheId=%q\n", knownCacheIds[0])
				}
			case "sql_cached_data":
				// Auto-populate empty cacheId
				cacheId, _ := args["cacheId"].(string)
				if strings.TrimSpace(cacheId) == "" && len(knownCacheIds) > 0 {
					args["cacheId"] = knownCacheIds[0]
					fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: auto-populated sql_cached_data cacheId=%q\n", knownCacheIds[0])
				}
				// SQL auto-extraction from reasoning text
				sql, _ := args["sql"].(string)
				if strings.TrimSpace(sql) == "" {
					extracted, _ := extractSQLFromText(reasoning)
					if extracted != "" {
						args["sql"] = extracted
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: extracted SQL from reasoning: %q\n", truncate(extracted, 80))
					} else if cacheId, ok := args["cacheId"].(string); ok && cacheId != "" {
						// Generate a default exploratory query
						args["sql"] = defaultSQLForCacheId(cacheId)
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: generated default SQL for cacheId=%q\n", cacheId)
					}
				}
				// Duplicate detection — skip if same args already dispatched
				hash := fmt.Sprintf("%s:%v", toolName, args)
				if dispatchedHashes[hash] {
					fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: skipping duplicate sql_cached_data call\n")
					// Return a no-op tool name to prevent dispatch
					return "noop", args
				}
				dispatchedHashes[hash] = true
			}
			return toolName, args
		},
		ToolPostProcess: func(phaseName, toolName string, args map[string]interface{}, output string, err error) {
			if toolName == "introspect_cache" && err == nil {
				// Extract any additional cacheIds discovered
				discovered := extractCacheIdFromText(output)
				if discovered != "" {
					for _, existing := range knownCacheIds {
						if existing == discovered {
							return
						}
					}
					knownCacheIds = append(knownCacheIds, discovered)
					fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolPostProcess: discovered new cacheId=%q\n", discovered)
				}
			}
		},
		Phases: map[string]*Phase{
			"schema_orient": {
				Name:         "schema_orient",
				AllowedTools: []string{"introspect_cache"},
				SystemPrompt: buildPhaseAnalyzePrompt("schema_orient", config.Goal, config.TaskContext),
				StepBudget:   4,
				Pass1Target:  TargetRouter,
				Recovery: PhaseRecovery{
					MaxRetries:   1, // Allow retry so forced tool call can fire introspect_cache
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					for _, tool := range result.ToolsCalled {
						if tool == "introspect_cache" {
							schemaIntrospected = true
							return "query_dev"
						}
					}
					return ""
				},
			},
			"query_dev": {
				Name:         "query_dev",
				AllowedTools: []string{"sql_cached_data"},
				SystemPrompt: buildPhaseAnalyzePrompt("query_dev", config.Goal, config.TaskContext),
				StepBudget:   6,
				MinToolCalls: 2, // Must execute ≥2 SQL queries before allowing synthesis
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					MaxRetries:   1,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
					BacktrackTo:  "schema_orient",
				},
				Transition: func(step int, result PhaseResult, err error) string {
					for _, tool := range result.ToolsCalled {
						if tool == "sql_cached_data" {
							analyticalQueries++
						}
					}
					if analyticalQueries >= 2 {
						return "compute"
					}
					return ""
				},
			},
			"compute": {
				Name:         "compute",
				AllowedTools: []string{"sql_cached_data"},
				SystemPrompt: buildPhaseAnalyzePrompt("compute", config.Goal, config.TaskContext),
				StepBudget:   4,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					MaxRetries:   0,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					// Terminal condition: all goal questions answered OR budget
					return ""
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{},
				SystemPrompt: buildPhaseAnalyzePrompt("synthesize", config.Goal, config.TaskContext),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionFail,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string { return "" },
			},
		},
		InitialPhase: "schema_orient",
		MaxCycles:    3,
		Goal:         config.Goal,
	}

	// Suppress unused variable warnings
	_ = schemaIntrospected

	return runner
}

// buildPhaseAnalyzePrompt constructs a phase-specific system prompt for Analyze nodes.
func buildPhaseAnalyzePrompt(phase, goal, taskContext string) string {
	var b strings.Builder

	switch phase {
	case "schema_orient":
		b.WriteString("You are analyzing cached data to answer analytical questions. ")
		b.WriteString("PHASE: SCHEMA-ORIENT — introspect the cache to understand data shape and columns. ")
		b.WriteString("Use introspect_cache ONLY. ")
	case "query_dev":
		b.WriteString("You are developing analytical SQL queries against cached data. ")
		b.WriteString("PHASE: QUERY-DEV — write exploratory SQL queries to understand the data. ")
		b.WriteString("Use sql_cached_data ONLY. Start with sampling queries, then move to aggregates. ")
	case "compute":
		b.WriteString("You are computing final analytical results from cached data. ")
		b.WriteString("PHASE: COMPUTE — run targeted analytical queries to answer the goal. ")
		b.WriteString("Use sql_cached_data ONLY. Focus on answering specific questions. ")
	case "synthesize":
		b.WriteString("You are synthesizing analytical findings into a final report. ")
		b.WriteString("PHASE: SYNTHESIZE — produce a comprehensive, data-backed answer. ")
		b.WriteString("You have NO tools. Use the phase results as your evidence. ")
	}

	b.WriteString(fmt.Sprintf("\n\nGoal: %s", goal))
	if taskContext != "" {
		b.WriteString(fmt.Sprintf("\n\nTask Context: %s", taskContext))
	}

	return b.String()
}
