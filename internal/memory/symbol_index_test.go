package memory

import (
	"os"
	"path/filepath"
	"testing"
	"tzro/internal/symbols"
)

func TestInsertAndGetSymbolIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	probeID := "probe-test-1"
	taskID := "task-test-1"

	syms := []symbols.Symbol{
		{Name: "InferenceBackend", Kind: symbols.SymbolInterface, Signature: "type InferenceBackend interface", File: "backend.go", Line: 5, Exported: true},
		{Name: "LlamaBackend", Kind: symbols.SymbolType, Signature: "type LlamaBackend struct", File: "backend.go", Line: 15, Exported: true},
		{Name: "NewLlamaBackend", Kind: symbols.SymbolFunc, Signature: "func NewLlamaBackend(url string) *LlamaBackend", File: "backend.go", Line: 25, Exported: true},
	}

	// Insert symbols
	if err := db.InsertSymbols(probeID, taskID, syms); err != nil {
		t.Fatalf("InsertSymbols failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := db.GetSymbolIndex(probeID)
	if err != nil {
		t.Fatalf("GetSymbolIndex failed: %v", err)
	}

	if len(retrieved) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(retrieved))
	}

	// Verify ordering by file/line
	if retrieved[0].Name != "InferenceBackend" {
		t.Errorf("expected first symbol InferenceBackend, got %q", retrieved[0].Name)
	}
	if retrieved[0].Kind != symbols.SymbolInterface {
		t.Errorf("expected kind interface, got %q", retrieved[0].Kind)
	}
	if !retrieved[0].Exported {
		t.Error("expected Exported=true")
	}
}

func TestSymbolIndexAccumulationAcrossSteps(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	probeID := "probe-accum-1"
	taskID := "task-accum-1"

	// Step 1: read backend.go
	syms1 := []symbols.Symbol{
		{Name: "Backend", Kind: symbols.SymbolInterface, Signature: "type Backend interface", File: "backend.go", Line: 5, Exported: true},
	}
	if err := db.InsertSymbols(probeID, taskID, syms1); err != nil {
		t.Fatalf("InsertSymbols (step 1) failed: %v", err)
	}

	// Step 2: read router.go
	syms2 := []symbols.Symbol{
		{Name: "Router", Kind: symbols.SymbolType, Signature: "type Router struct", File: "router.go", Line: 10, Exported: true},
		{Name: "NewRouter", Kind: symbols.SymbolFunc, Signature: "func NewRouter() *Router", File: "router.go", Line: 20, Exported: true},
	}
	if err := db.InsertSymbols(probeID, taskID, syms2); err != nil {
		t.Fatalf("InsertSymbols (step 2) failed: %v", err)
	}

	// Verify accumulation: should have all 3 symbols
	retrieved, err := db.GetSymbolIndex(probeID)
	if err != nil {
		t.Fatalf("GetSymbolIndex failed: %v", err)
	}

	if len(retrieved) != 3 {
		t.Fatalf("expected 3 accumulated symbols, got %d", len(retrieved))
	}

	// Verify count method
	count, err := db.GetSymbolIndexCount(probeID)
	if err != nil {
		t.Fatalf("GetSymbolIndexCount failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}
}

func TestSymbolIndexIsolationBetweenProbes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Probe A
	if err := db.InsertSymbols("probe-A", "task-1", []symbols.Symbol{
		{Name: "TypeA", Kind: symbols.SymbolType, Signature: "type TypeA", File: "a.go", Line: 1, Exported: true},
	}); err != nil {
		t.Fatalf("InsertSymbols (probe A) failed: %v", err)
	}

	// Probe B
	if err := db.InsertSymbols("probe-B", "task-1", []symbols.Symbol{
		{Name: "TypeB", Kind: symbols.SymbolType, Signature: "type TypeB", File: "b.go", Line: 1, Exported: true},
	}); err != nil {
		t.Fatalf("InsertSymbols (probe B) failed: %v", err)
	}

	// Probe A should only see TypeA
	indexA, err := db.GetSymbolIndex("probe-A")
	if err != nil {
		t.Fatalf("GetSymbolIndex (probe A) failed: %v", err)
	}
	if len(indexA) != 1 || indexA[0].Name != "TypeA" {
		t.Errorf("probe A should only see TypeA, got: %v", symbolNames(indexA))
	}

	// Probe B should only see TypeB
	indexB, err := db.GetSymbolIndex("probe-B")
	if err != nil {
		t.Fatalf("GetSymbolIndex (probe B) failed: %v", err)
	}
	if len(indexB) != 1 || indexB[0].Name != "TypeB" {
		t.Errorf("probe B should only see TypeB, got: %v", symbolNames(indexB))
	}
}

func TestSymbolIndexEmptyProbe(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Query non-existent probe
	retrieved, err := db.GetSymbolIndex("non-existent")
	if err != nil {
		t.Fatalf("GetSymbolIndex for non-existent probe failed: %v", err)
	}
	if len(retrieved) != 0 {
		t.Errorf("expected empty result for non-existent probe, got %d", len(retrieved))
	}

	count, err := db.GetSymbolIndexCount("non-existent")
	if err != nil {
		t.Fatalf("GetSymbolIndexCount for non-existent probe failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

// --- Test helpers ---

func setupTestDB(t *testing.T) *SqliteDatabase {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_symbol_index.db")
	jsonPath := filepath.Join(tempDir, "test_symbol_index.json")

	// Create empty JSON file
	os.WriteFile(jsonPath, []byte("{}"), 0644)

	db := &SqliteDatabase{
		dbPath:   dbPath,
		jsonPath: jsonPath,
	}
	if err := db.Init(); err != nil {
		t.Fatalf("failed to initialize test DB: %v", err)
	}
	return db
}

func symbolNames(syms []symbols.Symbol) []string {
	names := make([]string, len(syms))
	for i, s := range syms {
		names[i] = s.Name
	}
	return names
}
