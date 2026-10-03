package context

import (
	stdctx "context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindDeclarations_RespectsGitignore(t *testing.T) {
	ws := t.TempDir()

	// Write .gitignore
	gitignoreContent := []byte("ignored_dir/\n")
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), gitignoreContent, 0644); err != nil {
		t.Fatalf("failed to write .gitignore: %v", err)
	}

	// Create ignored directory with a Go file declaring MySymbol
	ignoredDir := filepath.Join(ws, "ignored_dir")
	if err := os.MkdirAll(ignoredDir, 0755); err != nil {
		t.Fatalf("failed to create ignored_dir: %v", err)
	}
	ignoredFile := filepath.Join(ignoredDir, "ignored.go")
	ignoredCode := []byte("package main\n\nfunc MySymbol() {}\n")
	if err := os.WriteFile(ignoredFile, ignoredCode, 0644); err != nil {
		t.Fatalf("failed to write ignored file: %v", err)
	}

	// Create src directory with a Go file declaring MySymbol
	srcDir := filepath.Join(ws, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	srcFile := filepath.Join(srcDir, "valid.go")
	validCode := []byte("package main\n\nfunc MySymbol() {}\n")
	if err := os.WriteFile(srcFile, validCode, 0644); err != nil {
		t.Fatalf("failed to write valid file: %v", err)
	}

	results, err := FindDeclarations(stdctx.Background(), ws, "MySymbol")
	if err != nil {
		t.Fatalf("FindDeclarations failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected exactly 1 declaration, got %d: %+v", len(results), results)
	}

	if !strings.Contains(results[0].FilePath, "src") || strings.Contains(results[0].FilePath, "ignored_dir") {
		t.Fatalf("expected declaration to be from src/, got: %s", results[0].FilePath)
	}
}

func TestFindDeclarations_RespectsContextCancellation(t *testing.T) {
	ws := t.TempDir()

	// Create a Go file declaring SomeSymbol
	srcDir := filepath.Join(ws, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	file := filepath.Join(srcDir, "symbol.go")
	code := []byte("package main\n\nfunc SomeSymbol() {}\n")
	if err := os.WriteFile(file, code, 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()

	start := time.Now()
	results, err := FindDeclarations(ctx, ws, "SomeSymbol")
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Fatalf("expected FindDeclarations to return quickly (< 100ms), took %v", elapsed)
	}

	// Results should be empty or partial, and context cancellation error is swallowed
	if err != nil {
		t.Fatalf("unexpected error on cancelled context: %v", err)
	}
	if len(results) > 1 {
		t.Fatalf("expected at most 1 result, got %d", len(results))
	}
}

func TestFindDeclarations_CapsAtMaxDeclCandidates(t *testing.T) {
	ws := t.TempDir()

	// Create 60+ Go files each declaring HighFanout
	const totalFiles = 65
	srcDir := filepath.Join(ws, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}

	declCode := []byte("package main\n\nfunc HighFanout() {}\n")
	for i := 0; i < totalFiles; i++ {
		filePath := filepath.Join(srcDir, fmt.Sprintf("file_%02d.go", i))
		if err := os.WriteFile(filePath, declCode, 0644); err != nil {
			t.Fatalf("failed to write %s: %v", filePath, err)
		}
	}

	results, err := FindDeclarations(stdctx.Background(), ws, "HighFanout")
	if err != nil {
		t.Fatalf("FindDeclarations failed: %v", err)
	}

	if len(results) > maxDeclCandidates {
		t.Fatalf("expected at most %d candidates, got %d", maxDeclCandidates, len(results))
	}

	if len(results) != maxDeclCandidates {
		t.Fatalf("expected exactly %d candidates (cap reached), got %d", maxDeclCandidates, len(results))
	}
}

func TestResolveSymbolAnchor_RespectsContextTimeout(t *testing.T) {
	ws := t.TempDir()

	srcDir := filepath.Join(ws, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	file := filepath.Join(srcDir, "symbol.go")
	code := []byte("package main\n\nfunc SomeSymbol() {}\n")
	if err := os.WriteFile(file, code, 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()

	start := time.Now()
	_, err := ResolveSymbolAnchor(ctx, ws, "SomeSymbol", "")
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Fatalf("expected ResolveSymbolAnchor to return quickly (< 100ms), took %v", elapsed)
	}

	if err != nil {
		t.Fatalf("unexpected error on cancelled context: %v", err)
	}
}

func TestFindDeclarations_NoGitignoreStillWorks(t *testing.T) {
	ws := t.TempDir()

	srcDir := filepath.Join(ws, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	file := filepath.Join(srcDir, "main.go")
	code := []byte("package main\n\nfunc MySymbol() {}\n")
	if err := os.WriteFile(file, code, 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	results, err := FindDeclarations(stdctx.Background(), ws, "MySymbol")
	if err != nil {
		t.Fatalf("FindDeclarations failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 declaration, got %d: %+v", len(results), results)
	}

	if results[0].Name != "MySymbol" {
		t.Fatalf("expected declaration name to be MySymbol, got %s", results[0].Name)
	}
}
