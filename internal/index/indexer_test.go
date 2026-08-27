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

func TestScanAndIndexWorkspace_Filters(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tzro-filter-test-*")
	if err != nil {
		t.Fatalf("temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Valid Go file
	_ = os.WriteFile(filepath.Join(tmpDir, "valid.go"), []byte("package main\nfunc Hello() {}\n"), 0644)

	// Ignored folder (static)
	staticDir := filepath.Join(tmpDir, "static")
	_ = os.MkdirAll(staticDir, 0755)
	_ = os.WriteFile(filepath.Join(staticDir, "bundle.js"), []byte("console.log('static');"), 0644)

	// Minified file
	_ = os.WriteFile(filepath.Join(tmpDir, "app.min.js"), []byte("console.log('min');"), 0644)

	// Huge file (>512KB)
	hugeFile := filepath.Join(tmpDir, "huge.go")
	hugeContent := make([]byte, 600*1024)
	_ = os.WriteFile(hugeFile, hugeContent, 0644)

	dbPath := filepath.Join(tmpDir, ".tzro", "index.db")
	store, err := NewIndexStore(dbPath)
	if err != nil {
		t.Fatalf("NewIndexStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	count, err := ScanAndIndexWorkspace(ctx, tmpDir, store, nil)
	if err != nil {
		t.Fatalf("ScanAndIndexWorkspace failed: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly 1 indexed file (valid.go), got %d", count)
	}
}
