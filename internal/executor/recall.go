package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"tzro/internal/compactor"
	"tzro/internal/cache"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/symbols"
)

// RunRecall executes a Recall Node loop (ADR-0038).
// It traverses the execution history of specified upstream nodes to align and synthesize discoveries.
func (e *ExecutionEngine) RunRecall(ctx context.Context, taskID, recallNodeID string, upstreamNodeIDs []string, goal string, engine ProbeInferenceEngine) (string, error) {
	fmt.Fprintf(os.Stderr, "[Recall] Node %s starting for task %s (Upstream: %v)\n", recallNodeID, taskID, upstreamNodeIDs)

	maxSteps := 8
	step := 0

	// 1. Build initial manifest of discoveries (metadata + synthesis outputs)
	manifest := ""
	for _, nodeID := range upstreamNodeIDs {
		manifest += fmt.Sprintf("### Node: %s\n", nodeID)

		// Include the upstream node's completed synthesis output first.
		// This is the high-quality, already-synthesized result from the
		// probe/analyze node — much more useful than raw step previews.
		if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok && state.RawOutput != "" {
			manifest += fmt.Sprintf("#### Synthesis Output:\n%s\n\n", state.RawOutput)
		}

		// Fix 2 (Cache ID Pinning): Surface the correct cacheId explicitly
		// so the model doesn't need to "discover" it from abbreviated context.
		if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
			cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
			combined := state.Output + "\n" + state.RawOutput
			if cacheId := cacheIdRe.FindString(combined); cacheId != "" {
				manifest += fmt.Sprintf("#### Active Cache Table: %s\nQuery this table using SQL. Examples:\n- SELECT COUNT(*) FROM %s\n- SELECT ColumnName, COUNT(*) as cnt FROM %s GROUP BY ColumnName ORDER BY cnt DESC\n- SELECT * FROM %s LIMIT 10\n\n", cacheId, cacheId, cacheId, cacheId)
			}
		}

		// Then include step-level detail as supporting evidence
		steps, err := memory.DB.GetThoughtSteps(taskID + "_" + nodeID)
		if err != nil {
			continue
		}
		if len(steps) > 0 {
			manifest += "#### Exploration Steps:\n"
			for _, s := range steps {
				if s.ToolName != "" {
					preview := truncate(s.ToolOutput, 100)
					manifest += fmt.Sprintf("- Step %d: %s(%s) -> %s\n", s.StepIndex, s.ToolName, s.ToolArgs, preview)
				}
			}
		}
	}

	systemPrompt := fmt.Sprintf(`You are a Recall Node (Map Phase). Your goal is to align and synthesize discoveries from previous nodes.
Target Goal: %s

## Discovery Manifest (Tool Outputs from Upstream Nodes)
%s

You can use these tools to examine specific results in detail or record key facts:
- <ACTION>{"tool": "fetch_details", "arguments": {"node_id": "id", "step_index": 0}}</ACTION>
- <ACTION>{"tool": "update_refined_context", "arguments": {"fact": "key fact or signature found"}}</ACTION>

On each step:
1. Reason about which discovery metadata suggests a high-signal result.
2. Fetch details for high-signal steps.
3. Record critical findings using 'update_refined_context'.
4. When you have aligned all necessary information and your 'Refined Discovery Context' is sufficient, output <SYNTHESIZE_READY>.

You have a maximum of %d steps.`, goal, manifest, maxSteps)

	lastResult := "Manifest loaded."
	refinedContext := ""

	for step < maxSteps {
		step++

		// 1. Infer next action
		// Include refinedContext in the prompt if not empty
		currentPrompt := systemPrompt
		if refinedContext != "" {
			currentPrompt += fmt.Sprintf("\n\n## Current Refined Discovery Context:\n%s", refinedContext)
		}

		rawResponse, err := engine.Infer(ctx, currentPrompt, lastResult, "")
		if err != nil {
			return "", fmt.Errorf("recall inference failed at step %d: %w", step, err)
		}

		if strings.Contains(rawResponse, "<SYNTHESIZE_READY>") {
			fmt.Fprintf(os.Stderr, "[Recall] Node %s signaled synthesis readiness at step %d\n", recallNodeID, step)
			break
		}

		// 2. Extract and execute tool call
		action, args := extractAction(rawResponse)
		switch action {
		case "fetch_details":
			nodeID, _ := args["node_id"].(string)
			stepIdx, _ := args["step_index"].(float64)

			stepData, err := memory.DB.GetThoughtStepByProbeAndIndex(taskID+"_"+nodeID, int(stepIdx))
			if err != nil {
				lastResult = fmt.Sprintf("Error fetching details: %v", err)
			} else {
				lastResult = fmt.Sprintf("### Details for %s Step %d\nTool: %s\nOutput:\n%s", nodeID, int(stepIdx), stepData.ToolName, stepData.ToolOutput)
			}
		case "update_refined_context":
			fact, _ := args["fact"].(string)
			if refinedContext != "" {
				refinedContext += "\n"
			}
			refinedContext += "- " + fact
			lastResult = "Refined context updated."

			// ADR-0040: Automatic Recall Compaction
			if len(refinedContext) > 2000 {
				compacted, err := e.compactRefinedContext(ctx, refinedContext, goal, engine)
				if err == nil {
					refinedContext = compacted
					lastResult = "Refined context updated and compacted (limit reached)."
				}
			}
		default:
			lastResult = "No valid ACTION found. Use fetch_details, update_refined_context, or SYNTHESIZE_READY."
		}

		// Publish progress
		e.getPublisher().PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  recallNodeID,
			Type:    "recall_step",
			Content: fmt.Sprintf("Step %d: %s (Context Size: %d)", step, lastResult, len(refinedContext)),
		})
	}

	// Final Synthesis Pass (Reduce)
	fmt.Fprintf(os.Stderr, "[Recall] Node %s executing final synthesis (Reduce Phase).\n", recallNodeID)

	// Load Symbol Index from upstream probes (ADR-0047)
	var symbolIndex []symbols.Symbol
	for _, nodeID := range upstreamNodeIDs {
		probeID := taskID + "_" + nodeID
		syms, err := memory.DB.GetSymbolIndex(probeID)
		if err == nil {
			symbolIndex = append(symbolIndex, syms...)
		}
	}

	// Build Symbol Index reference block for the synthesis prompt
	symbolRefBlock := ""
	if len(symbolIndex) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n## Authoritative Symbol Reference (AST-extracted, verified):\n")
		sb.WriteString("Use ONLY these exact names when referring to types, functions, and interfaces:\n")
		for _, sym := range symbolIndex {
			sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", sym.Name, sym.Kind, sym.Signature))
		}
		symbolRefBlock = sb.String()
	}


	// Fix 1 (Recall Context Injection): When refinedContext is empty (the local
	// model short-circuited to SYNTHESIZE_READY without calling update_refined_context),
	// build an enriched fallback from actual tool outputs instead of the bare manifest
	// which only has 100-char previews of each step.
	synthesisInput := refinedContext
	if strings.TrimSpace(synthesisInput) == "" {
		synthesisInput = buildEnrichedRecallFallback(taskID, upstreamNodeIDs, manifest)
		fmt.Fprintf(os.Stderr, "[Recall] Node %s: enriched fallback context (%d chars)\n", recallNodeID, len(synthesisInput))
	}

	synthPrompt := fmt.Sprintf(`You are the Synthesis Engine (Reduce Phase) for a Recall Node.
Goal: %s

## Refined Discovery Context (Verified Facts):
%s%s

Review the gathered facts and produce a comprehensive, structured final answer. If the facts are insufficient, explain what is missing.`, goal, refinedContext, symbolRefBlock)
Review the gathered facts and produce a comprehensive, structured final answer.
IMPORTANT: You MUST produce actual data values, counts, and results. Do NOT output placeholders like [X] or [Y]. Do NOT output control tokens. If the data is insufficient, explain what is missing.`, goal, synthesisInput)

	synthesis, err := engine.Infer(ctx, synthPrompt, lastResult, "")
	if err != nil {
		return "", err
	}

	// Symbol Anchor Check (ADR-0047): verify synthesis references against Index
	if len(symbolIndex) > 0 {
		anchorResult := symbols.CheckAnchoring(synthesis, symbolIndex, symbols.DefaultAnchorThreshold)
		fmt.Fprintf(os.Stderr, "[Recall] Symbol Anchor Check: %d referenced, %d anchored, %d unanchored (%.0f%% hallucination)\n",
			anchorResult.TotalReferenced, anchorResult.Anchored, len(anchorResult.Unanchored), anchorResult.HallucinationPct*100)

		if anchorResult.NeedsCorrection {
			fmt.Fprintf(os.Stderr, "[Recall] Hallucination threshold exceeded — running targeted correction pass.\n")
			correctionPrompt := symbols.BuildCorrectionPrompt(synthesis, anchorResult.Unanchored, symbolIndex)
			corrected, corrErr := engine.Infer(ctx, "You are a code documentation editor. Fix hallucinated symbol names.", correctionPrompt, "")
			if corrErr == nil && corrected != "" {
				synthesis = corrected
				fmt.Fprintf(os.Stderr, "[Recall] Correction pass complete. Re-checking...\n")

				// Re-check after correction
				recheck := symbols.CheckAnchoring(synthesis, symbolIndex, symbols.DefaultAnchorThreshold)
				fmt.Fprintf(os.Stderr, "[Recall] Post-correction: %d referenced, %d anchored, %d unanchored (%.0f%% hallucination)\n",
					recheck.TotalReferenced, recheck.Anchored, len(recheck.Unanchored), recheck.HallucinationPct*100)
			}
		}
	}

	return synthesis, nil
	result, err := engine.Infer(ctx, synthPrompt, lastResult, "")
	if err != nil {
		return "", err
	}

	// Fix 3 (Synthesis Generation Guard): Validate the synthesis output.
	// Detect control token leaks, degenerate output, and repetitive content.
	reason := validateSynthesisOutput(result)
	if reason != "" {
		fmt.Fprintf(os.Stderr, "[Recall] Synthesis output invalid (%s), escalating to cloud\n", reason)
		if !isCloudEscalationBlocked() {
			cloudResult, cloudErr := retryWithCloud(ctx, []inference.InferenceMessage{
				{Role: "system", Content: synthPrompt},
				{Role: "user", Content: lastResult},
			}, "", taskID)
			if cloudErr == nil && validateSynthesisOutput(cloudResult) == "" {
				fmt.Fprintf(os.Stderr, "[Recall] Cloud escalation succeeded for synthesis (%d chars)\n", len(cloudResult))
				return cloudResult, nil
			}
		}
	}

	// Strip any leaked control tokens from the output
	result = stripControlTokens(result)
	return result, nil
}

func extractAction(response string) (string, map[string]interface{}) {
	actionRe := regexp.MustCompile("(?s)<ACTION>(.*?)</ACTION>")
	matches := actionRe.FindStringSubmatch(response)
	if len(matches) > 1 {
		var parsed struct {
			Tool      string                 `json:"tool"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(matches[1]), &parsed); err == nil {
			return parsed.Tool, parsed.Arguments
		}
	}
	return "", nil
}

