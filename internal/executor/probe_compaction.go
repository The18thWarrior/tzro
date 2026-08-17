package executor

import (
	"context"
	"fmt"
	"os"
	"time"
	"tzro/internal/compactor"
	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// estimateConversationTokens provides a fast heuristic token count for a message
// array. Uses the ~4 chars/token approximation (standard for English text with
// code and JSON). This is used by the sliding window compaction to decide when
// to drop oldest turns — exact counts aren't needed, just a budget estimate.
func estimateConversationTokens(messages []inference.InferenceMessage) int {
	total := 0
	for _, m := range messages {
		// ~4 chars per token + overhead for role/template tokens
		total += len(m.Content)/4 + 4
	}
	return total
}

// slidingWindowCompact implements the sliding window strategy for append-only
// conversations (ADR-0056). When the estimated token count exceeds the budget,
// it drops the oldest user/assistant turn pairs (after the static prefix)
// while keeping the most recent turns that fit within the budget.
//
// Parameters:
//   - messages: the full conversation history
//   - staticPrefixLen: number of messages in the immutable prefix (system + upstream)
//   - budgetTokens: maximum estimated tokens for the conversation
//
// Returns the compacted message slice. If no compaction is needed, returns the
// original slice unchanged. When compaction occurs, a brief "[N earlier turns
// compacted]" marker is injected after the static prefix.
func slidingWindowCompact(messages []inference.InferenceMessage, staticPrefixLen, budgetTokens int) []inference.InferenceMessage {
	estimated := estimateConversationTokens(messages)
	if estimated <= budgetTokens {
		return messages
	}

	// Static prefix is immutable — only compact dynamic turns
	prefix := messages[:staticPrefixLen]
	dynamic := messages[staticPrefixLen:]

	if len(dynamic) <= 2 {
		// Can't compact further — only 1 turn pair remains
		return messages
	}

	// Drop oldest turn pairs (user + assistant) until we're within budget.
	// Keep dropping pairs from the front of dynamic turns.
	prefixTokens := estimateConversationTokens(prefix)
	droppedCount := 0

	for len(dynamic) > 2 {
		// Estimate tokens for prefix + remaining dynamic
		remainingTokens := prefixTokens
		for _, m := range dynamic {
			remainingTokens += len(m.Content)/4 + 4
		}
		if remainingTokens <= budgetTokens {
			break
		}

		// Drop the oldest pair (user + assistant) or single message
		if len(dynamic) >= 2 && dynamic[0].Role == "user" && dynamic[1].Role == "assistant" {
			dynamic = dynamic[2:]
			droppedCount += 2
		} else {
			dynamic = dynamic[1:]
			droppedCount++
		}
	}

	if droppedCount == 0 {
		return messages
	}

	fmt.Fprintf(os.Stderr, "[Probe] Sliding window compaction: dropped %d messages, keeping %d dynamic + %d prefix (est. %d → %d tokens)\n",
		droppedCount, len(dynamic), staticPrefixLen, estimated, estimateConversationTokens(append(prefix, dynamic...)))

	// Reassemble: prefix + compaction marker + remaining dynamic turns
	result := make([]inference.InferenceMessage, 0, staticPrefixLen+1+len(dynamic))
	result = append(result, prefix...)
	result = append(result, inference.InferenceMessage{
		Role:    "user",
		Content: fmt.Sprintf("[%d earlier exploration turns compacted to fit context window]", droppedCount/2),
	})
	result = append(result, inference.InferenceMessage{
		Role:    "assistant",
		Content: "Understood. I will continue exploration from the most recent context.",
	})
	result = append(result, dynamic...)

	return result
}

// compactThoughtChain creates a rolling summary of recent thought chain steps.
// The compactionLevel parameter is retained for API compatibility but
// the structured compactor handles content-type-aware compaction internally.
// Code tool outputs are deterministically skeletonized (signatures preserved).
// Model reasoning text is compressed via the router LLM.
func compactThoughtChain(ctx context.Context, probeID, taskID string, currentStep, window int, compactionLevel compiler.CompactionLevel, engine ProbeInferenceEngine) error {
	startStep := currentStep - window + 1
	if startStep < 1 {
		startStep = 1
	}

	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil {
		return err
	}

	// Collect steps in the compaction window
	var windowSteps []memory.ThoughtStep
	for _, s := range steps {
		if s.StepIndex >= startStep && s.StepIndex <= currentStep {
			windowSteps = append(windowSteps, s)
		}
	}

	if len(windowSteps) == 0 {
		return nil
	}

	// Convert to compactor steps.
	// Fix (ADR-benchmark-data-3): Steps whose ToolName is sql_cached_data or
	// introspect_cache have their ToolOutput preserved verbatim through
	// compaction. These outputs contain actual query result rows that the
	// 1B router model would otherwise strip as "verbose tabular content",
	// causing downstream synthesis to lose all data.
	hasCacheResults := false
	compactorSteps := make([]compactor.Step, len(windowSteps))
	for i, s := range windowSteps {
		toolOutput := s.ToolOutput
		isCacheTool := s.ToolName == "sql_cached_data" || s.ToolName == "introspect_cache"
		if isCacheTool && toolOutput != "" {
			hasCacheResults = true
		}
		compactorSteps[i] = compactor.Step{
			Index:      s.StepIndex,
			Thought:    s.Thought,
			ToolName:   s.ToolName,
			ToolArgs:   s.ToolArgs,
			ToolOutput: toolOutput,
		}
	}

	// ADR-benchmark-30: Disable LLM reasoning compression. Edge thoughts
	// contain non-actionable reasoning ("I should synthesize...") that the
	// router LLM inflates rather than compresses (50% of tasks in run 30
	// showed content inflation, e.g. 1,415→4,877 chars). Deterministic-only
	// mode preserves tool outputs (the actual value) without LLM overhead.
	// Tool outputs are always handled deterministically regardless of engine.
	var compactEngine compactor.CompactEngine // nil = deterministic only
	// Budget: reserve ~3K tokens for system prompt + recent steps + user prompt.
	// Compaction summary gets ~13K tokens of the router's 16K window ≈ ~52K chars.
	const compactionBudgetChars = 52000
	// Preserve tool output when explicitly set by compaction level OR when
	// the window contains cache query results that must survive for synthesis.
	preserveOutput := compactionLevel == compiler.CompactPreserve || hasCacheResults
	if hasCacheResults && compactionLevel != compiler.CompactPreserve {
		fmt.Fprintf(os.Stderr, "[Probe Compactor] Cache tool results detected in window — preserving tool outputs through compaction\n")
	}
	result, err := compactor.CompactSteps(ctx, compactorSteps, "", compactionBudgetChars, compactEngine, preserveOutput)

	// Fix 4: Post-compaction size validation — detect inflation and warn.
	// If compaction output exceeds input, the LLM reasoning compression is
	// inflating instead of compressing. Log a warning for diagnostics.
	if err == nil && result.OutputChars > result.InputChars {
		fmt.Fprintf(os.Stderr, "[Probe Compactor] WARNING: compaction inflated output (%d→%d chars, %.1fx). Router may be generating verbose responses.\n",
			result.InputChars, result.OutputChars, float64(result.OutputChars)/float64(result.InputChars))
	}
	if err != nil {
		return fmt.Errorf("structured compaction failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[Probe Compactor] Steps %d-%d: %d→%d chars (%d LLM calls)\n",
		startStep, currentStep, result.InputChars, result.OutputChars, result.LLMCalls)

	summary := memory.ThoughtSummary{
		ID:        fmt.Sprintf("%s_summary_%d_%d", probeID, startStep, currentStep),
		ProbeID:   probeID,
		TaskID:    taskID,
		StepRange: fmt.Sprintf("%d-%d", startStep, currentStep),
		Summary:   result.Output,
		CreatedAt: time.Now().Unix(),
	}

	return memory.DB.AddThoughtSummary(summary)
}
