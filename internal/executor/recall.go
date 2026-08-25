package executor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"tzro/internal/compactor"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/symbols"
)

// RecallResult holds both the final synthesis and the refinedContext
// built during the recall loop. VTE (ADR-0067) uses the refinedContext
// for the cloud Verification Gate without storage round-trips.
type RecallResult struct {
	Synthesis      string
	RefinedContext string
}

// RunRecall executes a Recall Node loop (ADR-0038, ADR-0064).
// It traverses the execution history of specified upstream nodes to align and synthesize discoveries.
//
// ADR-0064 Loop Inversion: builds a deterministic baseline context from
// compacted upstream ThoughtSteps BEFORE the agentic loop. The loop is now
// a Refinement Pass that optionally enhances the baseline, not a mandatory
// discovery pass.
func (e *ExecutionEngine) RunRecall(ctx context.Context, taskID, recallNodeID string, upstreamNodeIDs []string, goal string, engine ProbeInferenceEngine) (RecallResult, error) {
	fmt.Fprintf(os.Stderr, "[Recall] Node %s starting for task %s (Upstream: %v)\n", recallNodeID, taskID, upstreamNodeIDs)

	// Temperature 0.6 for recall synthesis: sharper distribution reduces
	// repetitive phrasing while min_p still provides dynamic token pruning.
	ctx = context.WithValue(ctx, inference.TemperatureKey, 0.6)

	maxSteps := 8
	step := 0

	// ADR-0064 / ADR-0078: Build deterministic baseline context BEFORE the loop with 0 LLM calls.
	// This uses Hybrid Extractive Compaction (BM25 + Cosine Similarity) against the goal in <5ms.
	baselineContext, err := buildCompactedRecallContext(ctx, taskID, upstreamNodeIDs, nil, goal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Recall] Warning: baseline compaction failed: %v\n", err)
	}

	// 1. Build initial manifest of discoveries (metadata + synthesis outputs)
	manifest := ""
	for _, nodeID := range upstreamNodeIDs {
		manifest += fmt.Sprintf("### Node: %s\n", nodeID)

		// Include the upstream node's completed synthesis output first, semantically pruned to safe budget.
		if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok && state.RawOutput != "" {
			prunedRaw, err := PruneUpstreamOutput(ctx, state.RawOutput, goal, 4000)
			if err != nil || prunedRaw == "" {
				prunedRaw = truncate(state.RawOutput, 4000)
			}
			manifest += fmt.Sprintf("#### Synthesis Output:\n%s\n\n", prunedRaw)
		}

		// Fix 2 (Cache ID Pinning): Surface the correct cacheId explicitly
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

	// ADR-0064: Pre-populate refinedContext with the baseline (loop inversion).
	// The agentic loop adds to this, never replaces it.
	refinedContext := baselineContext

	// If the upstream node already provided complete synthesis output and there are no extra exploration steps in manifest, bypass the agentic refinement loop directly to synthesis pass.
	if refinedContext != "" && manifest == "" {
		fmt.Fprintf(os.Stderr, "[Recall] Upstream discovery already complete without raw thought steps. Bypassing refinement loop into Reduce phase.\n")
		step = maxSteps
	}

	// ADR-0064: Updated prompt reflects Refinement Pass role (not discovery).
	systemPrompt := fmt.Sprintf(`You are a Recall Node (Refinement Pass). Your goal is to review and refine the baseline summary of upstream discoveries.
Target Goal: %s

## Baseline Summary (Auto-Compacted)
%s

## Discovery Manifest (Metadata)
%s

You can use these tools to examine specific results in detail or add key facts:
- <ACTION>{"tool": "fetch_details", "arguments": {"node_id": "id", "step_index": 0}}</ACTION>
- <ACTION>{"tool": "update_refined_context", "arguments": {"fact": "key fact or signature found"}}</ACTION>

On each step:
1. Review the baseline summary. If it is sufficient, output <SYNTHESIZE_READY>.
2. If the manifest shows high-signal steps not captured in the baseline, fetch details.
3. Record additional critical findings using 'update_refined_context'.
4. When the refined context is sufficient, output <SYNTHESIZE_READY>.

You have a maximum of %d steps.`, goal, baselineContext, manifest, maxSteps)

	lastResult := "Baseline context loaded. Review it and determine if refinement is needed."

	for step < maxSteps {
		step++

		currentPrompt := systemPrompt
		if refinedContext != "" && refinedContext != baselineContext {
			currentPrompt += fmt.Sprintf("\n\n## Current Refined Discovery Context:\n%s", refinedContext)
		}

		// Hard safety clamp: ensure prompt never exceeds 80K chars (~20K tokens) to prevent 400 Bad Request
		const maxSafePromptChars = 80000
		if len(currentPrompt) > maxSafePromptChars {
			currentPrompt = compactor.TruncateTextMiddleOut(currentPrompt, maxSafePromptChars)
		}

		rawResponse, err := engine.Infer(ctx, currentPrompt, lastResult, "", TargetWorker)
		if err != nil {
			return RecallResult{}, fmt.Errorf("recall inference failed at step %d: %w", step, err)
		}

		extractedAction, extractedTool, extractedArgs := parseActionFromResponse(rawResponse)
		if extractedAction == "synthesize" {
			fmt.Fprintf(os.Stderr, "[Recall] Node %s signaled synthesis readiness at step %d\n", recallNodeID, step)
			break
		}

		switch extractedTool {
		case "fetch_details":
			nodeID, _ := extractedArgs["node_id"].(string)
			stepIdx, _ := extractedArgs["step_index"].(float64)

			stepData, err := memory.DB.GetThoughtStepByProbeAndIndex(taskID+"_"+nodeID, int(stepIdx))
			if err != nil {
				lastResult = fmt.Sprintf("Error fetching details: %v", err)
			} else {
				lastResult = fmt.Sprintf("### Details for %s Step %d\nTool: %s\nOutput:\n%s", nodeID, int(stepIdx), stepData.ToolName, stepData.ToolOutput)
			}
		case "update_refined_context":
			fact, _ := extractedArgs["fact"].(string)
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
			lastResult = "No valid action found. Use fetch_details, update_refined_context, or signal synthesis readiness."
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
		maxSyms := 40
		if len(symbolIndex) < maxSyms {
			maxSyms = len(symbolIndex)
		}
		for _, sym := range symbolIndex[:maxSyms] {
			sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", sym.Name, sym.Kind, sym.Signature))
		}
		if len(symbolIndex) > maxSyms {
			sb.WriteString(fmt.Sprintf("... and %d more verified symbols\n", len(symbolIndex)-maxSyms))
		}
		symbolRefBlock = sb.String()
	}

	// ADR-0064: The refinedContext is always populated (deterministic baseline).
	// No need for the old buildEnrichedRecallFallback.
	synthesisInput := refinedContext
	if synthesisInput == "" {
		for _, nodeID := range upstreamNodeIDs {
			if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
				out := state.RawOutput
				if out == "" {
					out = state.Output
				}
				if out != "" {
					synthesisInput = out
					break
				}
			}
		}
	}

	// Build fact-citation constraint for research tasks.
	// When the refined context contains structured facts from the extractive
	// pipeline (CLAIM/SOURCE/QUOTE format), constrain the model to only use
	// the provided facts — preventing parametric bias.
	factConstraint := ""
	if strings.Contains(synthesisInput, "- CLAIM:") && strings.Contains(synthesisInput, "- SOURCE:") {
		factConstraint = `
CRITICAL CONSTRAINT: The context below contains structured facts extracted from source documents.
You may ONLY use the provided facts. Do NOT add information from your own knowledge.
Every factual claim in your response MUST reference a source from the extracted facts.
If the extracted facts are insufficient to answer the question, say so explicitly.`
	}

	// Build research formatting constraint if the task is research- or comparison-oriented
	researchConstraint := ""
	lowerGoal := strings.ToLower(goal)
	if strings.Contains(lowerGoal, "research") || strings.Contains(lowerGoal, "compare") || strings.Contains(lowerGoal, "framework") || strings.Contains(lowerGoal, "vulnerabilit") || strings.Contains(lowerGoal, "cve") || strings.Contains(lowerGoal, "format") || strings.Contains(lowerGoal, "engine") || strings.Contains(lowerGoal, "analysis") {
		researchConstraint = `
IMPORTANT: You MUST cite specific URLs from the verified discovery context for all referenced facts, metrics, and specifications using inline markdown links with descriptive names (e.g. [System Documentation](https://example.com/docs)). Do NOT output literal placeholder tokens like "[Source Title]".
IMPORTANT: When comparing tools, technologies, architectures, or frameworks, include a structured markdown comparison table with corroborated data points.
IMPORTANT: All concrete identifiers, version strings, release numbers, quantitative metrics, dates, and architectural claims must be corroborated by the retrieved discovery context. Do NOT invent unverified version numbers, fabricate non-existent identifiers, or extrapolate speculative future dates. If specific data points are absent from the retrieved evidence, state the limitation explicitly.
IMPORTANT: For cost arbitrage or economic analysis, provide concrete quantitative estimates based on verified evidence in the context.
IMPORTANT: Structure and pace your response concisely so all requested dimensions are completely written without reaching token limits or ending abruptly.`
	}

	synthPrompt := fmt.Sprintf(`You are the Synthesis Engine (Reduce Phase) for a Recall Node.
Goal: %s

## Refined Discovery Context (Verified Facts):
%s%s%s%s

Produce the EXACT document described in the Goal above. Follow these rules:
1. READ THE GOAL CAREFULLY. If it asks for a "function index", produce a function index. If it asks for a "decision log", produce a decision log. Match the requested format precisely.
2. EXTRACT, then FORMAT. First identify all relevant data points from the context, then restructure them into the goal's requested format. Do NOT dump raw context.
3. BE EXHAUSTIVE within the goal's scope. Include ALL items that match the goal's criteria from the discovery context. Do not skip or summarize away individual items.
4. Use the exact structure the goal implies: tables for indexes, bullet lists for logs, sections for architecture docs.
IMPORTANT: You MUST produce actual data values, counts, and results. Do NOT output placeholders like [X] or [Y]. Do NOT output control tokens. If the data is insufficient, explain what is missing.
IMPORTANT: The output must be ONLY the final document requested by the user. Do NOT include sections describing the execution process, such as "Execution History", "Explore Node", "Discovered Facts Summary", "Probe Findings", or "Traversal Steps". Start directly with "# " followed by a descriptive title for the actual document itself (e.g. "# Function Index" or "# Decision Log"). Never include process commentary or node execution post-mortems.`, goal, synthesisInput, symbolRefBlock, factConstraint, researchConstraint)

	// Synthesis escalation policy: if any upstream probe had its synthesis
	// escalated to cloud (local model produced invalid/repetitive output),
	// the Recall node should also use cloud for its synthesis. The local model
	// already proved insufficient for this content type.
	upstreamSynthEscalated := false
	for _, nodeID := range upstreamNodeIDs {
		steps, err := memory.DB.GetThoughtSteps(taskID + "_" + nodeID)
		if err != nil {
			continue
		}
		for _, s := range steps {
			if s.ToolName == "_cloud_synthesis_escalation" {
				upstreamSynthEscalated = true
				break
			}
		}
		if upstreamSynthEscalated {
			break
		}
	}

	var synthesis string
	// ADR-0080: Inject DRY Sampling and Presence Penalty on Recall synthesis passes
	// to eliminate sequence-level repetition loops in the local 4B model.
	synthCtx := context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
		Multiplier:    0.8,
		Base:          1.75,
		AllowedLength: 2,
	})
	synthCtx = context.WithValue(synthCtx, inference.PresencePenaltyKey, 0.2)

	if upstreamSynthEscalated && !isCloudEscalationBlocked() {
		fmt.Fprintf(os.Stderr, "[Recall] Upstream probe synthesis was escalated to cloud — using cloud for Recall synthesis\n")
		synthUserPrompt := fmt.Sprintf("Produce the comprehensive final response fulfilling the goal: %s", goal)
		cloudResult, cloudErr := retryWithCloud(ctx, []inference.InferenceMessage{
			{Role: "system", Content: synthPrompt},
			{Role: "user", Content: synthUserPrompt},
		}, "", taskID)
		if cloudErr != nil {
			// Cloud failed — fall back to local
			fmt.Fprintf(os.Stderr, "[Recall] Cloud synthesis failed (%v), falling back to local engine\n", cloudErr)
			synthesis, err = engine.Infer(synthCtx, synthPrompt, synthUserPrompt, "", TargetWorker)
			if err != nil {
				return RecallResult{}, err
			}
		} else {
			synthesis = cloudResult
		}
	} else if ShouldRunSectionedSynthesis(goal, "", synthesisInput, len(symbolIndex), 0, strings.Contains(taskID, "codegen")) {
		fmt.Fprintf(os.Stderr, "[Recall] Sectioned Map-Reduce synthesis triggered (ADR-0084)\n")

		if IsDocGenGoal(goal) || strings.Contains(taskID, "docgen") {
			outline, outErr := GenerateDocGenOutline(synthCtx, engine, goal, synthesisInput, symbolIndex)
			if outErr == nil && outline != nil && len(outline.Sections) > 0 {
				docSynth, docErr := ExecuteDocGenSectionedSynthesis(synthCtx, goal, synthesisInput, outline, symbolIndex, engine)
				if docErr == nil && len(docSynth) > 200 {
					synthesis = docSynth
					goto postSynthesis
				}
				fmt.Fprintf(os.Stderr, "[Recall] DocGen sectioned synthesis failed (%v) — falling back to single-pass\n", docErr)
			}
		} else {
			secSections := DecomposeResearchGoalIntoSections(goal)
			secSynth, secErr := ExecuteSectionedSynthesis(synthCtx, goal, synthesisInput, secSections, engine)
			if secErr == nil && len(secSynth) > 200 {
				synthesis = secSynth
				goto postSynthesis
			}
			fmt.Fprintf(os.Stderr, "[Recall] Research sectioned synthesis failed (%v) — falling back to single-pass\n", secErr)
		}

		synthUserPrompt := fmt.Sprintf("Produce the comprehensive final response fulfilling the goal: %s", goal)
		synthesis, err = engine.Infer(synthCtx, synthPrompt, synthUserPrompt, "", TargetWorker)
		if err != nil {
			return RecallResult{}, err
		}
	} else if len(synthesisInput) > hybridSynthesisThreshold() && !isCloudEscalationBlocked() && !upstreamSynthEscalated {
		fmt.Fprintf(os.Stderr, "[Recall] Hybrid synthesis triggered: synthesisInput=%d chars exceeds threshold=%d\n", len(synthesisInput), hybridSynthesisThreshold())

		outlinePrompt := fmt.Sprintf(`You are a structured note-taker. Your goal was: %s

Given the refined discovery context below, produce a CONCISE STRUCTURED OUTLINE with:
- Section headers for each major topic
- Key bullet points with specific data values, names, and numbers
- Source references where available
- NO prose paragraphs — bullet points ONLY
- Include ALL relevant facts from the discovery context`, goal)

		outline, outlineErr := engine.Infer(synthCtx, outlinePrompt, synthesisInput, "", TargetWorker)
		if outlineErr == nil && len(strings.TrimSpace(outline)) > 100 {
			fmt.Fprintf(os.Stderr, "[Recall] Hybrid Phase 1 (local outline): %d chars\n", len(outline))

			expandPrompt := fmt.Sprintf(`You are the Synthesis Engine (Reduce Phase) for a Recall Node.
Goal: %s

Expand the structured outline below into a comprehensive, well-cited final answer.
Preserve all data values, names, and numbers from the outline.
Add proper prose transitions and paragraph structure.
IMPORTANT: You MUST produce actual data values, counts, and results. Do NOT output placeholders like [X] or [Y].
IMPORTANT: Begin your response with the content directly. Do NOT write meta-commentary. Start with "# " followed by a heading.%s`, goal, symbolRefBlock)

			cloudResult, cloudErr := retryWithCloud(ctx, []inference.InferenceMessage{
				{Role: "system", Content: expandPrompt},
				{Role: "user", Content: outline},
			}, "", taskID)

			if cloudErr == nil && validateSynthesisOutput(cloudResult) == "" {
				fmt.Fprintf(os.Stderr, "[Recall] Hybrid synthesis succeeded: outline=%d chars, expansion=%d chars\n", len(outline), len(cloudResult))
				synthesis = cloudResult
				goto postSynthesis
			}
			fmt.Fprintf(os.Stderr, "[Recall] Hybrid Phase 2 (cloud expansion) failed, falling through to standard synthesis\n")
		} else if outlineErr != nil {
			fmt.Fprintf(os.Stderr, "[Recall] Hybrid Phase 1 (local outline) failed: %v, falling through to standard synthesis\n", outlineErr)
		}

		synthUserPrompt := fmt.Sprintf("Produce the comprehensive final response fulfilling the goal: %s", goal)
		synthesis, err = engine.Infer(synthCtx, synthPrompt, synthUserPrompt, "", TargetWorker)
		if err != nil {
			return RecallResult{}, err
		}
	} else {
		synthUserPrompt := fmt.Sprintf("Produce the comprehensive final response fulfilling the goal: %s", goal)
		synthesis, err = engine.Infer(synthCtx, synthPrompt, synthUserPrompt, "", TargetWorker)
		if err != nil {
			return RecallResult{}, err
		}
	}

