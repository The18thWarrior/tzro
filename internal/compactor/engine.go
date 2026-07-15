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
}

// RouterEngine uses the 1B router model for reasoning compression.
// Each chunk is ≤500 chars, well within the router's 16K context.
type RouterEngine struct{}

// CompactReasoning compresses a reasoning text chunk via the router model.
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

	result, err := inference.CallRouter(ctx, messages, "")
	if err != nil {
		return chunk, err // Fall back to original on error
	}
	if result == nil {
		return chunk, nil
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
