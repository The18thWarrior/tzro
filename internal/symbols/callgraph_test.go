package symbols

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Slice 3: Call edge extraction ---

func TestExtractCallEdges_Go_DirectCall(t *testing.T) {
	src := []byte(`package example

func processData(input []byte) []byte {
	result := transform(input)
	return validate(result)
}

func transform(data []byte) []byte {
	return data
}

func validate(data []byte) []byte {
	return data
}
`)

	symbols, err := ExtractAllSymbols("example.go", src)
	if err != nil {
		t.Fatalf("ExtractAllSymbols: %v", err)
	}

	symTable := buildSymbolTable(symbols)
	edges := ExtractCallEdges(symTable, "example.go", src)

	// processData should call transform and validate
	found := make(map[string]bool)
	for _, e := range edges {
		found[e.CallerName+"->"+e.CalleeName] = true
	}

	if !found["processData->transform"] {
		t.Error("expected edge processData->transform")
	}
	if !found["processData->validate"] {
		t.Error("expected edge processData->validate")
	}
}

func TestExtractCallEdges_Go_MethodCall(t *testing.T) {
	src := []byte(`package example

type Server struct{}

func (s *Server) Start() {
	s.listen()
}

func (s *Server) listen() {}
`)

	symbols, err := ExtractAllSymbols("server.go", src)
	if err != nil {
		t.Fatalf("ExtractAllSymbols: %v", err)
	}

	symTable := buildSymbolTable(symbols)
	edges := ExtractCallEdges(symTable, "server.go", src)

	found := false
	for _, e := range edges {
		if e.CallerName == "Start" && e.CalleeName == "listen" {
			found = true
		}
	}
	if !found {
		t.Error("expected edge Start->listen")
	}
}

// --- Slice 4: BuildCallGraph ---

func TestBuildCallGraph_MultiFile(t *testing.T) {
	// Create temp directory with 3 Go files
	tmpDir := t.TempDir()

	writeFile(t, tmpDir, "main.go", `package example

func main() {
	result := Process("input")
	_ = result
}
`)

	writeFile(t, tmpDir, "process.go", `package example

func Process(input string) string {
	return validate(input)
}

func validate(input string) string {
	return input
}
`)

	writeFile(t, tmpDir, "helper.go", `package example

func helperFunc() string {
	return "help"
}
`)

	symbols, edges, err := BuildCallGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	// Should have symbols from all files
	if len(symbols) == 0 {
		t.Fatal("expected symbols from all files")
	}

	// Check that we have symbols from each file
	fileSet := make(map[string]bool)
	for _, s := range symbols {
		fileSet[s.File] = true
	}
	for _, f := range []string{"main.go", "process.go", "helper.go"} {
		if !fileSet[f] {
			t.Errorf("missing symbols from %s", f)
		}
	}

	// Should have cross-file edges: main->Process, Process->validate
	edgeSet := make(map[string]bool)
	for _, e := range edges {
		edgeSet[e.CallerName+"->"+e.CalleeName] = true
	}
	if !edgeSet["main->Process"] {
		t.Error("expected cross-file edge main->Process")
	}
	if !edgeSet["Process->validate"] {
		t.Error("expected same-file edge Process->validate")
	}
}

// --- Slice 5: CallGraphStore ---

func TestCallGraphStore_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile(t, tmpDir, "a.go", `package example
func Foo() { Bar() }
func Bar() {}
`)

	symbols, edges, err := BuildCallGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	store, err := NewCallGraphStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("NewCallGraphStore: %v", err)
	}
	defer store.Close()

	if err := store.SaveGraph(tmpDir, symbols, edges); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}

	loadedSymbols, loadedEdges, err := store.LoadGraph(tmpDir)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if len(loadedSymbols) != len(symbols) {
		t.Errorf("symbol count: got %d, want %d", len(loadedSymbols), len(symbols))
	}
	if len(loadedEdges) != len(edges) {
		t.Errorf("edge count: got %d, want %d", len(loadedEdges), len(edges))
	}
}

func TestCallGraphStore_StalenessDetection(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile(t, tmpDir, "a.go", `package example
func Foo() {}
`)

	symbols, edges, err := BuildCallGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	store, err := NewCallGraphStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("NewCallGraphStore: %v", err)
	}
	defer store.Close()

	if err := store.SaveGraph(tmpDir, symbols, edges); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}

	// Modify the file
	writeFile(t, tmpDir, "a.go", `package example
func Foo() {}
func Bar() {}
`)

	stale, err := store.IsStale(tmpDir)
	if err != nil {
		t.Fatalf("IsStale: %v", err)
	}
	if len(stale) == 0 {
		t.Error("expected stale files after modification")
	}
}

// --- Test helpers ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
}
