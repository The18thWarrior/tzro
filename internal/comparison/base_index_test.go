package comparison

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tzro/internal/index"
)

func TestBaseProjectIndexAndCopy(t *testing.T) {
	tmpRoot, err := os.MkdirTemp("", "tzro-base-index-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpRoot)

	// Create sample project structure
	_ = os.WriteFile(filepath.Join(tmpRoot, "sample.go"), []byte("package main\nfunc TestSymbol() {}\n"), 0644)

	ctx := context.Background()
	basePath, err := EnsureBaseProjectIndex(ctx, tmpRoot, nil)
	if err != nil {
		t.Fatalf("EnsureBaseProjectIndex failed: %v", err)
	}
	if basePath == "" {
		t.Fatalf("expected non-empty basePath")
	}

	// Verify subsequent call returns the same cached path
	basePath2, err := EnsureBaseProjectIndex(ctx, tmpRoot, nil)
	if err != nil || basePath2 != basePath {
		t.Fatalf("expected same cached path %s, got %s (err: %v)", basePath, basePath2, err)
	}

	// Verify CopyIndexDB creates an isolated, functional copy
	taskDir, err := os.MkdirTemp("", "tzro-task-test-*")
	if err != nil {
		t.Fatalf("failed to create task dir: %v", err)
	}
	defer os.RemoveAll(taskDir)

	taskDBPath := filepath.Join(taskDir, ".tzro", "index.db")
	if err := CopyIndexDB(basePath, taskDBPath); err != nil {
		t.Fatalf("CopyIndexDB failed: %v", err)
	}

	// Open the copied index store and verify it contains the base symbols
	taskStore, err := index.NewIndexStore(taskDBPath)
	if err != nil {
		t.Fatalf("failed to open copied store: %v", err)
	}
	defer taskStore.Close()

	hits, err := taskStore.SearchFTS("TestSymbol", 5)
	if err != nil {
		t.Fatalf("SearchFTS failed on copied store: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected search hit for 'TestSymbol' in cloned store, got 0")
	}

	// Clean up base index
	ResetBaseProjectIndex()
	if _, err := os.Stat(basePath); !os.IsNotExist(err) {
		t.Fatalf("expected base index file to be removed after reset")
	}
}