postSynthesis:

	// Symbol Anchor Check (ADR-0047): verify synthesis references against Index
	if len(symbolIndex) > 0 {
		anchorResult := symbols.CheckAnchoring(synthesis, symbolIndex, symbols.DefaultAnchorThreshold)
		fmt.Fprintf(os.Stderr, "[Recall] Symbol Anchor Check: %d referenced, %d anchored, %d unanchored (%.0f%% hallucination)\n",
			anchorResult.TotalReferenced, anchorResult.Anchored, len(anchorResult.Unanchored), anchorResult.HallucinationPct*100)

		if anchorResult.NeedsCorrection {
			fmt.Fprintf(os.Stderr, "[Recall] Hallucination threshold exceeded — running targeted correction pass.\n")
			correctionPrompt := symbols.BuildCorrectionPrompt(synthesis, anchorResult.Unanchored, symbolIndex)
			corrected, corrErr := engine.Infer(ctx, "You are a code documentation editor. Fix hallucinated symbol names.", correctionPrompt, "", TargetWorker)
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

	// ADR-0058: Detect if upstream was an Analyze Node by checking for
	// cache tool usage. If so, exempt synthesis from repetition detection
	// since tabular data naturally repeats column headers.
	isAnalyzeUpstream := false
	for _, nodeID := range upstreamNodeIDs {
		if steps, err := memory.DB.GetThoughtSteps(taskID + "_" + nodeID); err == nil {
			for _, s := range steps {
				if s.ToolName == "sql_cached_data" || s.ToolName == "introspect_cache" {
					isAnalyzeUpstream = true
					break
				}
			}
		}
		if isAnalyzeUpstream {
			break
		}
	}
	var recallValidationOpts []ValidationOption
	if isAnalyzeUpstream {
		recallValidationOpts = append(recallValidationOpts, WithAnalyzeNode())
	}

	// Fix 3 (Synthesis Generation Guard): Validate the synthesis output.
	// Detect control token leaks, degenerate output, and repetitive content.
	// Re-attempt with cloud model on failure (same pattern as ConfidenceTier escalation).
	reason := validateSynthesisOutput(synthesis, recallValidationOpts...)
	if reason != "" {
		fmt.Fprintf(os.Stderr, "[Recall] Synthesis output invalid (%s), escalating to cloud\n", reason)
		if !isCloudEscalationBlocked() {
			synthUserPrompt := fmt.Sprintf("Produce the comprehensive final response fulfilling the goal: %s", goal)
			cloudResult, cloudErr := retryWithCloud(ctx, []inference.InferenceMessage{
				{Role: "system", Content: synthPrompt},
				{Role: "user", Content: synthUserPrompt},
			}, "", taskID)
			if cloudErr == nil && validateSynthesisOutput(cloudResult, recallValidationOpts...) == "" {
				fmt.Fprintf(os.Stderr, "[Recall] Cloud escalation succeeded for synthesis (%d chars)\n", len(cloudResult))
				synthesis = cloudResult
			}
		}
	}

	// Strip any leaked control tokens from the output
	synthesis = stripControlTokens(synthesis)
	return RecallResult{Synthesis: synthesis, RefinedContext: refinedContext}, nil
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



// controlTokens are internal control signals that should never appear in user-facing output.
var controlTokens = []string{
	"<SYNTHESIZE_READY>",
	"SYNTHESIZE_READY",
	"<ACTION>", "</ACTION>",
	"<TOOL_CALL>", "</TOOL_CALL>",
	// Reasoning trace tags leaked by the local model at higher temperatures.
	// These come from the model's instruction-tuning and are internal artifacts.
	"<thinking>", "</thinking>",
	"<think>", "</think>",
	"<tool_code>", "</tool_code>",
	"<tool_output>", "</tool_output>",
}

// validationConfig holds options for validateSynthesisOutput.
type validationConfig struct {
	isAnalyzeNode   bool // When true, skip repetition detection (tabular data naturally repeats column headers)
	isCodegenOutput bool // When true, apply codegen-specific idiom exclusions and raise n-gram minimum
}

// ValidationOption configures synthesis output validation behavior.
type ValidationOption func(*validationConfig)

// WithAnalyzeNode exempts the output from the repetition detector.
// Analyze Node synthesis naturally contains repeated column headers and
// structural patterns in tabular data — these are not failure modes.
func WithAnalyzeNode() ValidationOption {
	return func(c *validationConfig) {
		c.isAnalyzeNode = true
	}
}

// WithCodegenOutput configures the repetition detector for generated code.
// It raises the minimum n-gram repetition threshold from 4 to 8 and strips
// known Go idiomatic phrases ("if err != nil", "http.Error(", etc.) before
// n-gram counting, preventing false positives on multi-handler files that
// repeat canonical error-handling patterns.
// ADR-run32: Addresses false rejection of codegen output in Run 32.
func WithCodegenOutput() ValidationOption {
	return func(c *validationConfig) {
		c.isCodegenOutput = true
	}
}

// validateSynthesisOutput checks the synthesis output for common failure modes:
//   - Control token leaks (SYNTHESIZE_READY, ACTION, TOOL_CALL)
//   - Degenerate output (< 50 chars after stripping control tokens)
//   - Repetitive content (3+ repeated 4-word sequences) — skipped for Analyze Nodes
//
// Returns empty string if valid, or a reason string describing the failure.
func validateSynthesisOutput(output string, opts ...ValidationOption) string {
	var cfg validationConfig
	for _, opt := range opts {
		opt(&cfg)
	}

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

	// Check for Generation Guard abort marker (F3-B: guard-aborted synthesis recovery).
	// When the GenerationGuard aborts generation mid-stream, it appends a marker
	// to the truncated output. Detect it here so the Recall Node can escalate
	// to cloud re-synthesis before VTE needs to catch it.
	if strings.Contains(cleaned, "[GENERATION_ABORTED") {
		return "generation aborted by guard"
	}

	// Check if output IS a control token (the entire output)
	trimmed := strings.TrimSpace(output)
	for _, token := range controlTokens {
		if trimmed == token {
			return fmt.Sprintf("output is bare control token: %s", token)
		}
	}

	// Check for repetitive content: detect N+ occurrences of any 5-word sequence.
	// Threshold scales with output length to avoid false positives on structural
	// markdown repetition (e.g., repeated section headers in longer documents).
	// ADR-0058: Skip for Analyze Nodes — tabular data naturally repeats column
	// headers and structural patterns. ADR-0066: 4-gram→5-gram, 3x→scaled.
	// ADR-run32: WithCodegenOutput raises minimum from 4 to 8.
	if !cfg.isAnalyzeNode {
		// Filter out markdown table rows so repeated table delimiters (e.g. "| Yes | Yes |")
		// do not trigger false positive repetition rejections on valid comparison tables.
		var nonTableLines []string
		var listLineCount int
		for _, line := range strings.Split(cleaned, "\n") {
			trimmedLine := strings.TrimSpace(line)
			if strings.HasPrefix(trimmedLine, "|") && strings.HasSuffix(trimmedLine, "|") {
				continue
			}
			if strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(trimmedLine, "* ") ||
				strings.HasPrefix(trimmedLine, "• ") || strings.HasPrefix(trimmedLine, "+ ") ||
				(len(trimmedLine) > 2 && trimmedLine[0] >= '0' && trimmedLine[0] <= '9' && (trimmedLine[1] == '.' || trimmedLine[2] == '.')) {
				listLineCount++
			}
			nonTableLines = append(nonTableLines, line)
		}
		ngramInput := strings.Join(nonTableLines, "\n")

		ngramMinThreshold := 4
		if cfg.isCodegenOutput {
			// For generated code, raise the minimum repetition threshold from 4 to 8.
			// Valid Go files with multiple handlers naturally repeat structural patterns
			// (function signatures, error checks) fewer than 8 times per 5-word n-gram.
			// ADR-run32: min threshold 4 → 8 for codegen output.
			ngramMinThreshold = 8
		} else if listLineCount >= 3 {
			// Structured bullet/enumerated lists naturally repeat structural metric templates
			// (e.g. "— 1 total lead - Sources:" or "Total: 10 leads") across distinct items.
			// Raise threshold from 4 to 8 to prevent false-positive rejection of valid tabular reports.
			ngramMinThreshold = 8
		}
		words := strings.Fields(ngramInput)
		ngramSize := 5
		threshold := len(words) / 250
		if threshold < ngramMinThreshold {
			threshold = ngramMinThreshold
		}
		if len(words) >= ngramSize*threshold {
			ngramCounts := make(map[string]int)
			for i := 0; i <= len(words)-ngramSize; i++ {
				ngram := strings.Join(words[i:i+ngramSize], " ")
				if isStructuralSyntaxNgram(ngram) {
					continue
				}
				ngramCounts[ngram]++
				if ngramCounts[ngram] >= threshold {
					return fmt.Sprintf("repetitive content detected (phrase '%s' repeated %d times)", truncate(ngram, 50), ngramCounts[ngram])
				}
			}
		}
	}

	// Check for placeholder patterns (e.g., *[X]*, *[Top Sector]*)
	placeholderRe := regexp.MustCompile(`\*\[.+?\]\*`)
	placeholders := placeholderRe.FindAllString(output, -1)
	if len(placeholders) >= 3 {
		return fmt.Sprintf("output contains %d template placeholders", len(placeholders))
	}

	// Check for meta-commentary degeneration (benchmark R14 regression):
	// The 4B model sometimes produces varied-but-vacuous sentences like
	// "The synthesis is complete. The final answer is ready. The engine is done."
	// These dodge n-gram detection because each sentence is a unique variant.
	// Detect by counting sentences that match meta-completion patterns and
	// flagging when they dominate the output (>40% of sentences).
	if !cfg.isAnalyzeNode {
		metaPatterns := []string{
			"synthesis is complete", "synthesis is done", "synthesis is final",
			"synthesis is finished", "synthesis is closed", "synthesis is ended",
			"synthesis is over", "synthesis is sealed", "synthesis is wrapped",
			"synthesis is terminated", "synthesis is concluded", "synthesis is capped",
			"answer is complete", "answer is done", "answer is final",
			"answer is ready", "answer is finished", "answer is closed",
			"answer is sealed", "answer is set", "answer is confirmed",
			"engine is done", "engine is complete", "engine is finished",
			"engine is closed", "engine is ended", "engine is sealed",
			"engine has completed", "engine has finished", "engine has concluded",
			"engine has succeeded", "engine has stopped", "engine has ceased",
			"task is complete", "task is done", "task is finished",
			"goal has been achieved", "goal has been fulfilled", "goal has been met",
			"ready for use", "ready for integration",
		}
		lowerCleaned := strings.ToLower(cleaned)
		// Split into sentences (rough: split on ". ")
		sentences := strings.Split(lowerCleaned, ". ")
		if len(sentences) >= 5 {
			metaCount := 0
			for _, sentence := range sentences {
				trimmed := strings.TrimSpace(sentence)
				if trimmed == "" {
					continue
				}
				for _, pattern := range metaPatterns {
					if strings.Contains(trimmed, pattern) {
						metaCount++
						break
					}
				}
			}
			metaRatio := float64(metaCount) / float64(len(sentences))
			if metaRatio > 0.4 && metaCount >= 5 {
				return fmt.Sprintf("meta-commentary degeneration detected (%d/%d sentences are vacuous completion phrases, ratio=%.0f%%)", metaCount, len(sentences), metaRatio*100)
			}

			// Trailing concentration check: the 4B model often produces
			// valid content followed by a degenerate tail. The overall ratio
			// check above misses this because the valid preamble dilutes
			// the percentage. Check the last 30% of sentences independently.
			tailStart := len(sentences) - len(sentences)*30/100
			if tailStart < 0 {
				tailStart = 0
			}
			tailSentences := sentences[tailStart:]
			if len(tailSentences) >= 4 {
				tailMetaCount := 0
				for _, sentence := range tailSentences {
					trimmedSent := strings.TrimSpace(sentence)
					if trimmedSent == "" {
						continue
					}
					for _, pattern := range metaPatterns {
						if strings.Contains(trimmedSent, pattern) {
							tailMetaCount++
							break
						}
					}
				}
				tailRatio := float64(tailMetaCount) / float64(len(tailSentences))
				if tailRatio > 0.6 && tailMetaCount >= 3 {
					return fmt.Sprintf("trailing meta-commentary degeneration detected (%d/%d tail sentences are vacuous, ratio=%.0f%%)", tailMetaCount, len(tailSentences), tailRatio*100)
				}
			}
		}
	}

	return ""
}

// stripControlTokens removes internal control signals from user-facing output.
func stripControlTokens(output string) string {
	result := output
	for _, token := range controlTokens {
		result = strings.ReplaceAll(result, token, "")
	}
	result = strings.TrimSpace(result)

	// ADR-0060: Trailing repetition stripping is now handled by the
	// GenerationGuard at the Inference Backend layer. Character-level
	// degeneration is caught during streaming (or post-generation for
	// non-streaming backends), so we no longer need to scan here.

	return result
}

// NOTE: stripTrailingRepetition has been removed (ADR-0060).
// Character-level degeneration detection is now handled by the GenerationGuard

func isStructuralSyntaxNgram(ngram string) bool {
	lower := strings.ToLower(ngram)
	return strings.Contains(lower, "signature") ||
		strings.Contains(lower, "func ") ||
		strings.Contains(lower, "func(") ||
		strings.Contains(lower, "type ") ||
		strings.Contains(lower, "struct") ||
		strings.Contains(lower, "interface") ||
		strings.Contains(lower, "parameters") ||
		strings.Contains(lower, "returns") ||
		strings.Contains(lower, "description") ||
		strings.Contains(lower, "`") ||
		strings.Contains(lower, "**")
}
// at the Inference Backend layer, which can abort streaming generation early
// rather than stripping post-hoc. See internal/inference/generation_guard.go.
