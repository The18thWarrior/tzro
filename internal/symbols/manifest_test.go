package symbols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Slice 11: BuildDirectoryManifest traverses depth 2, counts files ---

func TestBuildDirectoryManifest_BasicTraversal(t *testing.T) {
	// Create a temp directory tree:
	// root/
	//   main.go
	//   README.md
	//   pkg/
	//     handler.go
	//     deep/
	//       nested.go  (depth 3 — should NOT be included at maxDepth=2)
	root := t.TempDir()

	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc Main() {}\n")
	writeTestFile(t, filepath.Join(root, "README.md"), "# My Project\n\nThis is a project.\nWith multiple lines.\n")

	pkgDir := filepath.Join(root, "pkg")
	os.MkdirAll(pkgDir, 0755)
	writeTestFile(t, filepath.Join(pkgDir, "handler.go"), "package pkg\n\nfunc Handle() {}\n")

	deepDir := filepath.Join(root, "pkg", "deep")
	os.MkdirAll(deepDir, 0755)
	writeTestFile(t, filepath.Join(deepDir, "nested.go"), "package deep\n\nfunc Nested() {}\n")

	manifest := BuildDirectoryManifest(root, 0, 2, 100_000)

	if manifest == nil {
		t.Fatal("expected non-nil manifest")
	}

	// Root should have 2 files (main.go, README.md)
	if manifest.FileCount < 2 {
		t.Errorf("root FileCount = %d, expected >= 2", manifest.FileCount)
	}

	// Root should have at least 1 child directory (pkg)
	if len(manifest.Children) < 1 {
		t.Fatalf("expected at least 1 child directory, got %d", len(manifest.Children))
	}

	// pkg child should have handler.go
	pkgManifest := findChild(manifest, "pkg")
	if pkgManifest == nil {
		t.Fatal("expected 'pkg' child directory")
	}
	if pkgManifest.FileCount < 1 {
		t.Errorf("pkg FileCount = %d, expected >= 1", pkgManifest.FileCount)
	}

	// At depth 2, 'deep' should be a child of 'pkg' but NOT recursed into
	deepManifest := findChild(pkgManifest, "deep")
	if deepManifest == nil {
		t.Fatal("expected 'deep' child of 'pkg'")
	}
	// deep's children should be empty (depth limit reached)
	if len(deepManifest.Children) != 0 {
		t.Errorf("expected no children at depth limit, got %d", len(deepManifest.Children))
	}
}

// --- Slice 12: Budget exhaustion stops symbol extraction ---

func TestBuildDirectoryManifest_BudgetExhaustion(t *testing.T) {
	root := t.TempDir()

	// Create many files to exceed a small budget
	for i := 0; i < 20; i++ {
		content := "package main\n\nfunc Func" + string(rune('A'+i)) + "() { /* long content to consume budget */ }\n"
		writeTestFile(t, filepath.Join(root, "file"+string(rune('a'+i))+".go"), content)
	}

	// Tiny budget: should stop early
	manifest := BuildDirectoryManifest(root, 0, 1, 200)

	if manifest == nil {
		t.Fatal("expected non-nil manifest even with budget exhaustion")
	}

	// Should have some files but not extract symbols for all of them
	// (the manifest itself should still be created, just with fewer symbols)
	totalChars := manifest.EstimateChars()
	if totalChars > 400 { // allow some overhead beyond the 200 budget
		t.Errorf("manifest chars = %d, expected <= ~400 (budget was 200)", totalChars)
	}
}

// --- Slice 13: Code files produce symbols, doc files produce previews ---

func TestBuildDirectoryManifest_CodeAndDocFiles(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, filepath.Join(root, "server.go"), `package main

import "net/http"

// Server handles HTTP requests.
func NewServer() *http.Server {
	return &http.Server{}
}

type Config struct {
	Port int
	Host string
}
`)

	writeTestFile(t, filepath.Join(root, "CHANGELOG.md"), `# Changelog

## v1.0.0
- Initial release
- Added server functionality
`)

	manifest := BuildDirectoryManifest(root, 0, 1, 100_000)

	if manifest == nil {
		t.Fatal("expected non-nil manifest")
	}

	// Should have symbols from Go file
	hasFunc := false
	hasType := false
	for _, sym := range manifest.Symbols {
		if sym.Name == "NewServer" {
			hasFunc = true
		}
		if sym.Name == "Config" {
			hasType = true
		}
	}
	if !hasFunc {
		t.Error("expected NewServer symbol from Go file")
	}
	if !hasType {
		t.Error("expected Config symbol from Go file")
	}

	// Should have doc preview from markdown file
	hasDoc := false
	for _, doc := range manifest.DocPreview {
		if strings.Contains(doc.Title, "Changelog") || strings.Contains(doc.File, "CHANGELOG") {
			hasDoc = true
			if len(doc.Preview) == 0 {
				t.Error("expected non-empty preview for doc file")
			}
		}
	}
	if !hasDoc {
		t.Error("expected doc preview from CHANGELOG.md")
	}
}

// --- helpers ---

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeTestFile(%s): %v", path, err)
	}
}

func findChild(m *DirectoryManifest, name string) *DirectoryManifest {
	for _, c := range m.Children {
		if filepath.Base(c.Path) == name {
			return c
		}
	}
	return nil
}
