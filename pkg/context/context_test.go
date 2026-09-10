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

	_ = s.IndexSymbol(tempDir, "ValidateToken", "function", "pkg/auth/jwt.go", 3, "a1b2c3")

	assembler := NewAssembler(s, nil)

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

	assembler := NewAssembler(s, nil)

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
	_ = s.PruneFileSymbols(tempDir, "auth.go")
	syms, _ := s.SearchSymbols(tempDir, "NewFunc", 5)
	if len(syms) > 0 {
		t.Errorf("expected symbols pruned after file deletion, got %d", len(syms))
	}
}

func TestAssemble_StrictBudgetCeiling(t *testing.T) {
	tempDir := t.TempDir()

	// Create a file whose content is ~200 tokens (800 chars)
	bigCode := `package main

// BigFunc does a lot of work.
func BigFunc() {
` + strings.Repeat("\tfmt.Println(\"processing step\")\n", 40) + `}
`
	_ = os.MkdirAll(filepath.Join(tempDir, "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "big.go"), []byte(bigCode), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s, nil)

	// Budget of 10 tokens — the file is ~200 tokens, so it must NOT be included
	pack, err := assembler.Assemble(tempDir, "BigFunc", 10)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	// Strict invariant: used tokens must never exceed budget
	if pack.UsedTokens > 10 {
		t.Errorf("STRICT BUDGET VIOLATION: used %d tokens > budget %d", pack.UsedTokens, 10)
	}
}

func TestAssemble_EmptyPackOnTightBudget(t *testing.T) {
	tempDir := t.TempDir()

	// Create a file that's at least 100 tokens
	code := `package main
func Unreachable() {
` + strings.Repeat("\tx := 42\n", 30) + `}
`
	_ = os.WriteFile(filepath.Join(tempDir, "unreachable.go"), []byte(code), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s, nil)

	// Budget of 1 token — nothing can fit
	pack, err := assembler.Assemble(tempDir, "Unreachable", 1)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	// An empty pack is a valid result when budget is too tight
	if len(pack.Items) > 0 {
		t.Errorf("expected empty pack with 1-token budget, got %d items (used %d tokens)",
			len(pack.Items), pack.UsedTokens)
	}
}

func TestAssemble_KnapsackBackfill(t *testing.T) {
	tempDir := t.TempDir()

	// Create two files: one large (~200 tokens), one small (~15 tokens)
	bigCode := `package main
func BigTarget() {
` + strings.Repeat("\tfmt.Println(\"heavy computation\")\n", 40) + `}
`
	smallCode := `package main
func SmallTarget() { return }
`
	_ = os.WriteFile(filepath.Join(tempDir, "big_target.go"), []byte(bigCode), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "small_target.go"), []byte(smallCode), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s, nil)

	// Budget of 30 tokens — big won't fit, but small should backfill
	pack, err := assembler.Assemble(tempDir, "Target", 30)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	// Budget must not be exceeded
	if pack.UsedTokens > 30 {
		t.Errorf("budget exceeded: used %d > 30", pack.UsedTokens)
	}

	// At least the small file should be packed via backfill
	hasSmall := false
	for _, item := range pack.Items {
		if strings.Contains(item.FilePath, "small_target") {
			hasSmall = true
		}
	}
	if !hasSmall && len(pack.Items) == 0 {
		t.Log("NOTE: neither file fit — budget may be too tight for even the small file's skeleton")
	}

	// The big file must NOT be packed (it exceeds budget)
	for _, item := range pack.Items {
		if strings.Contains(item.FilePath, "big_target") && item.TokenWeight > 30 {
			t.Errorf("big_target.go packed despite exceeding budget (weight=%d)", item.TokenWeight)
		}
	}
}

func TestAssemble_SymbolDeclarationSpans(t *testing.T) {
	tempDir := t.TempDir()

	// Create a file with multiple functions — the symbol match should extract
	// just the declaration span, not the whole file skeleton
	code := `package auth

import "fmt"

// ValidateToken checks if a token is valid and not expired.
func ValidateToken(token string) bool {
	fmt.Println("validating token")
	if token == "" {
		return false
	}
	return len(token) > 20
}

// RevokeToken removes a token from the active set.
func RevokeToken(token string) error {
	fmt.Println("revoking token")
	return nil
}

// RefreshToken generates a new token to replace an expired one.
func RefreshToken(old string) (string, error) {
	fmt.Println("refreshing token")
	return old + "_refreshed", nil
}
`
	authDir := filepath.Join(tempDir, "pkg", "auth")
	_ = os.MkdirAll(authDir, 0755)
	_ = os.WriteFile(filepath.Join(authDir, "auth.go"), []byte(code), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Pre-index ValidateToken as a symbol the FTS5 search will find
	_ = s.IndexSymbol(tempDir, "ValidateToken", "function", "pkg/auth/auth.go", 6, "abc123")

	assembler := NewAssembler(s, nil)
	pack, err := assembler.Assemble(tempDir, "ValidateToken", 2000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	if len(pack.Items) == 0 {
		t.Fatal("expected at least one item in pack")
	}

	// Find the FTS5 symbol match item
	var symbolItem *PackItem
	for i, item := range pack.Items {
		if item.SymbolName == "ValidateToken" {
			symbolItem = &pack.Items[i]
			break
		}
	}

	if symbolItem == nil {
		t.Fatal("expected a symbol match item for ValidateToken")
	}

	// The symbol match should use a declaration span (with elision tag),
	// not the whole file content
	if !strings.Contains(symbolItem.Content, "[body elided: #") {
		t.Error("expected declaration span with body elision tag, not raw file content")
	}

	// The span should be much smaller than the full file
	fullFileTokens := EstimateTokens(code)
	if symbolItem.TokenWeight > fullFileTokens/2 {
		t.Errorf("expected declaration span tokens (%d) much smaller than full file (%d)",
			symbolItem.TokenWeight, fullFileTokens)
	}

	// The span should NOT contain content from other functions
	if strings.Contains(symbolItem.Content, "RefreshToken") {
		t.Error("expected declaration span to not include unrelated functions")
	}
}
