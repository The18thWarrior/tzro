package executor

import (
	"sort"
	"testing"
)

func TestSplitListOutputIntoFileChunks_Basic(t *testing.T) {
	input := `--- file: /path/to/a.go lines: 1-20 ---
func Foo() {}

--- file: /path/to/b.go lines: 1-10 ---
func Bar() {}
`

	chunks := SplitListOutputIntoFileChunks(input)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	if chunks[0].FilePath != "/path/to/a.go" {
		t.Errorf("chunk 0 path: expected /path/to/a.go, got %s", chunks[0].FilePath)
	}
	if chunks[1].FilePath != "/path/to/b.go" {
		t.Errorf("chunk 1 path: expected /path/to/b.go, got %s", chunks[1].FilePath)
	}
}

func TestSplitListOutputIntoFileChunks_MergesSameFile(t *testing.T) {
	input := `--- file: /path/to/a.go lines: 1-10 ---
func Foo() {}

--- file: /path/to/a.go lines: 50-60 ---
func Bar() {}
`

	chunks := SplitListOutputIntoFileChunks(input)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 merged chunk, got %d", len(chunks))
	}
	if chunks[0].FilePath != "/path/to/a.go" {
		t.Errorf("expected /path/to/a.go, got %s", chunks[0].FilePath)
	}
}

func TestExpandChunksIntraFile_SplitsOversized(t *testing.T) {
	// Create a chunk with 3 paragraphs, each ~100 chars
	content := ""
	for i := 0; i < 3; i++ {
		if i > 0 {
			content += "\n\n"
		}
		for j := 0; j < 100; j++ {
			content += "x"
		}
	}

	chunks := []ListFileChunk{{FilePath: "/a.go", Content: content}}
	expanded := ExpandChunksIntraFile(chunks, 150)

	if len(expanded) < 2 {
		t.Errorf("expected at least 2 expanded chunks, got %d", len(expanded))
	}
	for _, c := range expanded {
		if c.FilePath != "/a.go" {
			t.Errorf("expanded chunk should preserve filepath, got %s", c.FilePath)
		}
	}
}

func TestExpandChunksIntraFile_PassesThroughSmallChunks(t *testing.T) {
	chunks := []ListFileChunk{
		{FilePath: "/a.go", Content: "short content"},
	}
	expanded := ExpandChunksIntraFile(chunks, 1000)
	if len(expanded) != 1 {
		t.Errorf("expected 1 chunk for small content, got %d", len(expanded))
	}
}

func TestFallbackBudgetTruncate(t *testing.T) {
	chunks := []ListFileChunk{
		{FilePath: "/a.go", Content: "aaaa"},
		{FilePath: "/b.go", Content: "bbbb"},
		{FilePath: "/c.go", Content: "cccc"},
	}

	result := fallbackBudgetTruncate(chunks, 10)
	if len(result) > 11 { // "aaaa\nbbbb\n" = 10
		t.Errorf("budget truncation exceeded: got %d chars", len(result))
	}
}

func TestEmbeddingPruneChunks_EmptyChunks(t *testing.T) {
	result := EmbeddingPruneChunks(nil, nil, "goal", 0.85, 0.20, 80000)
	if result != "" {
		t.Errorf("expected empty string for empty chunks, got %q", result)
	}
}

func TestListFileChunk_SortByOriginalOrder(t *testing.T) {
	// Validate the sort.Ints behavior used in EmbeddingPruneChunks
	indices := []int{5, 2, 8, 1}
	sort.Ints(indices)
	expected := []int{1, 2, 5, 8}
	for i, v := range indices {
		if v != expected[i] {
			t.Errorf("index %d: expected %d, got %d", i, expected[i], v)
		}
	}
}
