package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"tzro/internal/compiler"
	"tzro/internal/index"
	"tzro/internal/symbols"
)

func TestProbePreflight_RepositoryIndex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tzro-probe-index-test-*")
	if err != nil {
		t.Fatalf("temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "index.db")
	store, err := index.NewIndexStore(dbPath)
	if err != nil {
		t.Fatalf("NewIndexStore failed: %v", err)
	}
	defer store.Close()

	// Insert test data in index
	syms := []symbols.Symbol{
		{
			Name:       "TopologicalSort",
			Kind:       symbols.SymbolFunc,
			Signature:  "func TopologicalSort(nodes []*Node) ([]*Node, error)",
			DocComment: "Sorts execution nodes in Kahn order.",
			File:       "internal/compiler/kahn.go",
			Line:       12,
			Exported:   true,
		},
	}
	_ = store.UpsertCodeFile("internal/compiler/kahn.go", syms, nil, "hash-1")

	// Set global index
	index.SetGlobalIndex(store)
	defer index.SetGlobalIndex(nil)

	// Create test ProbeConfig
	probeConfig := &compiler.ProbeConfig{
		Goal: "Explain how TopologicalSort orders nodes in the compiler",
	}

	promoted, contextContent, err := TryIndexPreflight(context.Background(), probeConfig, nil)
	if err != nil {
		t.Fatalf("TryIndexPreflight failed: %v", err)
	}

	if !promoted {
		t.Fatalf("expected Probe to be promoted to DirectSynthesis via index preflight")
	}

	if len(contextContent) == 0 {
		t.Errorf("expected non-empty context content from packed index")
	}

	// Verify low confidence / unrelated query returns promoted=false
	unrelatedConfig := &compiler.ProbeConfig{
		Goal: "Some completely unindexed external xyz subject",
	}
	promotedUnrelated, _, _ := TryIndexPreflight(context.Background(), unrelatedConfig, nil)
	if promotedUnrelated {
		t.Errorf("expected unrelated query to not be promoted")
	}
}
