package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	cfgpkg "tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// hybridSynthesisThreshold returns the configured context size (in chars) above
// which synthesis uses a two-phase approach: local outline + cloud polish.
// Reads from config.json ("hybridSynthesisThresholdChars"), defaults to 50000.
func hybridSynthesisThreshold() int {
	return cfgpkg.GetHybridSynthesisThresholdChars()
}

func runSynthesisPass(ctx context.Context, probeID, taskID, goal, taskContext string, engine ProbeInferenceEngine, bindingKeys []string, edgeEntries []EdgeEntry, preloadedContent string, isAnalyze bool) (string, error) {
	// ADR-0059: Synthesis reads from the Edge Entry log accumulated during the
	// Thought Chain loop. No more reading compaction summaries or raw thought
	// steps from SQLite — the edge log is the authoritative exploration record.
	fmt.Fprintf(os.Stderr, "[Probe] Synthesis Pass: probeID=%s, edgeEntries=%d\n", probeID, len(edgeEntries))

	var contextStr string

	// Inject pre-loaded source material directly into synthesis context.
	// This bypasses the compaction pipeline that would otherwise destroy the content.
	// The pre-loaded content is the GROUND TRUTH source data; the edge entries
	// provide the probe's exploration findings on top of it.
	if preloadedContent != "" {
		// Budget: use at most 16K chars of preloaded content for synthesis
		// to avoid overwhelming the local model's context window.
		const maxPreloadInSynthesis = 16384
		if len(preloadedContent) > maxPreloadInSynthesis {
			preloadedContent = preloadedContent[:maxPreloadInSynthesis] + "\n[... truncated ...]"
		}
		contextStr += "## Source Material (pre-loaded)\n" + preloadedContent + "\n\n"
	}

	// ADR-0059: For Analyze Nodes, also read sql_cached_data results from SQLite
	// ThoughtSteps as a high-fidelity supplement. The edge log has truncated snippets;
	// the raw ThoughtSteps have full query results that are critical for data analysis.
	if isAnalyze {
		steps, _ := memory.DB.GetThoughtSteps(probeID)
		const maxQueryResultsInSynthesis = 12288
		var queryResultsBuf strings.Builder
		for _, s := range steps {
			if (s.ToolName == "sql_cached_data" || s.ToolName == "introspect_cache") && s.ToolOutput != "" {
				// Skip error outputs
				if strings.HasPrefix(s.ToolOutput, "Error:") || strings.HasPrefix(s.ToolOutput, "error:") {
					continue
				}
				queryResultsBuf.WriteString(fmt.Sprintf("### Step %d: %s\nArgs: %s\nResult:\n%s\n\n", s.StepIndex, s.ToolName, s.ToolArgs, s.ToolOutput))
			}
		}
		if queryResultsBuf.Len() > 0 {
			qr := queryResultsBuf.String()
			if len(qr) > maxQueryResultsInSynthesis {
				qr = qr[:maxQueryResultsInSynthesis] + "\n[... query results truncated ...]\n"
			}
			contextStr += "## Query Results (compaction-exempt)\n" + qr + "\n"
			fmt.Fprintf(os.Stderr, "[Probe] Injected %d chars of sql_cached_data/introspect_cache results into synthesis context\n", len(qr))
		}
	}

	// ADR-0059: Compile the Edge Entry log for synthesis.
	// CompileEdgeLog returns the formatted exploration log and whether overflow was detected.
	edgeLog, overflow := CompileEdgeLog(edgeEntries)
	if overflow {
		fmt.Fprintf(os.Stderr, "[Probe] Edge log overflow detected (%d entries, %d chars) — truncated oldest entries\n", len(edgeEntries), len(edgeLog))
	}
	contextStr += edgeLog

	// Build the synthesis schema dynamically. When downstream nodes declare
	// dynamic bindings referencing this probe's output (e.g., "probe_id.output.handler_file_path"),
	// we extend the schema with those keys as required string fields. This ensures
	// the GBNF grammar forces the local model to produce structured key-value pairs
	// that the Response Resolver can extract deterministically (Tier 1: recursive_key)
	// instead of falling through to the lossy semantic fallback.
	synthSchema, extractionHint := buildSynthesisSchema(bindingKeys)

	// Fix 1 (Cluster 3): Pin TaskContext into synthesis prompt so the model sees
	// the full task specification at synthesis time, not just the short goal string.
	// After 30+ exploration steps, the goal alone is too vague to produce specific output.
	var taskReqSection string
	if taskContext != "" {
		taskReqSection = fmt.Sprintf("\n## Task Requirements (PRIORITY — your response MUST satisfy these)\n%s\n", taskContext)
	}
	systemPrompt := fmt.Sprintf(`You are the Synthesis Engine for a Probe Node.
%sYour goal was: %s

You have completed your exploration. Review the findings and produce a comprehensive, structured final answer.%s`, taskReqSection, goal, extractionHint)

	// Synthesis needs more output tokens than regular probe steps.
	// The default 2048 truncates content-heavy outputs (e.g., ADR logs).
	synthCtx := context.WithValue(ctx, inference.MaxTokensKey, 4096)

	// Fix 3 (Cluster 3): Override RepetitionGuard to prose mode for synthesis.
	// The probe loop correctly uses ContentModeCode for read_file outputs, but
	// synthesis output is markdown/prose. The code-mode guard (0.20 threshold)
	// was causing false-positive aborts on markdown tables and structured content.
	// The guard auto-detects tabular content and promotes to ContentModeTabular
	// if CSV/TSV/markdown-table patterns are found during generation.
	synthCtx = context.WithValue(synthCtx, inference.GenerationGuardKey,
		inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

	// Temperature 0.6 for synthesis: sharper distribution reduces repetitive
	// phrasing while min_p 0.1 still provides dynamic token pruning.
	synthCtx = context.WithValue(synthCtx, inference.TemperatureKey, 0.6)

	// DRY (Don't Repeat Yourself) sampling for synthesis: the 4B model reliably
	// degenerates into repetitive phrase loops during synthesis (benchmark runs
	// 10-11: 4-5/5 tasks hit repetitive content detection, e.g., "Consider Using
	// a Security Toolchain" ×115). DRY is sequence-aware — it detects repeated
	// multi-token sequences and applies exponential penalties based on match
	// length. This directly targets phrase-level repetition without degrading
	// code quality or structured output the way frequency_penalty would.
	// Values: multiplier=0.8 (community default), base=1.75, allowed_length=2,
	// full-context lookback (-1), markdown-aware sequence breakers.
	synthCtx = context.WithValue(synthCtx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
		Multiplier:       0.8,
		Base:             1.75,
		AllowedLength:    2,
		PenaltyLastN:     -1,
		SequenceBreakers: []string{"\n", ":", "\"", "*"},
	})

	// P1: Hybrid Synthesis — when context is large, local synthesis reliably
	// fails with repetitive content (benchmark run 8: 100% failure rate).
	// Use a two-phase approach: local model generates a structured outline,
	// cloud model expands it into polished prose.
	if len(contextStr) > hybridSynthesisThreshold() && !isCloudEscalationBlocked() {
		fmt.Fprintf(os.Stderr, "[Probe] Hybrid synthesis triggered: context=%d chars exceeds threshold=%d\n", len(contextStr), hybridSynthesisThreshold())

		// Phase 1: Local outline — the local model is good at organizing
		// and extracting facts, even from large contexts.
		outlinePrompt := fmt.Sprintf(`You are a structured note-taker.
%sYour goal was: %s

Given the exploration findings below, produce a CONCISE STRUCTURED OUTLINE with:
- Section headers for each major topic
- Key bullet points with specific data values, names, and numbers
- Source URLs where available
- NO prose paragraphs — bullet points ONLY
- Include ALL relevant facts discovered during exploration`, taskReqSection, goal)

		outline, outlineErr := engine.Infer(synthCtx, outlinePrompt, contextStr, "", TargetWorker)
		if outlineErr == nil && len(strings.TrimSpace(outline)) > 100 {
			fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 1 (local outline): %d chars\n", len(outline))

			// Phase 2: Cloud expansion — cloud model expands the outline
			// into polished prose. Low token cost (~500-1K tokens).
			expandPrompt := fmt.Sprintf(`You are the Synthesis Engine for a Probe Node.
%sGoal: %s

Expand the structured outline below into a comprehensive, well-cited final answer.
Preserve all data values, names, and numbers from the outline.
Add proper prose transitions and paragraph structure.
Include source citations where the outline references URLs.%s`, taskReqSection, goal, extractionHint)

			cloudResult, cloudErr := retryWithCloud(ctx, []inference.InferenceMessage{
				{Role: "system", Content: expandPrompt},
				{Role: "user", Content: outline},
			}, synthSchema, taskID)

			if cloudErr == nil && validateSynthesisOutput(cloudResult) == "" {
				fmt.Fprintf(os.Stderr, "[Probe] Hybrid synthesis succeeded: outline=%d chars, expansion=%d chars\n", len(outline), len(cloudResult))
				result := stripControlTokens(cloudResult)
				if len(bindingKeys) > 0 {
					var check map[string]interface{}
					if json.Unmarshal([]byte(result), &check) == nil {
						return result, nil
					}
				}
				var parsed struct {
					Synthesis string `json:"synthesis"`
				}
				if err := json.Unmarshal([]byte(result), &parsed); err != nil {
					return result, nil
				}
				return parsed.Synthesis, nil
			}
			fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 2 (cloud expansion) failed or invalid, falling through to standard synthesis\n")
		} else {
			if outlineErr != nil {
				fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 1 (local outline) failed: %v, falling through to standard synthesis\n", outlineErr)
			} else {
				fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 1 produced degenerate outline (%d chars), falling through to standard synthesis\n", len(strings.TrimSpace(outline)))
			}
		}
	}

	// Build pruned edge evidence for cloud escalation. This uses FullResult
	// (uncompacted web search JSON, full browse content) instead of the truncated
	// ResultSnippets in contextStr. Injected as supplementary context so the cloud
	// model has actual factual data to prevent hallucination.
	const cloudEvidenceBudgetChars = 12288 // ~3K tokens, fits comfortably in cloud context
	prunedEvidence := PruneEdgeContext(edgeEntries, cloudEvidenceBudgetChars)

	// Helper: build cloud retry messages with optional pruned evidence injection.
	buildCloudMessages := func() []inference.InferenceMessage {
		msgs := []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: contextStr},
		}
		if prunedEvidence != "" {
			msgs = append(msgs, inference.InferenceMessage{
				Role:    "user",
				Content: "IMPORTANT: The following is the full exploration evidence from the probe. Use ONLY the facts, URLs, and data below. Do NOT invent or hallucinate any details not present in this evidence.\n\n" + prunedEvidence,
			})
			fmt.Fprintf(os.Stderr, "[Probe] Injecting %d chars of pruned edge evidence into cloud escalation\n", len(prunedEvidence))
		}
		return msgs
	}

	// Standard synthesis path (local-try → cloud-fallback on repetition)
	var result string
	var err error
	result, err = engine.Infer(synthCtx, systemPrompt, contextStr, synthSchema, TargetWorker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Primary synthesis engine failed: %v. Attempting cloud escalation.\n", err)
		if !isCloudEscalationBlocked() {
			cloudResult, cloudErr := retryWithCloud(ctx, buildCloudMessages(), synthSchema, taskID)
			if cloudErr == nil {
				fmt.Fprintf(os.Stderr, "[Probe] Cloud escalation succeeded for synthesis after engine failure (%d chars)\n", len(cloudResult))
				result = cloudResult
			} else {
				return "Synthesis inference failed (primary: " + err.Error() + "; cloud fallback: " + cloudErr.Error() + ")", nil
			}
		} else {
			return "Synthesis inference failed: " + err.Error(), nil
		}
	}

	// Fix 3 (Synthesis Generation Guard): Validate probe synthesis output.
	// Detect control token leaks, degenerate output, and repetitive content.
	// Re-attempt with cloud model on failure (same pattern as ConfidenceTier escalation).
	// ADR-0058: Analyze Node synthesis is exempt from the repetition detector.
	var validationOpts []ValidationOption
	if isAnalyze {
		validationOpts = append(validationOpts, WithAnalyzeNode())
	}
	reason := validateSynthesisOutput(result, validationOpts...)
	if reason != "" {
		fmt.Fprintf(os.Stderr, "[Probe] Synthesis output invalid (%s), escalating to cloud\n", reason)
		if !isCloudEscalationBlocked() {
			cloudResult, cloudErr := retryWithCloud(ctx, buildCloudMessages(), synthSchema, taskID)
		if cloudErr == nil && validateSynthesisOutput(cloudResult, validationOpts...) == "" {
				fmt.Fprintf(os.Stderr, "[Probe] Cloud escalation succeeded for synthesis (%d chars)\n", len(cloudResult))
				result = cloudResult
				// Record escalation as a thought step so downstream Recall nodes
				// can detect that local synthesis was insufficient and default to cloud.
				escalationStep := memory.ThoughtStep{
					ID:        fmt.Sprintf("%s_synthesis_escalation", probeID),
					ProbeID:   probeID,
					TaskID:    taskID,
					StepIndex: -1, // sentinel: not a regular step
					ToolName:  "_cloud_synthesis_escalation",
					CreatedAt: time.Now().Unix(),
				}
				_ = memory.DB.AddThoughtStep(escalationStep)
			}
		}
	}

	// Strip control tokens from the result before downstream processing
	result = stripControlTokens(result)

	// Return the full JSON result so the Response Resolver can parse binding keys
	// directly from the JSON structure via recursive_key search (Tier 1).
	// Previously we extracted only the "synthesis" string field, which discarded
	// all structured binding keys and forced downstream resolution through the
	// lossy semantic fallback (Tier 3).
	if len(bindingKeys) > 0 {
		// Validate the JSON is parseable before returning it raw
		var check map[string]interface{}
		if json.Unmarshal([]byte(result), &check) == nil {
			return result, nil
		}
	}

	// No binding keys or JSON parse failed — extract the synthesis field
	var parsed struct {
		Synthesis string `json:"synthesis"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil // fallback to raw string if parsing fails
	}
	return parsed.Synthesis, nil
}

// buildSynthesisSchema constructs the GBNF-constrained JSON schema for probe synthesis.
// When bindingKeys is non-empty, the schema is extended with those keys as required
// string fields. Returns the schema string and an extraction hint for the system prompt.
func buildSynthesisSchema(bindingKeys []string) (string, string) {
	if len(bindingKeys) == 0 {
		schema := `{
		"type": "object",
		"properties": {
			"synthesis": { "type": "string" }
		},
		"required": ["synthesis"]
	}`
		return schema, ""
	}

	// Build dynamic schema with binding keys
	properties := `"synthesis": { "type": "string" }`
	required := `"synthesis"`
	var keyList string
	for i, key := range bindingKeys {
		properties += fmt.Sprintf(`, "%s": { "type": "string" }`, key)
		required += fmt.Sprintf(`, "%s"`, key)
		if i > 0 {
			keyList += ", "
		}
		keyList += key
	}

	schema := fmt.Sprintf(`{
		"type": "object",
		"properties": { %s },
		"required": [ %s ]
	}`, properties, required)

	hint := fmt.Sprintf(`

In addition to the "synthesis" field, you MUST also extract and return these specific values as separate JSON fields: [%s].
For each field, extract the most relevant value discovered during exploration. If a value was not found, use an empty string.`, keyList)

	return schema, hint
}
