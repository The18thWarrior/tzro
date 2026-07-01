package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompilationCommand_Go(t *testing.T) {
	cmd, available := CompilationCommand("go", "/tmp/project/cache/lru.go")
	if !available {
		t.Fatal("Go compilation command should be available")
	}
	if !strings.Contains(cmd, "go build") {
		t.Errorf("Go command should contain 'go build', got: %s", cmd)
	}
}

func TestCompilationCommand_TypeScript(t *testing.T) {
	cmd, available := CompilationCommand("typescript", "/tmp/project/src/emitter.ts")
	if !available {
		t.Fatal("TypeScript compilation command should be available")
	}
	if !strings.Contains(cmd, "tsc") {
		t.Errorf("TypeScript command should contain 'tsc', got: %s", cmd)
	}
	if !strings.Contains(cmd, "--noEmit") {
		t.Errorf("TypeScript command should include --noEmit, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--target es2020") {
		t.Errorf("TypeScript command should include --target es2020 (prevents es5 false-negatives), got: %s", cmd)
	}
}

func TestCompilationCommand_Python(t *testing.T) {
	cmd, available := CompilationCommand("python", "/tmp/project/main.py")
	if !available {
		t.Fatal("Python compilation command should be available")
	}
	if !strings.Contains(cmd, "py_compile") {
		t.Errorf("Python command should contain 'py_compile', got: %s", cmd)
	}
}

func TestCompilationCommand_UnknownLanguage(t *testing.T) {
	_, available := CompilationCommand("brainfuck", "/tmp/project/main.bf")
	if available {
		t.Error("Unknown language should return available=false")
	}
}

func TestCompilationCommand_GoUsesPackageDir(t *testing.T) {
	cmd, _ := CompilationCommand("go", "/tmp/project/internal/cache/lru.go")
	// Go build should target the package directory, not the individual file
	if strings.Contains(cmd, "lru.go") {
		t.Errorf("Go command should not reference individual file, got: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "/...") {
		t.Errorf("Go command should end with /... pattern, got: %s", cmd)
	}
}

func TestRunCompilationGate_ValidGoFile(t *testing.T) {
	// Create a temp Go module with a valid file
	dir := t.TempDir()
	modFile := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modFile, []byte("module testmod\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	goFile := filepath.Join(dir, "main.go")
	validCode := "package testmod\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	if err := os.WriteFile(goFile, []byte(validCode), 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	result := RunCompilationGate("go", goFile)
	if !result.Pass {
		t.Errorf("valid Go file should pass compilation gate, got: %s", result.Reason)
	}
}

func TestRunCompilationGate_InvalidGoFile(t *testing.T) {
	dir := t.TempDir()
	modFile := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modFile, []byte("module testmod\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	goFile := filepath.Join(dir, "main.go")
	invalidCode := "package main\n\nfunc Add(a, b int) string {\n\treturn a + b\n}\n"
	if err := os.WriteFile(goFile, []byte(invalidCode), 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	result := RunCompilationGate("go", goFile)
	if result.Pass {
		t.Error("invalid Go file should fail compilation gate")
	}
	if result.Reason == "" {
		t.Error("failure should include compiler error text")
	}
}

func TestRunCompilationGate_UnknownLanguageSkips(t *testing.T) {
	result := RunCompilationGate("brainfuck", "/nonexistent/file.bf")
	if !result.Pass {
		t.Error("unknown language should pass (skip) compilation gate")
	}
}

func TestRunCompilationGate_NonexistentFile(t *testing.T) {
	result := RunCompilationGate("go", "/nonexistent/path/main.go")
	if result.Pass {
		t.Error("nonexistent file should fail compilation gate")
	}
}
