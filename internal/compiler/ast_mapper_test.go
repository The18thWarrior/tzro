package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateShallowMap(t *testing.T) {
	// Setup a temporary directory structure
	tmpDir, err := os.MkdirTemp("", "tzro_ast_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a nested structure
	// root/
	//   cmd/
	//     tzro/
	//       main.go (type X struct{})
	//   internal/
	//     compiler/
	//       ast_mapper.go (func F(){})
	//     executor/
	//       engine.go
	//   pkg/
	//     utils/
	//       helpers.go
	//   other/ (should be ignored)
	dirs := []string{
		filepath.Join(tmpDir, "cmd", "tzro"),
		filepath.Join(tmpDir, "internal", "compiler"),
		filepath.Join(tmpDir, "internal", "executor"),
		filepath.Join(tmpDir, "pkg", "utils"),
		filepath.Join(tmpDir, "other"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create some dummy Go files with signatures
	files := map[string]string{
		filepath.Join(tmpDir, "cmd", "tzro", "main.go"):            "package main\ntype Config struct{}\nfunc main(){}",
		filepath.Join(tmpDir, "internal", "compiler", "mapper.go"): "package compiler\nfunc GenerateMap(){} ",
		filepath.Join(tmpDir, "pkg", "utils", "helpers.go"):        "package utils\ntype Helper interface{}",
	}

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Test GenerateShallowMap with depth 2
	shallowMap, err := GenerateShallowMap(tmpDir, 2)
	if err != nil {
		t.Fatalf("GenerateShallowMap failed: %v", err)
	}

	// Assertions
	// 1. Should contain top-level dirs in the whitelist (cmd, internal, pkg)
	if !strings.Contains(shallowMap, "cmd/") {
		t.Errorf("Expected map to contain 'cmd/', got: %s", shallowMap)
	}
	if !strings.Contains(shallowMap, "internal/compiler/") {
		t.Errorf("Expected map to contain 'internal/compiler/', got: %s", shallowMap)
	}

	// 2. Should NOT contain 'other/' (not in targetDirs)
	if strings.Contains(shallowMap, "other/") {
		t.Errorf("Expected map to NOT contain 'other/', got: %s", shallowMap)
	}

	// 3. Should NOT contain function signatures or types (the core goal)
	if strings.Contains(shallowMap, "func main()") || strings.Contains(shallowMap, "type Config") {
		t.Errorf("Expected map to be signature-blind, but found signatures: %s", shallowMap)
	}

	// 4. Should NOT contain file basenames (if we are only doing directories)
	if strings.Contains(shallowMap, "main.go") {
		t.Errorf("Expected map to contain only directories, but found file: %s", shallowMap)
	}
}
