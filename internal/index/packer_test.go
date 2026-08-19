package index

import (
	"testing"
)

func TestPackContextBudget(t *testing.T) {
	results := []SearchResult{
		{
			ID:         "code-1",
			FilePath:   "internal/compiler/sct_compiler.go",
			Kind:       "func",
			Title:      "CompileAndSort",
			Signature:  "func CompileAndSort(graph *AbstractGraph) (*ExecutionGraph, error)",
			Content:    "Topological sorting of abstract graph into Kahn DAG execution layers.",
			Score:      0.030, // High RRF score
			SourceType: "code",
		},
		{
			ID:         "doc-1",
			FilePath:   "docs/adr/0086.md",
			Kind:       "doc_section",
			Title:      "Decision",
			Signature:  "CompileAndSort ExecutionGraph",
			Content:    "We introduce the Repository Pre-Index with Dual-Plane Indexing.",
			Score:      0.025,
			SourceType: "doc",
		},
		{
			ID:         "noise-low-score",
			FilePath:   "internal/misc/random.go",
			Kind:       "func",
			Title:      "RandomFunc",
			Signature:  "func RandomFunc()",
			Content:    "Irrelevant helper.",
			Score:      0.001, // Below floor
			SourceType: "code",
		},
	}

	// 1. Pack with a token budget of 500 tokens and minScore of 0.010
	packed := PackContextBudget(results, 0.010, 500)

	if packed.ItemsCount != 2 {
		t.Fatalf("expected 2 items packed (filtering out noise), got %d", packed.ItemsCount)
	}

	if packed.TokensUsed <= 0 {
		t.Errorf("expected positive TokensUsed, got %d", packed.TokensUsed)
	}

	// Verify formatted context buffer contains both sections
	if len(packed.Buffer) == 0 {
		t.Fatalf("expected non-empty context buffer")
	}

	// 2. Pack with tight budget that only fits 1 item
	tightPacked := PackContextBudget(results, 0.010, 30) // ~120 chars
	if tightPacked.ItemsCount != 1 {
		t.Errorf("expected tight budget to pack exactly 1 item, got %d", tightPacked.ItemsCount)
	}
}
