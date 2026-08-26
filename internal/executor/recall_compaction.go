package executor

// recall_compaction.go — Deterministic baseline context for Recall Node
// Refinement Pass (ADR-0064 Mechanism C: Recall Loop Inversion).
//
// buildCompactedRecallContext reads ThoughtSteps from upstream probes and
// compacts them using content-aware CompactToolOutputs. This provides a
// guaranteed-quality floor that the agentic loop can optionally enhance.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"tzro/internal/compactor"
	cfgpkg "tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// buildCompactedRecallContext reads ThoughtSteps from upstream probes and
// compacts their tool outputs using content-aware strategies (ADR-0064).
//
// Returns a deterministic baseline context string within the configured budget.
// Code outputs → skeleton, text → LLM fact-extraction, tabular → sample rows.
// Analyze Node tools (sql_cached_data, introspect_cache) are exempt.
func buildCompactedRecallContext(
	ctx context.Context,
	taskID string,
	upstreamNodeIDs []string,
	engine compactor.CompactEngine,
	goal string,
) (string, error) {
	if goal != "" {
		ctx = context.WithValue(ctx, compactor.CompactorGoalKey, goal)
	}
	budget := cfgpkg.GetRecallCompactionBudgetChars()

	var allSteps []compactor.ToolOutputStep
	for _, nodeID := range upstreamNodeIDs {
		probeID := taskID + "_" + nodeID
		steps, err := memory.DB.GetThoughtSteps(probeID)
		if err != nil {
			continue
		}

		// Two-pass approach: first collect all successful outputs as upstream
		// context, then classify errors against that context.
		var successOutputs []string
		var rawSteps []memory.ThoughtStep
		for _, s := range steps {
			if s.ToolName == "" || s.ToolOutput == "" {
				continue
			}
			rawSteps = append(rawSteps, s)
			if !isToolError(s.ToolOutput) {
				successOutputs = append(successOutputs, s.ToolOutput)
			}
		}

		upstreamContext := strings.Join(successOutputs, "\n")
		prunedCount := 0

		// Window rawSteps to at most maxStepsPerProbe before adding to allSteps.
		// ADR-run32: Long probes (60+ steps) overflow the recall context budget;
		// stratified windowing preserves head (orientation), tail (final state),
		// and evenly-spaced middle steps while capping total at 25.
		rawSteps = windowThoughtSteps(rawSteps, 25)

		for _, s := range rawSteps {
			// Prune uninformative TOOL_ERRORs before they enter synthesis context
			if isToolError(s.ToolOutput) && IsUninformativeToolError(s.ToolName, s.ToolArgs, s.ToolOutput, upstreamContext) {
				prunedCount++
				continue
			}
			allSteps = append(allSteps, compactor.ToolOutputStep{
				StepIndex:  s.StepIndex,
				ToolName:   s.ToolName,
				ToolArgs:   s.ToolArgs,
				ToolOutput: s.ToolOutput,
			})
		}

		if prunedCount > 0 {
			fmt.Fprintf(os.Stderr, "[EdgeEntry] Pruned %d uninformative TOOL_ERRORs from probe %s (hallucinated parameters)\n", prunedCount, probeID)
		}
	}

	if len(allSteps) == 0 {
		// Fallback: check node_states for upstream direct synthesis output (ADR-0086/0088)
		for _, nodeID := range upstreamNodeIDs {
			if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
				out := state.RawOutput
				if out == "" {
					out = state.Output
				}
				if out == "" {
					continue
				}
				// If the output fits within budget, return it directly.
				if len(out) <= budget {
					return out, nil
				}
				// FM-5 fix: Decompose oversized List node output into file-level
				// chunks and route them through the compaction pipeline. The List
				// node's output uses "--- file:" dividers between per-file
				// extractions. Splitting on these boundaries produces chunks that
				// each fit within the 4B model's context window.
				chunks := splitListOutputIntoFileChunks(out)
				if len(chunks) <= 1 {
					// No file dividers found — truncate to budget as last resort.
					fmt.Fprintf(os.Stderr, "[Recall] Oversized upstream output (%d chars) with no file dividers — truncating to budget %d\n", len(out), budget)
					return out[:budget], nil
				}
				fmt.Fprintf(os.Stderr, "[Recall] Decomposing oversized List output (%d chars) into %d file chunks for compaction\n", len(out), len(chunks))
				for i, chunk := range chunks {
					allSteps = append(allSteps, compactor.ToolOutputStep{
						StepIndex:  i,
						ToolName:   "list_extract",
						ToolArgs:   chunk.FilePath,
						ToolOutput: chunk.Content,
					})
				}
				// Fall through to compaction below
				break
			}
		}
		if len(allSteps) == 0 {
			return "", nil
		}
	}

	// Compaction bypass: when total raw content fits within the budget,
	// skip CompactToolOutputs entirely and concatenate raw step outputs.
	// This preserves fine-grained details (function signatures, type
	// declarations, exact values) that per-step compaction would strip.
	// The compaction pipeline applies per-step budgets that are much
	// smaller than the global budget, causing data loss even when the
	// total would fit. Only compact when we genuinely need to reduce size.
	totalRawChars := 0
	for _, s := range allSteps {
		totalRawChars += len(s.ToolOutput)
	}
	if totalRawChars <= budget {
		var sb strings.Builder
		for _, s := range allSteps {
			sb.WriteString(fmt.Sprintf("### Step %d: %s\n", s.StepIndex, s.ToolName))
			if s.ToolArgs != "" {
				sb.WriteString(fmt.Sprintf("Args: %s\n", s.ToolArgs))
			}
			sb.WriteString(s.ToolOutput)
			sb.WriteString("\n\n")
		}
		rawResult := sb.String()
		fmt.Fprintf(os.Stderr,
			"[Recall] Skipping compaction — raw content (%d chars) fits within budget (%d)\n",
			totalRawChars, budget)
		return rawResult, nil
	}

	result, err := compactor.CompactToolOutputs(ctx, allSteps, budget, engine)
	if err != nil {
		return "", fmt.Errorf("recall compaction failed: %w", err)
	}

	fmt.Fprintf(
		os.Stderr,
		"[Recall] Compacted baseline context: %d→%d chars (%d LLM calls)\n",
		result.InputChars, result.OutputChars, result.LLMCalls,
	)

	return result.Output, nil
}

