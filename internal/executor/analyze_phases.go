package executor

// analyze_phases.go — Analyze Node phase template for the Phase Runner.
//
// ADR-0074: Structured Query Composition — replaces raw sql_cached_data
// generation with query_builder, a composable tool the 4B model handles
// reliably via structured parameter extraction.
//
// v3 2-phase pipeline: Schema-Orient → Query (+ raw data passthrough).
// The synthesize phase was removed because the 4B model corrupts
// structured data into bad prose (FM-21 hangs, VTE rejections,
// stochastic variance). Query results pass through directly.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	configPkg "tzro/internal/config"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// RunAnalyzePhases executes an Analyze Node using the Phase Runner with the
// Analyze-specific 2-phase template: Schema-Orient → Query.
// The synthesize phase is skipped — raw query results pass through directly
// to avoid FM-21 hangs and stochastic synthesis corruption.
func RunAnalyzePhases(
	ctx context.Context,
	taskID, probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	runner, resolvedKeyColumns := buildAnalyzePhaseRunner(config)

	results, err := runner.Run(ctx, taskID, probeID, engine, synthesisEngine)
	if err != nil {
		return "", fmt.Errorf("analyze phases failed: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("analyze phases produced no results")
	}

	// Persist resolved key columns for downstream evidence pruning
	if len(*resolvedKeyColumns) > 0 {
		if colJSON, err := json.Marshal(*resolvedKeyColumns); err == nil {
			if dbErr := memory.DB.SetNodeKeyColumns(taskID, probeID, string(colJSON)); dbErr != nil {
				fmt.Fprintf(os.Stderr, "[AnalyzePhases] Failed to persist key columns: %v\n", dbErr)
			} else {
				fmt.Fprintf(os.Stderr, "[AnalyzePhases] Persisted key columns: %v\n", *resolvedKeyColumns)
			}
		}
	}

	manifest := runner.BuildManifest(results)

	fmt.Fprintf(os.Stderr, "[AnalyzePhases] Completed %d phases, %d total steps, %d backtracks\n",
		len(manifest.Phases), manifest.TotalStepsUsed, manifest.TotalBacktracks)

	// Build raw data passthrough instead of using LLM prose synthesis.
	// The query phase's tool outputs contain the actual answer — passing
	// them through directly eliminates FM-21 hangs and VTE rejections.
	finalOutput := buildDataPassthrough(config.Goal, results, probeID)

	fmt.Fprintf(os.Stderr, "[AnalyzePhases] Data passthrough: %d chars (no LLM synthesis)\n", len(finalOutput))
	return finalOutput, nil
}

// buildDataPassthrough constructs a structured output from the goal and raw
// query results, bypassing LLM prose synthesis entirely. The VTE/cloud model
// handles formatting if needed.
func buildDataPassthrough(goal string, results []PhaseResult, probeID string) string {
	var sb strings.Builder

	sb.WriteString("## Goal\n")
	sb.WriteString(goal)
	sb.WriteString("\n\n")

	// Extract raw tool outputs from the query phase results.
	// The last query_builder/sql_cached_data output is the actual answer.
	var lastQueryOutput string
	var cacheIds []string
	cacheIdRe := regexp.MustCompile(`cache_[a-z0-9_]{10,}`)

	// Pull from persisted ThoughtSteps — these have the full raw output
	// that isn't truncated by the phase runner's toolOutputLog.
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err == nil && len(steps) > 0 {
		for _, s := range steps {
			if s.ToolOutput == "" || strings.HasPrefix(s.ToolOutput, "Error:") {
				continue
			}
			// Track cache IDs for reference
			for _, id := range cacheIdRe.FindAllString(s.ToolOutput, -1) {
				cacheIds = append(cacheIds, id)
			}
			if s.ToolName == "query_builder" || s.ToolName == "sql_cached_data" {
				lastQueryOutput = s.ToolOutput
			}
		}
	}

	if lastQueryOutput != "" {
		sb.WriteString("## Query Result\n")
		sb.WriteString(lastQueryOutput)
		sb.WriteString("\n\n")
	} else {
		// Fallback: use the last phase's Summary (LLM-generated, from phase transition)
		if len(results) > 0 {
			sb.WriteString("## Analysis Result\n")
			sb.WriteString(results[len(results)-1].Summary)
			sb.WriteString("\n\n")
		}
	}

	if len(cacheIds) > 0 {
		sb.WriteString("## Cache Reference\n")
		seen := make(map[string]bool)
		for _, id := range cacheIds {
			if !seen[id] {
				sb.WriteString(fmt.Sprintf("cacheId: %s\n", id))
				seen[id] = true
			}
		}
	}

	return sb.String()
}

// buildAnalyzePhaseRunner constructs a PhaseRunner with the Analyze-specific
// 3-phase v2 template: schema_orient → query → synthesize.
// tabularPathRe extracts file paths ending in tabular extensions from text.
// Captures paths like "/path/to/file.csv", "helpers/LeadSuccess.csv", etc.
var tabularPathRe = regexp.MustCompile(`(?:^|\s|['"\'\(])([\w./_-]*\.(?:csv|tsv|xlsx|xls))(?:[\s'"\'\),]|$)`)

// extractTabularPaths returns all tabular file paths found in text.
func extractTabularPaths(text string) []string {
	matches := tabularPathRe.FindAllStringSubmatch(text, -1)
	var paths []string
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			paths = append(paths, m[1])
		}
	}
	return paths
}

