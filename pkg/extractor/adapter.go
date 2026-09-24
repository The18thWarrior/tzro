package extractor

import (
	"context"

	"tzro/pkg/executor"
)

// GLiNERAdapter bridges the extractor.WorkerClient to the executor.Extractor interface,
// allowing the graph execution engine to use GLiNER for zero-shot span extraction.
type GLiNERAdapter struct {
	client *WorkerClient
}

// NewGLiNERAdapter wraps a WorkerClient to satisfy executor.Extractor.
func NewGLiNERAdapter(client *WorkerClient) *GLiNERAdapter {
	return &GLiNERAdapter{client: client}
}

// Extract delegates to the WorkerClient and converts the response
// from extractor.ExtractedSpan to executor.ExtractedSpan.
func (g *GLiNERAdapter) Extract(ctx context.Context, text string, labels []string) ([]executor.ExtractedSpan, error) {
	resp, err := g.client.Extract(ctx, &ExtractionRequest{
		Text:   text,
		Labels: labels,
	})
	if err != nil {
		return nil, err
	}

	spans := make([]executor.ExtractedSpan, len(resp.Spans))
	for i, s := range resp.Spans {
		spans[i] = executor.ExtractedSpan{
			Label:      s.Label,
			Text:       s.Text,
			Start:      s.Start,
			End:        s.End,
			Confidence: s.Confidence,
		}
	}
	return spans, nil
}
