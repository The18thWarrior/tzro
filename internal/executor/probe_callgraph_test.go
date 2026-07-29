package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/symbols"
)

// --- Slice 8: Probe integration ---

func TestIsCodeDominantDirectory_CodeHeavy(t *testing.T) {
	tmpDir := t.TempDir()

	// 8 Go files, 2 Markdown files = 80% code
	for i := 0; i < 8; i++ {
		name := "file" + string(rune('a'+i)) + ".go"
		os.WriteFile(filepath.Join(tmpDir, name), []byte("package x\nfunc F() {}"), 0644)
	}
	for i := 0; i < 2; i++ {
		name := "doc" + string(rune('a'+i)) + ".md"
		os.WriteFile(filepath.Join(tmpDir, name), []byte("# Doc"), 0644)
	}

	if !isCodeDominantDirectory([]string{tmpDir}) {
		t.Error("expected code-dominant for 80% code files")
	}
}

func TestIsCodeDominantDirectory_NonCode(t *testing.T) {
	tmpDir := t.TempDir()

	// 2 Go files, 8 Markdown files = 20% code
	for i := 0; i < 2; i++ {
		name := "file" + string(rune('a'+i)) + ".go"
		os.WriteFile(filepath.Join(tmpDir, name), []byte("package x\nfunc F() {}"), 0644)
	}
	for i := 0; i < 8; i++ {
		name := "doc" + string(rune('a'+i)) + ".md"
		os.WriteFile(filepath.Join(tmpDir, name), []byte("# Doc"), 0644)
	}

	if isCodeDominantDirectory([]string{tmpDir}) {
		t.Error("expected non-code-dominant for 20% code files")
	}
}

func TestBuildGraphDrivenContext_ProducesContext(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a small Go project
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(`package example

func main() {
	Process("hello")
}

func Process(s string) string {
	return validate(s)
}

func validate(s string) string {
	return s
}
`), 0644)

	// Mock entry point selector that picks all exported functions
	selector := &mockEntryPointSelector{
		selectFn: func(sigs []string, goal string) []string {
			var selected []string
			for _, sig := range sigs {
				if strings.Contains(sig, "main") || strings.Contains(sig, "Process") {
					selected = append(selected, sig)
				}
			}
			return selected
		},
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	context, err := buildGraphDrivenContext(tmpDir, "Understand the processing pipeline", dbPath, selector)
	if err != nil {
		t.Fatalf("buildGraphDrivenContext: %v", err)
	}

	if context == "" {
		t.Fatal("expected non-empty context")
	}

	if !strings.Contains(context, "Process") {
		t.Error("context should contain Process")
	}

	// Verify size is within budget (24KB)
	if len(context) > 30000 {
		t.Errorf("context too large: %d chars", len(context))
	}
}

// mockEntryPointSelector implements the EntryPointSelector interface for testing.
type mockEntryPointSelector struct {
	selectFn func(sigs []string, goal string) []string
}

func (m *mockEntryPointSelector) SelectEntryPoints(sigs []string, goal string) ([]string, error) {
	return m.selectFn(sigs, goal), nil
}

// Ensure symbols package is used (prevents import cycle issues)
var _ = symbols.SymbolFunc