func (e *ExecutionEngine) compactRefinedContext(ctx context.Context, refinedCtx, goal string, engine ProbeInferenceEngine) (string, error) {
	// Use the structured compactor for content-aware fact compaction.
	// Code/data facts are preserved deterministically.
	// Large text-only facts are compressed via LLM.
	compactEngine := &compactor.RouterEngine{}
	result, err := compactor.CompactFacts(ctx, refinedCtx, goal, 2000, compactEngine)
	if err != nil {
		return refinedCtx, err
	}

	fmt.Fprintf(os.Stderr, "[Recall Compactor] %d→%d chars (%d LLM calls)\n",
		result.InputChars, result.OutputChars, result.LLMCalls)

	return result.Output, nil
}

// buildEnrichedRecallFallback constructs a rich fallback context when the Recall
// loop's refinedContext is empty (the local model short-circuited to SYNTHESIZE_READY
// without calling update_refined_context). Instead of falling back to the bare
// manifest (which only has 100-char step previews), this function includes:
//   - The upstream probe's synthesis output (from manifest)
//   - Full tool outputs from successful probe steps (capped at 2000 chars each)
//   - Cache introspection data if a cacheId is present
//
// This directly addresses the root cause of 4/5 benchmark failures where the
// Recall node produced empty/degenerate synthesis from insufficient context.
func buildEnrichedRecallFallback(taskID string, upstreamNodeIDs []string, manifest string) string {
	var enriched strings.Builder

	// Start with the manifest (contains synthesis output + step metadata)
	enriched.WriteString(manifest)
	enriched.WriteString("\n\n## Enriched Tool Outputs (Full Data)\n")

	cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
	maxOutputLen := 2000

	for _, nodeID := range upstreamNodeIDs {
		steps, err := memory.DB.GetThoughtSteps(taskID + "_" + nodeID)
		if err != nil {
			continue
		}

		for _, s := range steps {
			if s.ToolName == "" || s.ToolOutput == "" {
				continue
			}
			// Only include steps with substantive output (not error messages)
			if strings.HasPrefix(s.ToolOutput, "Error") || strings.HasPrefix(s.ToolOutput, "No valid") {
				continue
			}
			output := s.ToolOutput
			if len(output) > maxOutputLen {
				output = output[:maxOutputLen] + "\n... (truncated)"
			}
			enriched.WriteString(fmt.Sprintf("\n### Step %d: %s\nArguments: %s\nOutput:\n%s\n", s.StepIndex, s.ToolName, s.ToolArgs, output))
		}

		// Include cache introspection if available
		if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
			combined := state.Output + "\n" + state.RawOutput
			if cacheId := cacheIdRe.FindString(combined); cacheId != "" {
				schema := cache.DefaultStore.Introspect(context.Background(), cacheId)
				if schema != "" && !strings.HasPrefix(schema, "Error:") {
					enriched.WriteString(fmt.Sprintf("\n### Cache Data Schema for %s\n%s\n", cacheId, truncate(schema, 3000)))
				}
			}
		}
	}

	return enriched.String()
}

