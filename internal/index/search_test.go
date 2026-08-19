package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"tzro/internal/symbols"
)

// mockEmbedder implements the Embedder interface for testing vector searches
type mockEmbedder struct {
	embeddings map[string][]float32
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if emb, ok := m.embeddings[text]; ok {
		return emb, nil
	}
	// Default synthetic embedding
	return []float32{0.1, 0.2, 0.3, 0.4}, nil
}

func TestIndexStore_HybridSearch_RRF(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tzro-search-test-*")
	if err != nil {
		t.Fatalf("temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewIndexStore(filepath.Join(tmpDir, "index.db"))
	if err != nil {
		t.Fatalf("NewIndexStore failed: %v", err)
	}
	defer store.Close()

	// 1. Insert code symbols
	syms := []symbols.Symbol{
		{
			Name:       "ResolveDynamicBindings",
			Kind:       symbols.SymbolFunc,
			Signature:  "func ResolveDynamicBindings(bindings []Binding) map[string]any",
			DocComment: "Extracts parameter references from completed nodes.",
			File:       "internal/compiler/binding.go",
			Line:       10,
			Exported:   true,
		},
	}
	if err := store.UpsertCodeFile("internal/compiler/binding.go", syms, nil, "hash-code-1"); err != nil {
		t.Fatalf("UpsertCodeFile failed: %v", err)
	}

	// 2. Insert document chunk with conceptual synonym (no exact keyword match with "ResolveDynamicBindings")
	docChunks := []DocChunk{
		{
			ID:         "adr-bindings#s1",
			FilePath:   "docs/adr/0030-bindings.md",
			Kind:       "doc_section",
			Header:     "Parameter Propagation Across Nodes",
			Content:    "Downstream steps extract values from upstream JSON payloads using reactive slot splicing.",
			SymbolRefs: []string{"ResolveDynamicBindings"},
			Embedding:  []float32{0.9, 0.8, 0.1, 0.0}, // Matches query embedding closely
		},
	}
	if err := store.UpsertDocChunks("docs/adr/0030-bindings.md", docChunks, "hash-doc-1"); err != nil {
		t.Fatalf("UpsertDocChunks failed: %v", err)
	}

	mock := &mockEmbedder{
		embeddings: map[string][]float32{
			"how does parameter propagation work between nodes?": {0.9, 0.8, 0.1, 0.0},
		},
	}

	// 3. Perform Hybrid Search
	ctx := context.Background()
	results, err := store.HybridSearch(ctx, "how does parameter propagation work between nodes?", mock, 10)
	if err != nil {
		t.Fatalf("HybridSearch failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("expected search results, got 0")
	}

	// Verify that the semantic doc hit is ranked top due to vector cosine match
	topHit := results[0]
	if topHit.FilePath != "docs/adr/0030-bindings.md" {
		t.Errorf("expected ADR top hit via semantic similarity, got %s (Title: %s)", topHit.FilePath, topHit.Title)
	}
	if topHit.Score <= 0 {
		t.Errorf("expected positive RRF score, got %f", topHit.Score)
	}
}
