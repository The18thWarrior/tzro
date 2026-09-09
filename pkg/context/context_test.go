package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestContextPack_Assembler(t *testing.T) {
	tempDir := t.TempDir()

	// Setup fake files
	authDir := filepath.Join(tempDir, "pkg", "auth")
	_ = os.MkdirAll(authDir, 0755)

	jwtCode := `package auth

func ValidateToken(token string) bool {
	if token == "" {
		return false
	}
	return true
}
`
	_ = os.WriteFile(filepath.Join(authDir, "jwt.go"), []byte(jwtCode), 0644)

	jwtTestCode := `package auth

import "testing"

func TestValidateToken(t *testing.T) {
	if !ValidateToken("valid") {
		t.Errorf("failed")
	}
}
`
	_ = os.WriteFile(filepath.Join(authDir, "jwt_test.go"), []byte(jwtTestCode), 0644)

	// Ingest symbol into SQLite store
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	_ = s.IndexSymbol("ValidateToken", "function", "pkg/auth/jwt.go", 3, "a1b2c3")

	assembler := NewAssembler(s)

	// Run assemble with 500 token budget
	pack, err := assembler.Assemble(tempDir, "validate token", 500)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	if len(pack.Items) == 0 {
		t.Fatalf("expected assembled items, got none")
	}

	if pack.UsedTokens > 500 {
		t.Errorf("expected used tokens <= budget (500), got %d", pack.UsedTokens)
	}

	// Verify explainable reason and deterministic ordering
	hasReason := false
	for _, item := range pack.Items {
		if item.Reason != "" {
			hasReason = true
		}
	}
	if !hasReason {
		t.Errorf("expected items to include explainable inclusion reasons")
	}

	markdown := pack.FormatMarkdown()
	if !strings.Contains(markdown, "Context Pack") || !strings.Contains(markdown, "pkg/auth/jwt") {
		t.Errorf("expected formatted markdown with context details, got:\n%s", markdown)
	}
}

func TestContextPack_IncrementalFreshness(t *testing.T) {
	tempDir := t.TempDir()
	authFile := filepath.Join(tempDir, "auth.go")
	_ = os.WriteFile(authFile, []byte("package main\nfunc OldFunc() {}\n"), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s)

	// Assemble 1: indexes auth.go with OldFunc
	pack1, err := assembler.Assemble(tempDir, "OldFunc", 1000)
	if err != nil {
		t.Fatalf("Assemble 1 failed: %v", err)
	}
	if len(pack1.Items) == 0 {
		t.Fatalf("expected item for OldFunc")
	}

	// Now modify auth.go replacing OldFunc with NewFunc
	_ = os.WriteFile(authFile, []byte("package main\nfunc NewFunc() {}\n"), 0644)

	// Assemble 2: should re-index dirty file and discover NewFunc
	pack2, err := assembler.Assemble(tempDir, "NewFunc", 1000)
	if err != nil {
		t.Fatalf("Assemble 2 failed: %v", err)
	}
	if len(pack2.Items) == 0 {
		t.Fatalf("expected item for NewFunc after incremental re-indexing")
	}

	// Delete file
	_ = os.Remove(authFile)
	_ = s.PruneFileSymbols("auth.go")
	syms, _ := s.SearchSymbols("NewFunc", 5)
	if len(syms) > 0 {
		t.Errorf("expected symbols pruned after file deletion, got %d", len(syms))
	}
}