// controlTokens are internal control signals that should never appear in user-facing output.
var controlTokens = []string{
	"<SYNTHESIZE_READY>",
	"SYNTHESIZE_READY",
	"<ACTION>",
	"</ACTION>",
	"<TOOL_CALL>",
	"</TOOL_CALL>",
}

// validateSynthesisOutput checks the synthesis output for common failure modes:
//   - Control token leaks (SYNTHESIZE_READY, ACTION, TOOL_CALL)
//   - Degenerate output (< 50 chars after stripping control tokens)
//   - Repetitive content (3+ repeated 4-word sequences)
//
// Returns empty string if valid, or a reason string describing the failure.
func validateSynthesisOutput(output string) string {
	// Strip control tokens for length check
	cleaned := output
	for _, token := range controlTokens {
		cleaned = strings.ReplaceAll(cleaned, token, "")
	}
	cleaned = strings.TrimSpace(cleaned)

	// Check for degenerate output
	if len(cleaned) < 50 {
		return fmt.Sprintf("degenerate output (%d chars after cleaning)", len(cleaned))
	}

	// Check if output IS a control token (the entire output)
	trimmed := strings.TrimSpace(output)
	for _, token := range controlTokens {
		if trimmed == token {
			return fmt.Sprintf("output is bare control token: %s", token)
		}
	}

	// Check for repetitive content: detect 3+ occurrences of any 4-word sequence
	words := strings.Fields(cleaned)
	if len(words) >= 12 { // Need at least 12 words to detect 3x repetition of 4-word sequences
		ngramCounts := make(map[string]int)
		for i := 0; i <= len(words)-4; i++ {
			ngram := strings.Join(words[i:i+4], " ")
			ngramCounts[ngram]++
			if ngramCounts[ngram] >= 3 {
				return fmt.Sprintf("repetitive content detected (phrase '%s' repeated %d times)", truncate(ngram, 40), ngramCounts[ngram])
			}
		}
	}

	// Check for placeholder patterns (e.g., *[X]*, *[Top Sector]*)
	placeholderRe := regexp.MustCompile(`\*\[.+?\]\*`)
	placeholders := placeholderRe.FindAllString(output, -1)
	if len(placeholders) >= 3 {
		return fmt.Sprintf("output contains %d template placeholders", len(placeholders))
	}

	return ""
}

// stripControlTokens removes internal control signals from user-facing output.
func stripControlTokens(output string) string {
	result := output
	for _, token := range controlTokens {
		result = strings.ReplaceAll(result, token, "")
	}
	return strings.TrimSpace(result)
}
