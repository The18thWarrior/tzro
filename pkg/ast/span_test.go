package ast

import (
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestExtractDeclarationSpan_GoFunction(t *testing.T) {
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

	span, err := ExtractDeclarationSpan("math.go", src, 6, "Add", s)
	if err != nil {
		t.Fatalf("ExtractDeclarationSpan failed: %v", err)
	}
	if span == nil {
		t.Fatal("expected non-nil span for Add")
	}

	// Signature must be preserved
	if !strings.Contains(span.Signature, "func Add(a, b int) int") {
		t.Errorf("expected Add signature, got %q", span.Signature)
	}

	// Docstring must be captured
	if !strings.Contains(span.Docstring, "Add computes the sum") {
		t.Errorf("expected docstring captured, got %q", span.Docstring)
	}

	// Body must be elided with hash
	if span.BodyHash == "" {
		t.Error("expected body hash to be set")
	}

	// Rendered code must contain the elision tag, not the body
	if strings.Contains(span.Code, "fmt.Println") {
		t.Error("expected body to be elided in rendered code")
	}
	if !strings.Contains(span.Code, "[body elided: #") {
		t.Error("expected elision tag in rendered code")
	}

	// Kind should be function
	if span.Kind != "function" {
		t.Errorf("expected kind 'function', got %q", span.Kind)
	}

	// TokenWeight must be positive and much smaller than full file
	fullFileTokens := EstimateTokens(string(src))
	if span.TokenWeight <= 0 {
		t.Error("expected positive token weight")
	}
	if span.TokenWeight > fullFileTokens/2 {
		t.Errorf("expected declaration span tokens (%d) to be much smaller than full file (%d)",
			span.TokenWeight, fullFileTokens)
	}

	// Body should be retrievable from store
	blob, err := s.GetBlob(span.BodyHash)
	if err != nil {
		t.Fatalf("GetBlob failed: %v", err)
	}
	if !strings.Contains(blob.Body, "fmt.Println") {
		t.Error("expected stored blob to contain original body")
	}
}

func TestExtractDeclarationSpan_GoMethodWithReceiver(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	src := []byte(`package auth

// Store manages authentication state.
type Store struct {
	tokens map[string]bool
}

// Validate checks if a token is valid.
func (s *Store) Validate(token string) bool {
	_, ok := s.tokens[token]
	return ok
}

// Revoke invalidates a token.
func (s *Store) Revoke(token string) {
	delete(s.tokens, token)
}
`)

	// Target line 10 = the Validate method signature
	span, err := ExtractDeclarationSpan("auth.go", src, 10, "Validate", s)
	if err != nil {
		t.Fatalf("ExtractDeclarationSpan failed: %v", err)
	}
	if span == nil {
		t.Fatal("expected non-nil span for Validate")
	}

	// Kind should be method
	if span.Kind != "method" {
		t.Errorf("expected kind 'method', got %q", span.Kind)
	}

	// Signature must contain receiver
	if !strings.Contains(span.Signature, "(s *Store)") {
		t.Errorf("expected receiver in signature, got %q", span.Signature)
	}

	// Docstring captured
	if !strings.Contains(span.Docstring, "Validate checks") {
		t.Errorf("expected docstring, got %q", span.Docstring)
	}

	// Body elided
	if span.BodyHash == "" {
		t.Error("expected body hash")
	}
	if strings.Contains(span.Code, "s.tokens[token]") {
		t.Error("expected body elided from rendered code")
	}

	// Must NOT include the Revoke method — only the targeted symbol
	if strings.Contains(span.Code, "Revoke") {
		t.Error("expected only the targeted Validate method, not Revoke")
	}
}

func TestExtractDeclarationSpan_FallbackWindow(t *testing.T) {
	// .txt is not a supported Tree-sitter language — should fall back to 25-line window
	src := []byte(`Line 1
Line 2
Line 3
# Description of doSomething
doSomething:
  step1: run
  step2: build
  step3: test
  step4: deploy
Line 10
Line 11
Line 12
`)

	span, err := ExtractDeclarationSpan("config.txt", src, 5, "doSomething", nil)
	if err != nil {
		t.Fatalf("FallbackSpan failed: %v", err)
	}
	if span == nil {
		t.Fatal("expected non-nil span from fallback")
	}

	// Kind should be "unknown" for fallback
	if span.Kind != "unknown" {
		t.Errorf("expected kind 'unknown' for fallback, got %q", span.Kind)
	}

	// Code must include content around target line
	if !strings.Contains(span.Code, "doSomething") {
		t.Error("expected fallback code to include target line content")
	}

	// Window must not exceed 25 lines
	windowLines := strings.Count(span.Code, "\n") + 1
	if windowLines > 25 {
		t.Errorf("expected fallback window <= 25 lines, got %d", windowLines)
	}

	// TokenWeight must be set
	if span.TokenWeight <= 0 {
		t.Error("expected positive token weight in fallback span")
	}
}

func TestExtractDeclarationSpan_EmptySource(t *testing.T) {
	span, err := ExtractDeclarationSpan("empty.go", []byte{}, 1, "Foo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if span != nil {
		t.Error("expected nil span for empty source")
	}
}
