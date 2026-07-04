package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverModuleContext_GoWithGoMod(t *testing.T) {
	// Setup: temp dir with go.mod and a .go file
	dir := t.TempDir()
	goMod := `module example.com/myproject

go 1.22

require (
	github.com/google/uuid v1.6.0
	github.com/stretchr/testify v1.9.0
)
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	result := DiscoverModuleContext(goFile, "go")

	// Should contain module path
	if !contains(result, "example.com/myproject") {
		t.Errorf("expected module path in context, got: %s", result)
	}
	// Should contain require deps
	if !contains(result, "github.com/google/uuid") {
		t.Errorf("expected uuid dependency in context, got: %s", result)
	}
	if !contains(result, "github.com/stretchr/testify") {
		t.Errorf("expected testify dependency in context, got: %s", result)
	}
}

func TestDiscoverModuleContext_TypeScriptWithPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{
  "name": "my-project",
  "dependencies": {
    "express": "^4.18.0",
    "zod": "^3.22.0"
  },
  "devDependencies": {
    "typescript": "^5.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	tsFile := filepath.Join(dir, "src", "index.ts")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tsFile, []byte("export {}"), 0644); err != nil {
		t.Fatal(err)
	}

	result := DiscoverModuleContext(tsFile, "typescript")

	if !contains(result, "express") {
		t.Errorf("expected express in context, got: %s", result)
	}
	if !contains(result, "zod") {
		t.Errorf("expected zod in context, got: %s", result)
	}
}

func TestDiscoverModuleContext_NoManifest(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	result := DiscoverModuleContext(goFile, "go")

	if !contains(result, "standard library") && !contains(result, "Standard library") {
		t.Errorf("expected stdlib-only fallback, got: %s", result)
	}
}

func TestDiscoverModuleContext_NestedSubdir(t *testing.T) {
	// go.mod at root, file is deeply nested
	dir := t.TempDir()
	goMod := `module example.com/deep

go 1.22
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(dir, "internal", "codegen")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(nested, "codegen.go")
	if err := os.WriteFile(goFile, []byte("package codegen"), 0644); err != nil {
		t.Fatal(err)
	}

	result := DiscoverModuleContext(goFile, "go")

	if !contains(result, "example.com/deep") {
		t.Errorf("expected module path from ancestor go.mod, got: %s", result)
	}
}

// contains is a test helper for string containment checks.
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
