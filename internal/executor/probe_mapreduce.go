package executor

import (
	"context"
	"fmt"
	"strings"
)

// MapReduceSynthesis handles content that exceeds the DirectSynthesis cap
// by chunking and map-reducing. For content within the cap, it passes through
// as a single DirectSynthesis call.
//
// Map phase: split content into chunks, summarize each independently.
// Reduce phase: synthesize sub-summaries into a final answer.
//
// This replaces the Thought Chain fallback for "aggregate" mode probes,
// reducing 100+ inference calls to N+1 (where N = number of chunks).
func MapReduceSynthesis(
	ctx context.Context,
	goal, content string,
	engine ProbeInferenceEngine,
	directSynthesisCap int,
) (string, error) {
	// Single-chunk passthrough: content fits DirectSynthesis cap
	if len(content) <= directSynthesisCap {
		systemPrompt := fmt.Sprintf(
			"You are a precise technical writer. Your goal: %s\n\n"+
				"Read the content below and produce a comprehensive, accurate response.",
			goal,
		)
		result, err := engine.Infer(ctx, systemPrompt, content, "")
		if err != nil {
			return "", fmt.Errorf("MapReduceSynthesis single-chunk failed: %w", err)
		}
		return result, nil
	}

	// Multi-chunk map-reduce
	chunks := splitBySize(content, directSynthesisCap)

	// Map phase: summarize each chunk
	var subSummaries []string
	for i, chunk := range chunks {
		systemPrompt := fmt.Sprintf(
			"You are summarizing chunk %d of %d for the following goal: %s\n\n"+
				"Produce a focused summary that captures the key information relevant to the goal.",
			i+1, len(chunks), goal,
		)
		summary, err := engine.Infer(ctx, systemPrompt, chunk, "")
		if err != nil {
			return "", fmt.Errorf("MapReduceSynthesis map phase chunk %d failed: %w", i+1, err)
		}
		subSummaries = append(subSummaries, summary)
	}

	// Reduce phase: synthesize sub-summaries into final answer
	combined := strings.Join(subSummaries, "\n\n---\n\n")
	systemPrompt := fmt.Sprintf(
		"You are synthesizing %d sub-summaries into a final comprehensive answer.\n"+
			"Goal: %s\n\n"+
			"Produce the final, unified response.",
		len(subSummaries), goal,
	)
	result, err := engine.Infer(ctx, systemPrompt, combined, "")
	if err != nil {
		return "", fmt.Errorf("MapReduceSynthesis reduce phase failed: %w", err)
	}
	return result, nil
}

// splitBySize splits content into chunks of approximately maxSize characters.
// Splits prefer line boundaries to avoid breaking mid-sentence.
func splitBySize(content string, maxSize int) []string {
	if len(content) <= maxSize {
		return []string{content}
	}

	var chunks []string
	remaining := content

	for len(remaining) > 0 {
		if len(remaining) <= maxSize {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good split point near maxSize (prefer newline boundary)
		splitAt := maxSize
		// Search backward for a newline
		if idx := strings.LastIndex(remaining[:splitAt], "\n"); idx > maxSize/2 {
			splitAt = idx + 1
		}

		chunks = append(chunks, remaining[:splitAt])
		remaining = remaining[splitAt:]
	}

	return chunks
}
