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
