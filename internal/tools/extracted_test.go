package tools

import (
	"context"
	"testing"

	"tzro/internal/content"
)

func TestExtractedHolder_NewAndRetrieve(t *testing.T) {
	ctx := context.Background()

	// Without injection, should return nil
	holder := ExtractedFromCtx(ctx)
	if holder != nil {
		t.Fatal("expected nil holder on plain context")
	}

	// Inject holder
	ctx, holder = NewExtractedCtx(ctx)
	if holder == nil {
		t.Fatal("expected non-nil holder after injection")
	}
	if holder.Content != nil {
		t.Fatal("expected nil Content on fresh holder")
	}

	// Simulate tool setting content
	holder.Content = &content.ExtractedContent{
		Type: content.ContentImage,
		Text: "A bar chart",
	}

	// Retrieve from context
	retrieved := ExtractedFromCtx(ctx)
	if retrieved == nil {
		t.Fatal("expected non-nil holder from context")
	}
	if retrieved.Content == nil {
		t.Fatal("expected non-nil Content after setting")
	}
	if retrieved.Content.Text != "A bar chart" {
		t.Errorf("expected 'A bar chart', got '%s'", retrieved.Content.Text)
	}
}

func TestExtractedHolder_MultipleContextLayers(t *testing.T) {
	ctx := context.Background()
	ctx, holder1 := NewExtractedCtx(ctx)

	// Add another context layer on top
	ctx = context.WithValue(ctx, FileReadGoalKey, "test goal")

	// Should still be able to retrieve the holder through layers
	retrieved := ExtractedFromCtx(ctx)
	if retrieved != holder1 {
		t.Fatal("expected same holder through context layers")
	}
}
