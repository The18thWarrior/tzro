package ast

import (
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestSkeletonize_Go(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	src := []byte(`package math

import "fmt"

// Add computes the sum of two integers.
func Add(a, b int) int {
	fmt.Println("Adding numbers")
	res := a + b
	return res
}

func Multiply(a, b int) int {
	return a * b
}
`)

	res, err := Skeletonize("math.go", src, s)
	if err != nil {
		t.Fatalf("Skeletonize failed: %v", err)
	}

	if res.ElidedBlocks != 2 {
		t.Errorf("expected 2 elided blocks, got %d", res.ElidedBlocks)
	}

	if !strings.Contains(res.SkeletonCode, "func Add(a, b int) int") {
		t.Errorf("expected Add signature to be preserved")
	}

	if strings.Contains(res.SkeletonCode, "fmt.Println") {
		t.Errorf("expected function body to be elided")
	}

	if !strings.Contains(res.SkeletonCode, "[body elided: #") {
		t.Errorf("expected body elision hash tag")
	}

	// Verify the body was stored in SQLite
	symbols, err := s.SearchSymbols("Add", 10)
	if err != nil || len(symbols) == 0 {
		t.Fatalf("expected Add to be indexed in store, got %v", symbols)
	}

	blob, err := s.GetBlob(symbols[0].Hash)
	if err != nil {
		t.Fatalf("GetBlob failed: %v", err)
	}
	if !strings.Contains(blob.Body, "fmt.Println") {
		t.Errorf("expected retrieved blob to contain original body")
	}
}

func TestSkeletonize_Python(t *testing.T) {
	src := []byte(`def calculate_total(items):
    """Calculate total price."""
    total = 0
    for item in items:
        total += item.price
    return total
`)

	res, err := Skeletonize("calc.py", src, nil)
	if err != nil {
		t.Fatalf("Skeletonize failed: %v", err)
	}

	if res.ElidedBlocks != 1 {
		t.Errorf("expected 1 elided block, got %d", res.ElidedBlocks)
	}

	if !strings.Contains(res.SkeletonCode, "def calculate_total(items):") {
		t.Errorf("expected signature preserved")
	}

	if strings.Contains(res.SkeletonCode, "total += item.price") {
		t.Errorf("expected python body elided")
	}
}

func TestSkeletonize_TypeScript(t *testing.T) {
	src := []byte(`export class AuthService {
  public validateToken(token: string): boolean {
    const decoded = jwt.decode(token);
    return decoded !== null;
  }
}
`)

	res, err := Skeletonize("auth.ts", src, nil)
	if err != nil {
		t.Fatalf("Skeletonize failed: %v", err)
	}

	if res.ElidedBlocks != 1 {
		t.Errorf("expected 1 elided block, got %d", res.ElidedBlocks)
	}

	if !strings.Contains(res.SkeletonCode, "public validateToken(token: string): boolean") {
		t.Errorf("expected TS method signature preserved")
	}

	if strings.Contains(res.SkeletonCode, "jwt.decode") {
		t.Errorf("expected TS body elided")
	}
}

func TestSkeletonize_Markdown(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Build a markdown file with:
	// - Headings (should be preserved)
	// - A short paragraph (should be preserved)
	// - A long fenced code block (should be elided)
	// - A long paragraph (should be elided)
	longCode := strings.Repeat("    fmt.Println(\"line of code\")\n", 20)
	longParagraph := strings.Repeat("This is a sentence that goes on and on with many words. ", 15)

	src := []byte("# Project Title\n\nShort intro paragraph.\n\n## Installation\n\n```go\npackage main\n\n" + longCode + "```\n\n## Description\n\n" + longParagraph + "\n\n## License\n\nMIT\n")

	res, err := Skeletonize("README.md", src, s)
	if err != nil {
		t.Fatalf("Skeletonize failed: %v", err)
	}

	// Headings must be preserved
	if !strings.Contains(res.SkeletonCode, "# Project Title") {
		t.Errorf("expected h1 heading preserved")
	}
	if !strings.Contains(res.SkeletonCode, "## Installation") {
		t.Errorf("expected h2 heading preserved")
	}
	if !strings.Contains(res.SkeletonCode, "## Description") {
		t.Errorf("expected h2 Description heading preserved")
	}
	if !strings.Contains(res.SkeletonCode, "## License") {
		t.Errorf("expected h2 License heading preserved")
	}

	// Short paragraph preserved
	if !strings.Contains(res.SkeletonCode, "Short intro paragraph.") {
		t.Errorf("expected short paragraph preserved")
	}

	// MIT paragraph preserved (short)
	if !strings.Contains(res.SkeletonCode, "MIT") {
		t.Errorf("expected MIT preserved")
	}

	// Fenced code block body should be elided
	if strings.Contains(res.SkeletonCode, "fmt.Println") {
		t.Errorf("expected fenced code block body to be elided")
	}
	if !strings.Contains(res.SkeletonCode, "[body elided: #") {
		t.Errorf("expected code block elision marker")
	}

	// Long paragraph should be elided
	if strings.Count(res.SkeletonCode, "goes on and on") > 2 {
		t.Errorf("expected long paragraph to be elided, but most content remains")
	}
	if !strings.Contains(res.SkeletonCode, "[paragraph elided: #") {
		t.Errorf("expected paragraph elision marker")
	}

	// Should have at least 2 elided blocks (code + paragraph)
	if res.ElidedBlocks < 2 {
		t.Errorf("expected at least 2 elided blocks, got %d", res.ElidedBlocks)
	}

	// Savings should be significant
	if res.SavingsRatio < 0.3 {
		t.Errorf("expected >30%% savings, got %.1f%%", res.SavingsRatio*100)
	}

	// Verify expand round-trip via store
	if len(res.Hashes) > 0 {
		blob, err := s.GetBlob(res.Hashes[0])
		if err != nil {
			t.Fatalf("GetBlob failed for hash %s: %v", res.Hashes[0], err)
		}
		if len(blob.Body) == 0 {
			t.Errorf("expected non-empty blob body from expand")
		}
	}
}

