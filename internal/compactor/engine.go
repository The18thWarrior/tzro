package compactor

// engine.go — CompactEngine interface and implementations.
//
// CompactEngine abstracts LLM inference for reasoning text compression.
// The RouterEngine routes through the 1B router model for fast, cheap
// compression of model reasoning text.

import (
	"context"
	"tzro/internal/inference"
)

// CompactEngine abstracts LLM inference for reasoning text compression.
// Only the model's own reasoning text (Thought field) is LLM-compressed.
// Tool outputs are always handled deterministically.
type CompactEngine interface {
	// CompactReasoning compresses a chunk of model reasoning text
	// into its key conclusion. Chunks are typically ≤500 chars.
	CompactReasoning(ctx context.Context, chunk string) (string, error)

	// CompactToolOutput summarizes web/text tool output into a bulleted
	// fact list. Used by the Recall Node Refinement Pass (ADR-0064).
	// Code segments are never sent here — they use deterministic skeletons.
	CompactToolOutput(ctx context.Context, content string) (string, error)

	// ExtractWebFacts extracts structured facts from web-browsed content.
	// Returns a structured list of claims with source attribution.
	// This forces the model to commit to specific claims from source
	// material before synthesis, preventing parametric bias.
	ExtractWebFacts(ctx context.Context, content string, sourceURL string) (string, error)
}

// RouterEngine uses the 1B router model for reasoning compression.
// Each chunk is ≤500 chars, well within the router's 16K context.
type RouterEngine struct{}

// CompactReasoning compresses a reasoning text chunk via the router model.
// Generation is capped at 256 tokens to prevent inflation: a ~500-char input
// chunk (~125 tokens) should compress to fewer tokens, not expand to 4096.
func (r *RouterEngine) CompactReasoning(ctx context.Context, chunk string) (string, error) {
	messages := []inference.InferenceMessage{
		{
			Role:    "system",
			Content: "Compress this reasoning into its key conclusion. Output only the conclusion, no preamble.",
		},
		{
			Role:    "user",
			Content: chunk,
		},
	}

	// Cap generation to prevent inflation: compaction output must be shorter
	// than input. 256 tokens ≈ ~1000 chars, generous for a 500-char input chunk.
	cappedCtx := context.WithValue(ctx, inference.MaxTokensKey, 256)
	result, err := inference.CallRouter(cappedCtx, messages, "")
	if err != nil {
		return chunk, err // Fall back to original on error
	}
	if result == nil {
		return chunk, nil
	}
	return result.Content, nil
}

// CompactToolOutput summarizes web/text tool output into a bulleted fact list.
// Uses the router model with a fact-extraction prompt (ADR-0064).
func (r *RouterEngine) CompactToolOutput(ctx context.Context, content string) (string, error) {
	messages := []inference.InferenceMessage{
		{
			Role:    "system",
			Content: "Extract all factual claims, statistics, names, comparisons, and URLs from this text. Output as a bulleted list of facts. Omit opinions, navigation text, and boilerplate.",
		},
		{
			Role:    "user",
			Content: content,
		},
	}

	// Cap generation: fact list should be much shorter than source
	cappedCtx := context.WithValue(ctx, inference.MaxTokensKey, 512)
	result, err := inference.CallRouter(cappedCtx, messages, "")
	if err != nil {
		return "", err
	}
	if result == nil {
		return content, nil
	}
	return result.Content, nil
}

// ExtractWebFacts extracts structured facts from web-browsed content.
// Uses the router model with a constrained extraction prompt that forces
// the model to output structured claim → source → quote triples.
func (r *RouterEngine) ExtractWebFacts(ctx context.Context, content string, sourceURL string) (string, error) {
	sourceInfo := "the source document"
	if sourceURL != "" {
		sourceInfo = sourceURL
	}

	messages := []inference.InferenceMessage{
		{
			Role: "system",
			Content: `Extract ALL factual claims from this web page content. For each fact, output:
- CLAIM: [specific factual statement]
- SOURCE: [URL or document name]
- QUOTE: [verbatim quote from the source that supports this claim]

Rules:
1. Extract ONLY facts that appear in the source text. Do NOT add information from your own knowledge.
2. Include statistics, dates, names, comparisons, version numbers, and quantitative claims.
3. Preserve exact numbers, names, and URLs from the source.
4. Skip navigation text, advertisements, cookie notices, and boilerplate.`,
		},
		{
			Role:    "user",
			Content: "Source: " + sourceInfo + "\n\n" + content,
		},
	}

	cappedCtx := context.WithValue(ctx, inference.MaxTokensKey, 1024)
	result, err := inference.CallRouter(cappedCtx, messages, "")
	if err != nil {
		return "", err
	}
	if result == nil {
		return content, nil
	}
	return result.Content, nil
}

// PassthroughEngine returns input unchanged. Used for tests and
// deterministic-only compaction mode.
type PassthroughEngine struct{}

// CompactReasoning returns the chunk unchanged.
func (p *PassthroughEngine) CompactReasoning(_ context.Context, chunk string) (string, error) {
	return chunk, nil
}

// CompactToolOutput returns the content unchanged.
func (p *PassthroughEngine) CompactToolOutput(_ context.Context, content string) (string, error) {
	return content, nil
}

// ExtractWebFacts returns the content unchanged.
func (p *PassthroughEngine) ExtractWebFacts(_ context.Context, content string, _ string) (string, error) {
	return content, nil
}
