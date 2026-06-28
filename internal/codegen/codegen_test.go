package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/tools"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"foo.go", "go"},
		{"bar.ts", "typescript"},
		{"baz.py", "python"},
		{"qux.rs", "rust"},
		{"test.js", "javascript"},
		{"style.css", "css"},
		{"data.json", "json"},
		{"Makefile", "text"}, // no extension
		{"foo.xyz", "xyz"},   // unknown extension → raw ext
	}
	for _, tt := range tests {
		got := DetectLanguage(tt.path)
		if got != tt.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestGatherContext_ExistingFileWithSiblings(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir) // macOS: /var -> /private/var

	// Create target file
	targetPath := filepath.Join(tmpDir, "handler.go")
	os.WriteFile(targetPath, []byte("package foo\n\nfunc Handle() {}\n"), 0644)

	// Create siblings
	os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte("package foo\n\ntype Config struct{}\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "utils.go"), []byte("package foo\n\nfunc Helper() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Readme\n"), 0644)

	v := tools.NewStaticPathValidator([]string{tmpDir})
	ctx, err := GatherContext(targetPath, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ctx.Exists {
		t.Error("expected Exists=true")
	}
	if ctx.Language != "go" {
		t.Errorf("expected language 'go', got %q", ctx.Language)
	}
	if !strings.Contains(ctx.ExistingContent, "func Handle()") {
		t.Error("expected existing content to contain the handler function")
	}

	// Same-extension siblings should be present
	if _, ok := ctx.Siblings["types.go"]; !ok {
		t.Error("expected types.go in siblings")
	}
	if _, ok := ctx.Siblings["utils.go"]; !ok {
		t.Error("expected utils.go in siblings")
	}
	// README.md should also be present (under 5 file cap)
	if _, ok := ctx.Siblings["README.md"]; !ok {
		t.Error("expected README.md in siblings (within cap)")
	}
}

func TestGatherContext_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create some siblings but NOT the target
	os.WriteFile(filepath.Join(tmpDir, "existing.go"), []byte("package foo\n"), 0644)

	targetPath := filepath.Join(tmpDir, "new_file.go")
	v := tools.NewStaticPathValidator([]string{tmpDir})
	ctx, err := GatherContext(targetPath, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ctx.Exists {
		t.Error("expected Exists=false for new file")
	}
	if ctx.ExistingContent != "" {
		t.Error("expected empty existing content for new file")
	}
	if ctx.Language != "go" {
		t.Errorf("expected language 'go', got %q", ctx.Language)
	}
	if _, ok := ctx.Siblings["existing.go"]; !ok {
		t.Error("expected existing.go in siblings even when target doesn't exist")
	}
}

func TestGatherContext_BinaryFileRejected(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	targetPath := filepath.Join(tmpDir, "binary.go")
	os.WriteFile(targetPath, []byte("package foo\x00\x01\x02"), 0644)

	v := tools.NewStaticPathValidator([]string{tmpDir})
	_, err := GatherContext(targetPath, v)
	if err == nil {
		t.Fatal("expected error for binary file")
	}
	if !strings.Contains(err.Error(), "binary content") {
		t.Errorf("expected binary content error, got: %v", err)
	}
}

func TestGatherContext_SiblingSortOrder(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Target is a .go file
	targetPath := filepath.Join(tmpDir, "main.go")
	os.WriteFile(targetPath, []byte("package main\n"), 0644)

	// Create siblings: mix of .go and other extensions
	os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("key: val\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "alpha.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "beta.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "zebra.ts"), []byte("export {}\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "aardvark.py"), []byte("pass\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "charlie.go"), []byte("package main\n"), 0644)

	v := tools.NewStaticPathValidator([]string{tmpDir})
	ctx, err := GatherContext(targetPath, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have exactly 5 siblings (max cap)
	if len(ctx.Siblings) != 5 {
		t.Errorf("expected 5 siblings (max cap), got %d", len(ctx.Siblings))
	}

	// All 3 .go siblings should be present (same extension, highest priority)
	for _, name := range []string{"alpha.go", "beta.go", "charlie.go"} {
		if _, ok := ctx.Siblings[name]; !ok {
			t.Errorf("expected %s in siblings (same extension)", name)
		}
	}
}

func TestBuildCodePrompt_Create(t *testing.T) {
	prompt := BuildCodePrompt(
		"Implement a Config struct with a Load() method",
		"/path/to/config.go",
		"go",
		"create",
		"", // no existing content
		nil,
		500,
	)

	if !strings.Contains(prompt, "Implement a Config struct") {
		t.Error("prompt should contain the spec")
	}
	if !strings.Contains(prompt, "Action: create") {
		t.Error("prompt should say Action: create")
	}
	if strings.Contains(prompt, "Existing Content") {
		t.Error("prompt should NOT contain Existing Content for create action")
	}
	if !strings.Contains(prompt, "Maximum 500 lines") {
		t.Error("prompt should contain the line cap")
	}
}

func TestBuildCodePrompt_Update(t *testing.T) {
	prompt := BuildCodePrompt(
		"Add a Validate() method",
		"/path/to/config.go",
		"go",
		"update",
		"package config\n\ntype Config struct{}\n",
		map[string]string{
			"types.go": "package config\n\ntype Options struct{}\n",
		},
		300,
	)

	if !strings.Contains(prompt, "Action: update") {
		t.Error("prompt should say Action: update")
	}
	if !strings.Contains(prompt, "Existing Content") {
		t.Error("prompt should contain Existing Content for update action")
	}
	if !strings.Contains(prompt, "type Config struct") {
		t.Error("prompt should include existing content")
	}
	if !strings.Contains(prompt, "### types.go") {
		t.Error("prompt should include sibling file heading")
	}
	if !strings.Contains(prompt, "type Options struct") {
		t.Error("prompt should include sibling content")
	}
	if !strings.Contains(prompt, "Maximum 300 lines") {
		t.Error("prompt should contain custom line cap")
	}
}