// autoIngestTabularFile profiles a tabular file and stores it in the cache,
// returning the cacheId. This is the runtime equivalent of the read_file →
// cache_bridge pipeline path, used when probe→analyze conversion skips the
// read_file action node.
func autoIngestTabularFile(ctx context.Context, filePath string) (string, error) {
	profile, err := tools.ProfileTabularFile(filePath)
	if err != nil {
		return "", fmt.Errorf("auto-ingest profile failed: %w", err)
	}
	envelopeJSON, _ := json.MarshalIndent(profile, "", "  ")
	cacheID, storeErr := cache.DefaultStore.StoreFileRef(ctx, filePath, string(envelopeJSON))
	if storeErr != nil {
		return "", fmt.Errorf("auto-ingest store failed: %w", storeErr)
	}
	return cacheID, nil
}

func buildAnalyzePhaseRunner(config compiler.ProbeConfig) (*PhaseRunner, *[]string) {
	var schemaIntrospected bool
	var queryBuilderCalls int
	var keyColumns []string // Populated by embedding override in schema_orient transition

	// ADR-0058 port: State for cache ID guardrails.
	// Extract known cacheIds from upstream context so we can auto-populate
	// empty introspect_cache and query_builder calls.
	// Must scan BOTH TaskContext (goal prompt) and UpstreamContext (cache bridge
	// enrichment) — the cacheId lives in UpstreamContext, not TaskContext.
	knownCacheIds := extractCacheIdsFromContext(config.TaskContext + "\n" + config.UpstreamContext)

	// Red-team FM-1 fix: When knownCacheIds is empty and the goal references a
	// tabular file, auto-ingest the file into the cache. This handles the
	// probe→analyze conversion path where no read_file action node exists
	// upstream to pre-load data into the cache.
	if len(knownCacheIds) == 0 {
		combinedText := config.Goal + "\n" + config.TaskContext + "\n" + config.UpstreamContext
		tabularPaths := extractTabularPaths(combinedText)
		for _, relPath := range tabularPaths {
			// Resolve relative paths against known working directories
			absPath := relPath
			if !filepath.IsAbs(relPath) {
				// Try common base directories from config
				candidates := []string{relPath}
				if wd, err := os.Getwd(); err == nil {
					candidates = append(candidates, filepath.Join(wd, relPath))
				}
				for _, c := range candidates {
					if _, err := os.Stat(c); err == nil {
						absPath = c
						break
					}
				}
			}
			if _, err := os.Stat(absPath); err != nil {
				fmt.Fprintf(os.Stderr, "[AnalyzePhases] Auto-ingest: file not found at %q (skipping)\n", absPath)
				continue
			}
			cacheID, ingestErr := autoIngestTabularFile(context.Background(), absPath)
			if ingestErr != nil {
				fmt.Fprintf(os.Stderr, "[AnalyzePhases] Auto-ingest failed for %q: %v\n", absPath, ingestErr)
				continue
			}
			knownCacheIds = append(knownCacheIds, cacheID)
			fmt.Fprintf(os.Stderr, "[AnalyzePhases] Auto-ingested %q → cacheId=%q (FM-1 fix)\n", absPath, cacheID)
			break // One file is enough — the goal typically references a single dataset
		}
	}

	dispatchedHashes := make(map[string]bool)

	// QueryIntent GBNF extraction: pre-built operations from goal analysis.
	// Populated after schema_orient completes (when we have column names).
	var preBuiltOps []interface{}
	var sampleValues map[string][]string

	runner := &PhaseRunner{
		ToolFixup: func(phaseName, toolName string, args map[string]interface{}, reasoning string) (string, map[string]interface{}) {
			switch toolName {
			case "introspect_cache":
				// Auto-populate empty or hallucinated cacheId (Red-team FM-6 fix)
				cacheId, _ := args["cacheId"].(string)
				if len(knownCacheIds) > 0 && !isKnownCacheId(cacheId, knownCacheIds) {
					if strings.TrimSpace(cacheId) != "" {
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: rejected hallucinated cacheId=%q, replacing with %q\n", cacheId, knownCacheIds[0])
					} else {
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: auto-populated introspect_cache cacheId=%q\n", knownCacheIds[0])
					}
					args["cacheId"] = knownCacheIds[0]
				}
			case "query_builder":
				// Auto-populate empty or hallucinated cacheId (Red-team FM-6 fix)
				cacheId, _ := args["cacheId"].(string)
				if len(knownCacheIds) > 0 && !isKnownCacheId(cacheId, knownCacheIds) {
					if strings.TrimSpace(cacheId) != "" {
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: rejected hallucinated cacheId=%q, replacing with %q\n", cacheId, knownCacheIds[0])
					} else {
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: auto-populated query_builder cacheId=%q\n", knownCacheIds[0])
					}
					args["cacheId"] = knownCacheIds[0]
				}
				// QueryIntent fallback: When the model produces empty operations,
				// use the pre-built ops from GBNF intent extraction.
				ops, _ := args["operations"].([]interface{})
				if len(ops) == 0 && len(preBuiltOps) > 0 {
					args["operations"] = preBuiltOps
					fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: injected %d pre-built ops from QueryIntent extraction\n", len(preBuiltOps))
				} else if len(ops) == 0 {
					// Last resort: regex-based filter extraction from goal text
					defaultOps := extractFilterFromGoal(config.Goal)
					if len(defaultOps) > 0 {
						args["operations"] = defaultOps
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: injected goal-derived filter operations (regex fallback)\n")
					} else {
						args["operations"] = []interface{}{
							map[string]interface{}{"type": "select", "columns": []string{"*"}},
						}
						args["limit"] = 20
						fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: injected default SELECT * LIMIT 20 (no intent available)\n")
					}
				}
				// Duplicate detection — skip if same args already dispatched
				hash := fmt.Sprintf("%s:%v", toolName, args)
				if dispatchedHashes[hash] {
					fmt.Fprintf(os.Stderr, "[AnalyzePhases] ToolFixup: skipping duplicate query_builder call\n")
					return "noop", args
				}
				dispatchedHashes[hash] = true
			case "sql_cached_data":
				// Legacy fallback: if the model emits sql_cached_data, auto-populate cacheId
				// and let it through (backward compat during transition).
				cacheId, _ := args["cacheId"].(string)
				if len(knownCacheIds) > 0 && !isKnownCacheId(cacheId, knownCacheIds) {
					args["cacheId"] = knownCacheIds[0]
				}
				sql, _ := args["sql"].(string)
				if strings.TrimSpace(sql) == "" {
					extracted, _ := extractSQLFromText(reasoning)
					if extracted != "" {
						args["sql"] = extracted
					} else if cacheId, ok := args["cacheId"].(string); ok && cacheId != "" {
						args["sql"] = defaultSQLForCacheId(cacheId)
					}
				}
				hash := fmt.Sprintf("%s:%v", toolName, args)
				if dispatchedHashes[hash] {
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
				SystemPrompt: buildPhaseAnalyzePrompt("schema_orient", config.Goal, config.TaskContext, nil, nil),
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

							// QueryIntent GBNF extraction: run between schema_orient and query.
							// Now that we have column names (from introspect_cache), extract
							// structured query intent from the goal using a fast GBNF pass.
							fmt.Fprintf(os.Stderr, "[AnalyzePhases] schema_orient→query transition: knownCacheIds=%v\n", knownCacheIds)
							if len(knownCacheIds) > 0 {
								// Use TaskContext (original user prompt) for better
								// extraction — it has the actual column/value terms.
								intentGoal := config.TaskContext
								if intentGoal == "" {
									intentGoal = config.Goal
								}
								intent, intentErr := ExtractQueryIntent(
									context.Background(),
									intentGoal,
									knownCacheIds[0],
								)
								if intentErr != nil {
									fmt.Fprintf(os.Stderr, "[AnalyzePhases] QueryIntent extraction failed (non-fatal): %v\n", intentErr)
								} else {
									preBuiltOps = IntentToOperations(intent)
									fmt.Fprintf(os.Stderr, "[AnalyzePhases] QueryIntent extracted %d operations as fallback\n", len(preBuiltOps))
								}

								// Red-team FM-10 fix: Collect sample values per column so the
								// query phase prompt includes real data values for filter matching.
								columns := cache.GetCacheColumns(knownCacheIds[0])
								if len(columns) > 0 {
									sampleValues = cache.GetCacheSampleValues(knownCacheIds[0], columns, 15)
									fmt.Fprintf(os.Stderr, "[AnalyzePhases] Collected sample values for %d columns\n", len(sampleValues))
								}

								// ADR-0075: Embedding-based select column override.
								// The 4B model's GBNF extraction of selectColumns is unreliable
								// (e.g., extracts [name, AccountId, Status] instead of [name, email]).
								// Neural embeddings match the goal text against enriched column strings
								// to deterministically select the right output columns.
								//
								// IMPORTANT: Use config.TaskContext (the original user prompt) rather
								// than config.Goal (which wraps it with "Analyze the cached data to
								// answer:"). The TaskContext contains the actual analytical terms
								// (e.g., "name", "email", "country") needed for accurate embedding.
								if intent != nil {
									embGoal := config.TaskContext
									if embGoal == "" {
										embGoal = config.Goal // Fallback
									}
									embCols := ResolveSelectColumns(
										context.Background(),
										embGoal,
										knownCacheIds[0],
										sampleValues,
										configPkg.GetColumnScoreThreshold(),
									)
									if len(embCols) > 0 {
										intent.SelectColumns = embCols
										preBuiltOps = IntentToOperations(intent)
										keyColumns = embCols // Capture for downstream persistence
										fmt.Fprintf(os.Stderr, "[AnalyzePhases] Embedding override: selectColumns=%v\n", embCols)
									}
								}
							}

							return "query"
						}
					}
					return ""
				},
			},
			"query": {
				Name:         "query",
				AllowedTools: []string{"query_builder", "sql_cached_data"},
				SystemPrompt: buildPhaseAnalyzePrompt("query", config.Goal, config.TaskContext, knownCacheIds, sampleValues),
				StepBudget:   6,
				MinToolCalls: 1, // Must execute ≥1 query before allowing synthesis
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					MaxRetries:   1,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
					BacktrackTo:  "schema_orient",
				},
				Transition: func(step int, result PhaseResult, err error) string {
					for _, tool := range result.ToolsCalled {
						if tool == "query_builder" || tool == "sql_cached_data" {
							queryBuilderCalls++
						}
					}
					if queryBuilderCalls >= 1 {
						// v3: skip synthesize phase — raw data passthrough.
						// Return "" (terminal) instead of "synthesize".
						return ""
					}
					return ""
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{},
				SystemPrompt: buildPhaseAnalyzePrompt("synthesize", config.Goal, config.TaskContext, nil, nil),
				StepBudget:   2,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionSkip,
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

	return runner, &keyColumns
}

// analyzePromptOpts holds optional parameters for buildPhaseAnalyzePrompt.
type analyzePromptOpts struct {
	knownCacheIds []string
	sampleValues  map[string][]string
}

// buildPhaseAnalyzePrompt constructs a phase-specific system prompt for Analyze nodes.
// The optional params support cache ID anchoring (FM-6 fix) and sample value injection (FM-10 fix).
func buildPhaseAnalyzePrompt(phase, goal, taskContext string, knownCacheIds []string, sampleValues map[string][]string) string {
	var b strings.Builder

	// Extract first known cacheId for prompt anchoring
	var primaryCacheId string
	if len(knownCacheIds) > 0 {
		primaryCacheId = knownCacheIds[0]
	}

	switch phase {
	case "schema_orient":
		b.WriteString("You are analyzing cached data to answer analytical questions. ")
		b.WriteString("PHASE: SCHEMA-ORIENT — introspect the cache to understand data shape and columns. ")
		b.WriteString("Use introspect_cache ONLY. ")
	case "query":
		b.WriteString("You are querying cached data to answer analytical questions. ")
		b.WriteString("PHASE: QUERY — build composable queries to retrieve the data needed to answer the goal. ")
		b.WriteString("Use query_builder to construct queries. The query_builder tool accepts structured operations: ")
		b.WriteString("filter (WHERE), group_by (GROUP BY), aggregate (COUNT/SUM/AVG/MIN/MAX/GROUP_CONCAT), ")
		b.WriteString("order_by (ORDER BY), and select (specific columns). ")
		b.WriteString("You MUST use EXACT column names from the schema. Compose operations to answer the goal. ")
		b.WriteString("Example: to count leads by country, use operations: [{type:\"group_by\", column:\"Country\"}, ")
		b.WriteString("{type:\"aggregate\", function:\"COUNT\", alias:\"count\"}, {type:\"order_by\", column:\"count\", direction:\"DESC\"}]. ")
		// Red-team FM-6 fix: Anchor the cacheId directly in the prompt so the model
		// doesn't need to discover or remember it — prevents hallucinated cacheIds.
		if primaryCacheId != "" {
			b.WriteString(fmt.Sprintf("\nIMPORTANT: The data cache ID is: %s. Use this EXACT value for the cacheId parameter in ALL tool calls. Do NOT invent or guess cache IDs.", primaryCacheId))
		}
		// Red-team FM-10 fix: Inject sample values per column so the model can
		// use exact data values for filter matching instead of guessing.
		if len(sampleValues) > 0 {
			b.WriteString("\n\nSample values per column (use these EXACT values for filters):")
			for col, vals := range sampleValues {
				if len(vals) > 10 {
					vals = vals[:10] // Keep prompt compact
				}
				b.WriteString(fmt.Sprintf("\n  %s: %v", col, vals))
			}
		}
	case "synthesize":
		b.WriteString("You are synthesizing analytical findings into a final report. ")
		b.WriteString("PHASE: SYNTHESIZE — produce a comprehensive, data-backed answer. ")
		b.WriteString("You have NO tools. Use the query results as your evidence. ")
	}

	b.WriteString(fmt.Sprintf("\n\nGoal: %s", goal))
	if taskContext != "" {
		b.WriteString(fmt.Sprintf("\n\nTask Context: %s", taskContext))
	}

	return b.String()
}

// isKnownCacheId checks whether a cacheId is in the set of known cache IDs.
// Returns false for empty strings and hallucinated values like "abc123".
// Red-team FM-6 fix: prevents the model from using fabricated cacheIds.
func isKnownCacheId(cacheId string, known []string) bool {
	if strings.TrimSpace(cacheId) == "" {
		return false
	}
	for _, k := range known {
		if k == cacheId {
			return true
		}
	}
	return false
}

// extractFilterFromGoal parses the goal text for common filter patterns and
// returns query_builder operations that would implement that filter.
// Red-team FM-13 fix: ensures forced query_builder calls include filters
// derived from the goal, rather than returning unfiltered data.
//
// Supported patterns:
//   - "where COLUMN is 'VALUE'" / "where COLUMN is \"VALUE\""
//   - "COLUMN = 'VALUE'" / "COLUMN = \"VALUE\""
//   - "COLUMN equals 'VALUE'" / "COLUMN equals \"VALUE\""
//   - "filter ... where COLUMN ... 'VALUE'"
func extractFilterFromGoal(goal string) []interface{} {
	// Pattern: where/filter COLUMN is/=/equals 'VALUE' or "VALUE"
	patterns := []string{
		`(?i)(?:where|filter)\s+(?:the\s+)?(\w+)\s+(?:is|=|equals)\s+['""]([^'""]+)['""]`,
		`(?i)(\w+)\s+=\s+['""]([^'""]+)['""]`,
		`(?i)(\w+)\s+equals\s+['""]([^'""]+)['""]`,
	}

	for _, pat := range patterns {
		re := regexp.MustCompile(pat)
		matches := re.FindStringSubmatch(goal)
		if len(matches) >= 3 {
			column := matches[1]
			value := matches[2]
			// Skip common SQL keywords that might be falsely matched
			lower := strings.ToLower(column)
			if lower == "where" || lower == "the" || lower == "and" || lower == "or" || lower == "is" {
				continue
			}
			return []interface{}{
				map[string]interface{}{
					"type":     "filter",
					"column":   column,
					"operator": "=",
					"value":    value,
				},
			}
		}
	}
	return nil
}