// windowThoughtSteps applies stratified sampling to cap a step slice at max.
// When len(steps) <= max, all steps are returned unchanged.
// When len(steps) > max, the result contains:
//   - Head: the first 5 steps (orientation context)
//   - Tail: the last 2 steps (final state)
//   - Middle: evenly-spaced steps from the remainder, up to max-7 slots
//
// The returned slice is always in ascending StepIndex order.
// ADR-run32: Prevents ThoughtStep token overflow in buildCompactedRecallContext.
func windowThoughtSteps(steps []memory.ThoughtStep, max int) []memory.ThoughtStep {
	if len(steps) <= max {
		return steps
	}

	const headCount = 5
	const tailCount = 2
	middleCount := max - headCount - tailCount
	if middleCount < 0 {
		middleCount = 0
	}

	head := steps[:headCount]
	tail := steps[len(steps)-tailCount:]
	middle := steps[headCount : len(steps)-tailCount]

	// Evenly sample middleCount steps from middle using stride.
	var sampled []memory.ThoughtStep
	if middleCount > 0 && len(middle) > 0 {
		if len(middle) <= middleCount {
			sampled = middle
		} else {
			stride := float64(len(middle)) / float64(middleCount)
			for i := 0; i < middleCount; i++ {
				idx := int(float64(i) * stride)
				if idx >= len(middle) {
					idx = len(middle) - 1
				}
				sampled = append(sampled, middle[idx])
			}
		}
	}

	result := make([]memory.ThoughtStep, 0, max)
	result = append(result, head...)
	result = append(result, sampled...)
	result = append(result, tail...)
	return result
}

// listFileChunk represents a single file's extracted content from List node output.
type listFileChunk struct {
	FilePath string
	Content  string
}

