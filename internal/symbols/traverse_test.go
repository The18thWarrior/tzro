package symbols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Slice 6: Subgraph traversal ---

func TestTraverseSubgraph_TwoHops(t *testing.T) {
	// Linear call chain: A→B→C→D
	symbols := []CallGraphSymbol{
		{Symbol: Symbol{Name: "A", Kind: SymbolFunc, File: "a.go", Line: 1, EndLine: 5}},
		{Symbol: Symbol{Name: "B", Kind: SymbolFunc, File: "a.go", Line: 7, EndLine: 11}},
		{Symbol: Symbol{Name: "C", Kind: SymbolFunc, File: "a.go", Line: 13, EndLine: 17}},
		{Symbol: Symbol{Name: "D", Kind: SymbolFunc, File: "a.go", Line: 19, EndLine: 23}},
	}
	edges := []CallEdge{
		{CallerName: "A", CalleeName: "B"},
		{CallerName: "B", CalleeName: "C"},
		{CallerName: "C", CalleeName: "D"},
	}

	result := TraverseSubgraph(symbols, edges, []string{"A"}, 2, 100000, 30)

	// Should get A, B, C (not D — 3 hops away)
	nameSet := make(map[string]bool)
	for _, s := range result {
		nameSet[s.Name] = true
	}
	if !nameSet["A"] {
		t.Error("expected A (entry point)")
	}
	if !nameSet["B"] {
		t.Error("expected B (1 hop)")
	}
	if !nameSet["C"] {
		t.Error("expected C (2 hops)")
	}
	if nameSet["D"] {
		t.Error("D should not be included (3 hops)")
	}
}

func TestTraverseSubgraph_BidirectionalCallers(t *testing.T) {
	// B calls A, so from entry point A with hops=1, we should also get B (reverse edge)
	symbols := []CallGraphSymbol{
		{Symbol: Symbol{Name: "A", Kind: SymbolFunc, File: "a.go", Line: 1, EndLine: 5}},
		{Symbol: Symbol{Name: "B", Kind: SymbolFunc, File: "a.go", Line: 7, EndLine: 11}},
	}
	edges := []CallEdge{
		{CallerName: "B", CalleeName: "A"},
	}

	result := TraverseSubgraph(symbols, edges, []string{"A"}, 1, 100000, 30)

	nameSet := make(map[string]bool)
	for _, s := range result {
		nameSet[s.Name] = true
	}
	if !nameSet["A"] {
		t.Error("expected A (entry point)")
	}
	if !nameSet["B"] {
		t.Error("expected B (1 hop, caller of A)")
	}
}

func TestTraverseSubgraph_MaxFunctionsBudget(t *testing.T) {
	// 10 symbols in a chain, but maxFunctions=3
	var symbols []CallGraphSymbol
	var edges []CallEdge
	for i := 0; i < 10; i++ {
		name := string(rune('A' + i))
		symbols = append(symbols, CallGraphSymbol{
			Symbol: Symbol{Name: name, Kind: SymbolFunc, File: "a.go", Line: i*5 + 1, EndLine: i*5 + 4},
		})
		if i > 0 {
			prev := string(rune('A' + i - 1))
			edges = append(edges, CallEdge{CallerName: prev, CalleeName: name})
		}
	}

	result := TraverseSubgraph(symbols, edges, []string{"A"}, 10, 100000, 3)

	if len(result) > 3 {
		t.Errorf("expected at most 3 functions, got %d", len(result))
	}
}

// --- Slice 7: Body assembly ---

func TestAssembleContext_WithBodies(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile(t, tmpDir, "main.go", `package example

func Process(input string) string {
	return validate(input)
}

func validate(input string) string {
	return input
}
`)

	symbols, edges, err := BuildCallGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	result, err := AssembleContext(symbols, edges, tmpDir, true)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	if result == "" {
		t.Fatal("expected non-empty context")
	}

	// Should contain function signatures
	if !strings.Contains(result, "Process") {
		t.Error("expected context to contain Process")
	}
	if !strings.Contains(result, "validate") {
		t.Error("expected context to contain validate")
	}

	// Should contain function bodies when includeBodies=true
	if !strings.Contains(result, "return") {
		t.Error("expected context to contain function body content")
	}
}

func TestAssembleContext_WithoutBodies(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile(t, tmpDir, "main.go", `package example

func Process(input string) string {
	secretLogic := "should not appear"
	return secretLogic
}
`)

	symbols, edges, err := BuildCallGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	result, err := AssembleContext(symbols, edges, tmpDir, false)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	if result == "" {
		t.Fatal("expected non-empty context")
	}

	// Should contain signatures
	if !strings.Contains(result, "Process") {
		t.Error("expected context to contain Process signature")
	}

	// Should NOT contain function body content
	if strings.Contains(result, "secretLogic") {
		t.Error("context should not contain function body details when includeBodies=false")
	}
}

func TestAssembleContext_CharBudget(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a large file with many functions
	var b strings.Builder
	b.WriteString("package example\n\n")
	for i := 0; i < 50; i++ {
		b.WriteString("func Func")
		b.WriteString(string(rune('A' + (i % 26))))
		if i >= 26 {
			b.WriteString("2")
		}
		b.WriteString("() string {\n")
		// Add substantial body
		for j := 0; j < 20; j++ {
			b.WriteString("\t_ = \"padding line to make the function body large\"\n")
		}
		b.WriteString("\treturn \"done\"\n}\n\n")
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "big.go"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	symbols, edges, err := BuildCallGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	result, err := AssembleContext(symbols, edges, tmpDir, true)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	// Default budget is 24KB — result should not vastly exceed it
	if len(result) > 30000 {
		t.Errorf("context size %d exceeds expected budget ceiling", len(result))
	}
}
