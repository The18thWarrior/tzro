package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanAndIndexWorkspace(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tzro-scan-test-*")
	if err != nil {
		t.Fatalf("temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create sample code file
	pkgDir := filepath.Join(tmpDir, "pkg", "math")
	_ = os.MkdirAll(pkgDir, 0755)
	codeFile := filepath.Join(pkgDir, "calc.go")
	codeContent := `package math

// Add adds two numbers together.
func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(codeFile, []byte(codeContent), 0644); err != nil {
		t.Fatalf("writing code file: %v", err)
	}

	// Create sample doc file
	docsDir := filepath.Join(tmpDir, "docs")
	_ = os.MkdirAll(docsDir, 0755)
	docFile := filepath.Join(docsDir, "guide.md")
	docContent := `# Math Guide

Overview of math calculations in ` + "`math`" + ` package.

## Addition

Explains ` + "`Add`" + ` function operation.
`
	if err := os.WriteFile(docFile, []byte(docContent), 0644); err != nil {
		t.Fatalf("writing doc file: %v", err)
	}

	dbPath := filepath.Join(tmpDir, ".tzro", "index.db")
	store, err := NewIndexStore(dbPath)
	if err != nil {
		t.Fatalf("NewIndexStore failed: %v", err)
	}
	defer store.Close()

	// Run scanner
	ctx := context.Background()
	count, err := ScanAndIndexWorkspace(ctx, tmpDir, store, nil)
	if err != nil {
		t.Fatalf("ScanAndIndexWorkspace failed: %v", err)
	}
	if count < 2 {
		t.Fatalf("expected at least 2 indexed files, got %d", count)
	}

	// Verify querying indexed files
	res, err := store.SearchFTS("Add", 5)
	if err != nil {
		t.Fatalf("SearchFTS failed: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected search hits for 'Add', got 0")
	}
}