// splitListOutputIntoFileChunks splits List node output into per-file chunks
// by detecting "--- file:" boundary markers. Each chunk contains the content
// for a single source file, enabling per-file compaction that fits within the
// 4B model's context window.
func splitListOutputIntoFileChunks(output string) []listFileChunk {
	const divider = "--- file: "
	lines := strings.Split(output, "\n")

	var chunks []listFileChunk
	var currentPath string
	var currentLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, divider) {
			// Flush previous chunk
			if currentPath != "" && len(currentLines) > 0 {
				chunks = append(chunks, listFileChunk{
					FilePath: currentPath,
					Content:  strings.Join(currentLines, "\n"),
				})
			}
			// Extract file path from divider line: "--- file: /path/to/file lines: N-M ---"
			rest := line[len(divider):]
			if idx := strings.Index(rest, " lines: "); idx > 0 {
				currentPath = rest[:idx]
			} else {
				currentPath = strings.TrimSuffix(strings.TrimSpace(rest), " ---")
			}
			currentLines = []string{line} // Include the divider itself
		} else {
			currentLines = append(currentLines, line)
		}
	}

	// Flush final chunk
	if currentPath != "" && len(currentLines) > 0 {
		chunks = append(chunks, listFileChunk{
			FilePath: currentPath,
			Content:  strings.Join(currentLines, "\n"),
		})
	}

	// Merge chunks from the same file (List node may have multiple ranges per file)
	merged := make(map[string]*listFileChunk)
	var order []string
	for _, chunk := range chunks {
		if existing, ok := merged[chunk.FilePath]; ok {
			existing.Content += "\n" + chunk.Content
		} else {
			merged[chunk.FilePath] = &listFileChunk{
				FilePath: chunk.FilePath,
				Content:  chunk.Content,
			}
			order = append(order, chunk.FilePath)
		}
	}

	var result []listFileChunk
	for _, path := range order {
		result = append(result, *merged[path])
	}
	return result
}

