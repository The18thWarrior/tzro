package index

import (
	"os"
	"path/filepath"
	"testing"
	"tzro/internal/symbols"
)

func TestIndexStore_TracerBullet(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tzro-index-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "index.db")
	store, err := NewIndexStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create index store: %v", err)
	}
	defer store.Close()

	// 1. Upsert code file with symbols and call edges
	codeSymbols := []symbols.Symbol{
		{
			Name:       "CompileAndSort",
			Kind:       symbols.SymbolFunc,
			Signature:  "func CompileAndSort(graph *AbstractGraph) (*ExecutionGraph, error)",
			DocComment: "CompileAndSort topologically sorts the abstract graph.",
			File:       "internal/compiler/sct_compiler.go",
			Line:       42,
			EndLine:    120,
			Exported:   true,
		},
		{
			Name:       "ExecutionGraph",
			Kind:       symbols.SymbolType,
			Signature:  "type ExecutionGraph struct",
			DocComment: "ExecutionGraph represents the compiled DAG.",
			File:       "internal/compiler/sct_compiler.go",
			Line:       15,
			EndLine:    30,
			Exported:   true,
		},
	}
	codeEdges := []symbols.CallEdge{
		{
			CallerName: "CompileAndSort",
			CalleeName: "validateGraph",
			CallerFile: "internal/compiler/sct_compiler.go",
			CalleeFile: "internal/compiler/sct_compiler.go",
			CallLine:   55,
			EdgeKind:   "direct",
		},
	}

	err = store.UpsertCodeFile("internal/compiler/sct_compiler.go", codeSymbols, codeEdges, "hash-abc-123")
	if err != nil {
		t.Fatalf("UpsertCodeFile failed: %v", err)
	}

	// 2. Upsert document chunks
	docChunks := []DocChunk{
		{
			ID:         "adr-0086-sec-1",
			FilePath:   "docs/adr/0086-pre-index.md",
			Kind:       "doc_section",
			Header:     "Decision",
			Content:    "We introduce the Repository Pre-Index with Dual-Plane Indexing.",
			SymbolRefs: []string{"CompileAndSort", "ExecutionGraph"},
		},
	}

	err = store.UpsertDocChunks("docs/adr/0086-pre-index.md", docChunks, "hash-doc-456")
	if err != nil {
		t.Fatalf("UpsertDocChunks failed: %v", err)
	}

	// 3. Verify file hash queries for staleness check
	hash, exists, err := store.GetFileHash("internal/compiler/sct_compiler.go")
	if err != nil {
		t.Fatalf("GetFileHash failed: %v", err)
	}
	if !exists || hash != "hash-abc-123" {
		t.Errorf("expected hash-abc-123, got %s (exists=%v)", hash, exists)
	}

	// 4. Verify FTS5 keyword search
	ftsMatches, err := store.SearchFTS("CompileAndSort", 10)
	if err != nil {
		t.Fatalf("SearchFTS failed: %v", err)
	}
	if len(ftsMatches) < 1 {
		t.Fatalf("expected at least 1 match for CompileAndSort, got %d", len(ftsMatches))
	}
	found := false
	for _, m := range ftsMatches {
		if m.Title == "CompileAndSort" || m.FilePath == "docs/adr/0086-pre-index.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CompileAndSort or ADR in FTS matches, got %+v", ftsMatches)
	}
}
