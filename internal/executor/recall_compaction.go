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
				chunks := SplitListOutputIntoFileChunks(out)
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

// isToolError checks whether tool output text indicates an execution failure.
func isToolError(toolOutput string) bool {
	lower := strings.ToLower(toolOutput)
	return strings.Contains(lower, "error:") ||
		strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "failed to") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "is a directory") ||
		strings.Contains(lower, "cannot open")
}

// stripUnexportedSymbolBlocks removes documentation entries for unexported
// Go symbols from synthesis output.
func stripUnexportedSymbolBlocks(text string) string {
	return stripUngroundedSymbolBlocks(text, nil, "")
}

// stripUngroundedSymbolBlocks removes documentation entries for unexported
// or ungrounded Go symbols from synthesis output. When allowedSyms or rawSourceCtx
// is provided, headings and table rows referencing symbols that do not exist in
// either the AST symbol map or verbatim in the raw source text are stripped.
func stripUngroundedSymbolBlocks(text string, allowedSyms map[string]bool, rawSourceCtx string) string {
	lines := strings.Split(text, "\n")
	var kept []string
	skipping := false
	stripped := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check ### or #### heading
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "#### ") {
			symbolName := extractSymbolFromHeading(trimmed)
			if symbolName != "" {
				// 1. Must be exported (first char uppercase in Go)
				if !unicode.IsUpper(rune(symbolName[0])) {
					skipping = true
					stripped++
					continue
				}
				// 2. Must be grounded in AST or verbatim in source context if reference data is available
				if len(allowedSyms) > 0 || len(rawSourceCtx) > 0 {
					grounded := false
					if allowedSyms != nil && allowedSyms[symbolName] {
						grounded = true
					} else if len(rawSourceCtx) > 0 && strings.Contains(rawSourceCtx, symbolName) {
						grounded = true
					}
					if !grounded {
						skipping = true
						stripped++
						continue
					}
				}
			}
			skipping = false
		}

		// Check table row: | symbolName | kind | file | ...
		if strings.HasPrefix(trimmed, "| ") && !strings.HasPrefix(trimmed, "| Symbol") && !strings.HasPrefix(trimmed, "|--") {
			symbolName := extractSymbolFromTableRow(trimmed)
			if symbolName != "" {
				if !unicode.IsUpper(rune(symbolName[0])) {
					stripped++
					continue
				}
				if len(allowedSyms) > 0 || len(rawSourceCtx) > 0 {
					grounded := false
					if allowedSyms != nil && allowedSyms[symbolName] {
						grounded = true
					} else if len(rawSourceCtx) > 0 && strings.Contains(rawSourceCtx, symbolName) {
						grounded = true
					}
					if !grounded {
						stripped++
						continue
					}
				}
			}
		}

		if !skipping {
			kept = append(kept, line)
		}
	}

	if stripped > 0 {
		fmt.Fprintf(os.Stderr, "[Synthesis/PostFilter] Stripped %d ungrounded/unexported symbol entries\n", stripped)
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