// fanReduceSynthesis splits upstream output into file-level chunks, runs the
// downstream synthesis goal on each chunk (fan), then merges partial outputs (reduce).
// This replaces blind compaction with goal-directed synthesis on model-digestible chunks.
//
// The downstream goal comes from the compiler's StaticArgs injection — it's the
// task's GoalPrompt that the downstream synthesis node would use.
func fanReduceSynthesis(
	ctx context.Context,
	rawOutput string,
	downstreamGoal string,
	engine ProbeInferenceEngine,
) (string, error) {
	chunks := splitListOutputIntoFileChunks(rawOutput)
	if len(chunks) == 0 {
		return rawOutput, nil
	}

	// If only one chunk but it's too large, split within the file at function boundaries.
	if len(chunks) == 1 && len(chunks[0].Content) > 8000 {
		// Split at double-newline boundaries (function/type boundaries in extracted code)
		parts := strings.Split(chunks[0].Content, "\n\n")
		var subChunks []listFileChunk
		var current strings.Builder
		for _, part := range parts {
			if current.Len()+len(part) > 8000 && current.Len() > 0 {
				subChunks = append(subChunks, listFileChunk{
					FilePath: chunks[0].FilePath,
					Content:  current.String(),
				})
				current.Reset()
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(part)
		}
		if current.Len() > 0 {
			subChunks = append(subChunks, listFileChunk{
				FilePath: chunks[0].FilePath,
				Content:  current.String(),
			})
		}
		chunks = subChunks
	}

	fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Splitting into %d chunks for goal-directed synthesis\n", len(chunks))

	// Fan: run downstream synthesis on each chunk sequentially
	fanCtx := context.WithValue(ctx, inference.MaxTokensKey, 2048)
	fanCtx = context.WithValue(fanCtx, inference.GenerationGuardKey,
		inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

	var partialOutputs []string
	for i, chunk := range chunks {
		sysPrompt := fmt.Sprintf(`You are synthesizing documentation from source code.
Goal: %s

Source File: %s
Source Code:
%s

INSTRUCTIONS:
- Document ONLY exported symbols (names starting with an UPPERCASE letter in Go).
- SKIP all unexported/private symbols (names starting with a lowercase letter like sanitizeTableName, csvToJSON, compact, getRawPayload).
- For each exported symbol: write its full signature and a one-line description.
- Use markdown format with ### headings for each symbol.
- CRITICAL: Do NOT invent or hallucinate symbols that do not appear in the source code above. If a function is not in the source code, do NOT include it.
- Be concise and accurate. Only document what you can see.`, downstreamGoal, chunk.FilePath, chunk.Content)

		userPrompt := "Generate the documentation for the symbols in this file:"

		partial, err := engine.Infer(fanCtx, sysPrompt, userPrompt, "", TargetWorker)
		if err != nil {
			errStr := err.Error()
			// Detect sidecar crash: EOF or connection refused indicates the process died.
			if strings.Contains(errStr, "EOF") || strings.Contains(errStr, "connection refused") {
				fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Sidecar crash detected at chunk %d (%s). Attempting restart...\n", i, chunk.FilePath)
				// Attempt to restart the sidecar
				health := inference.GlobalWorkerModel.ProbeHealth(ctx)
				if health == inference.SidecarHealthDead {
					if restartErr := inference.GlobalWorkerModel.Start(ctx); restartErr != nil {
						fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Sidecar restart failed: %v\n", restartErr)
					} else {
						fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Sidecar restarted. Retrying chunk %d...\n", i)
						// Retry the chunk once after restart
						partial, err = engine.Infer(fanCtx, sysPrompt, userPrompt, "", TargetWorker)
					}
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Fan chunk %d (%s) failed: %v\n", i, chunk.FilePath, err)
				continue
			}
		}
		partial = strings.TrimSpace(partial)
		// Post-process: strip unexported (lowercase) symbol entries that the
		// 4B model includes despite the prompt instruction. Split on ### headings
		// and keep only those where the symbol name starts with uppercase.
		if strings.Contains(chunk.FilePath, ".go") && partial != "" {
			partial = stripUnexportedSymbolBlocks(partial)
		}
		if partial != "" {
			// Prepend file path so downstream extractors can locate source files.
			// Strip annotation suffixes like "(signature fallback)" from the path.
			cleanPath := chunk.FilePath
			if idx := strings.Index(cleanPath, " ("); idx > 0 {
				cleanPath = cleanPath[:idx]
			}
			partialOutputs = append(partialOutputs, fmt.Sprintf("<!-- source: %s -->\n%s", cleanPath, partial))
			fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Fan chunk %d (%s): %d chars output\n", i, chunk.FilePath, len(partial))
		}
	}

	if len(partialOutputs) == 0 {
		return rawOutput, fmt.Errorf("fan-reduce: all fan chunks failed")
	}

	// Reduce: merge partial outputs
	combined := strings.Join(partialOutputs, "\n\n---\n\n")

	// If the combined output is small enough, skip the merge inference call
	// and return the concatenated partials directly. The downstream DocGen
	// sectioned synthesis will restructure them into sections.
	// 24K threshold: allows 5-file fan outputs (~20K total) to pass through
	// without lossy merge compression. DocGen body sections extract per-section
	// symbols from the full context.
	if len(combined) <= 24000 {
		fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Combined partials (%d chars) within budget — returning directly\n", len(combined))
		return combined, nil
	}

	// Merge via inference for large combined outputs
	mergeSys := fmt.Sprintf(`You are merging partial documentation outputs into a single cohesive document.
Goal: %s

Partial outputs from individual source files:
%s

INSTRUCTIONS:
- Combine these partial documentation outputs into a single, well-structured document.
- Deduplicate any symbols that appear in multiple partials.
- Preserve all signatures and descriptions accurately.
- Do NOT add symbols that are not in the partials above.`, downstreamGoal, combined)

	mergeCtx := context.WithValue(ctx, inference.MaxTokensKey, 2048)
	mergeCtx = context.WithValue(mergeCtx, inference.GenerationGuardKey,
		inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

	merged, err := engine.Infer(mergeCtx, mergeSys, "Merge the partial outputs into a single document:", "", TargetWorker)
	if err != nil {
		// Fallback to raw concatenation on merge failure
		fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Merge inference failed: %v — returning concatenated partials\n", err)
		return combined, nil
	}

	fmt.Fprintf(os.Stderr, "[Recall/FanReduce] Merge complete: %d chars\n", len(merged))
	return strings.TrimSpace(merged), nil
}

// stripUnexportedSymbolBlocks removes documentation entries for unexported
// Go symbols from fan-reduce output. The 4B model consistently includes
// lowercase symbols despite prompt instructions; this structural filter
// strips them deterministically.
func stripUnexportedSymbolBlocks(text string) string {
	lines := strings.Split(text, "\n")
	var kept []string
	skipping := false
	stripped := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check ### or #### heading
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "#### ") {
			symbolName := extractSymbolFromHeading(trimmed)
			if symbolName != "" && !unicode.IsUpper(rune(symbolName[0])) {
				skipping = true
				stripped++
				continue
			}
			skipping = false
		}

		// Check table row: | symbolName | kind | file | ...
		if strings.HasPrefix(trimmed, "| ") && !strings.HasPrefix(trimmed, "| Symbol") && !strings.HasPrefix(trimmed, "|--") {
			symbolName := extractSymbolFromTableRow(trimmed)
			if symbolName != "" && !unicode.IsUpper(rune(symbolName[0])) {
				stripped++
				continue
			}
		}

		if !skipping {
			kept = append(kept, line)
		}
	}

	if stripped > 0 {
		fmt.Fprintf(os.Stderr, "[FanReduce/PostFilter] Stripped %d unexported symbol entries\n", stripped)
	}
	return strings.Join(kept, "\n")
}

// extractSymbolFromHeading extracts the symbol name from a ### or #### heading.
// Returns the symbol name that should be checked for exported status.
// For method headings (receiver.Method or (receiver).Method), returns the Method name.
func extractSymbolFromHeading(heading string) string {
	name := heading
	if strings.HasPrefix(name, "#### ") {
		name = strings.TrimPrefix(name, "#### ")
	} else if strings.HasPrefix(name, "### ") {
		name = strings.TrimPrefix(name, "### ")
	} else {
		return ""
	}
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "`")
	// Strip receiver with parens: "(r *Type) Method" or "(*Type).Method"
	if strings.HasPrefix(name, "(") {
		if idx := strings.Index(name, ") "); idx >= 0 {
			name = strings.TrimSpace(name[idx+2:])
		} else if idx := strings.Index(name, ")."); idx >= 0 {
			name = strings.TrimSpace(name[idx+2:])
		}
	}
	name = strings.TrimLeft(name, "*")
	// Handle dotted notation without parens: "Type.Method" → extract Method
	if dotIdx := strings.LastIndex(name, "."); dotIdx >= 0 && dotIdx < len(name)-1 {
		name = name[dotIdx+1:]
	}
	if idx := strings.IndexAny(name, "( "); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// extractSymbolFromTableRow extracts the symbol name from a markdown table row.
func extractSymbolFromTableRow(row string) string {
	parts := strings.SplitN(row, "|", 4)
	if len(parts) < 3 {
		return ""
	}
	name := strings.TrimSpace(parts[1])
	name = strings.Trim(name, "`")
	if strings.HasPrefix(name, "(") {
		if idx := strings.Index(name, ") "); idx >= 0 {
			name = strings.TrimSpace(name[idx+2:])
		} else if idx := strings.Index(name, ")."); idx >= 0 {
			name = strings.TrimSpace(name[idx+2:])
		}
	}
	name = strings.TrimLeft(name, "*")
	// Handle dotted notation: "Type.Method" → extract Method
	if dotIdx := strings.LastIndex(name, "."); dotIdx >= 0 && dotIdx < len(name)-1 {
		name = name[dotIdx+1:]
	}
	if idx := strings.IndexAny(name, "( "); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// deduplicateTableRows removes duplicate markdown table rows within a section.
// The 4B model frequently repeats symbols 2-3x in reference tables. This
// function deduplicates by the first column (symbol name), keeping the first
// occurrence of each symbol.
func deduplicateTableRows(section string) string {
	lines := strings.Split(section, "\n")
	var result []string
	seen := make(map[string]bool)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Only deduplicate table data rows (not headers or separators)
		if strings.HasPrefix(trimmed, "| ") && !strings.HasPrefix(trimmed, "| Symbol") && !strings.HasPrefix(trimmed, "|--") && !strings.HasPrefix(trimmed, "| -") {
			sym := extractSymbolFromTableRow(trimmed)
			if sym != "" {
				if seen[sym] {
					continue // skip duplicate
				}
				seen[sym] = true
			}
		}
		result = append(result, line)
	}

	if len(lines)-len(result) > 0 {
		fmt.Fprintf(os.Stderr, "[DocGen/Dedup] Removed %d duplicate table rows\n", len(lines)-len(result))
	}
	return strings.Join(result, "\n")
}
