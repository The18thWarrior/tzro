package store

import (
	"testing"
)

func TestStore_PutAndGetBlob(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	body := "func Add(a, b int) int {\n\treturn a + b\n}"
	hash, err := s.PutBlob("math.go", 10, 12, body)
	if err != nil {
		t.Fatalf("PutBlob failed: %v", err)
	}
	if len(hash) == 0 {
		t.Fatalf("expected non-empty hash")
	}

	blob, err := s.GetBlob(hash)
	if err != nil {
		t.Fatalf("GetBlob failed: %v", err)
	}
	if blob.Body != body {
		t.Errorf("expected body %q, got %q", body, blob.Body)
	}
	if blob.FilePath != "math.go" {
		t.Errorf("expected filepath math.go, got %s", blob.FilePath)
	}
}

func TestStore_IndexAndSearchSymbols(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	err = s.IndexSymbol("ValidateToken", "function", "auth/jwt.go", 45, "a8f19c")
	if err != nil {
		t.Fatalf("IndexSymbol failed: %v", err)
	}

	results, err := s.SearchSymbols("Validate", 10)
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Symbol != "ValidateToken" {
		t.Errorf("expected ValidateToken, got %s", results[0].Symbol)
	}
	if results[0].Hash != "a8f19c" {
		t.Errorf("expected hash a8f19c, got %s", results[0].Hash)
	}
}
