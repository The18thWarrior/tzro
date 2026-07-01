package codegen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/executor"
)

func TestCompilationGateHook_SourceCodeNode_Pass(t *testing.T) {
	// Setup: valid Go file with go.mod
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")

	// Create go.mod so "go build" finds a module root
	goMod := "module testmod\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	hook := &CompilationGateHook{
		FilePath: goFile,
		Language: "go",
	}

	// Valid Go code
	rawOutput := "package main\n\nfunc main() {}\n"
	node := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}

	action, err := hook.AfterNode(context.Background(), "test-task", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	// Output should contain compilation result
	if !strings.Contains(rawOutput, "## Compilation Result") {
		t.Error("output should contain compilation result section")
	}
	if !strings.Contains(rawOutput, "PASSED") {
		t.Errorf("expected PASSED in output, got: %s", rawOutput)
	}

	// File should have been written
	content, err := os.ReadFile(goFile)
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if !strings.Contains(string(content), "func main()") {
		t.Error("written file should contain the generated code")
	}
}

func TestCompilationGateHook_SourceCodeNode_Fail(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")

	// Create go.mod so "go build" doesn't complain about missing module
	goMod := "module testmod\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	hook := &CompilationGateHook{
		FilePath: goFile,
		Language: "go",
	}

	// Invalid Go code
	rawOutput := "package main\n\nfunc main() { undeclaredVar }\n"
	node := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}

	action, err := hook.AfterNode(context.Background(), "test-task", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue even on failure, got %v", action)
	}

	if !strings.Contains(rawOutput, "FAILED") {
		t.Errorf("expected FAILED in output, got: %s", rawOutput)
	}
	if !strings.Contains(rawOutput, "## Compilation Result") {
		t.Error("output should contain compilation result section")
	}
}

func TestCompilationGateHook_NonSourceCodeNode_Skipped(t *testing.T) {
	hook := &CompilationGateHook{
		FilePath: "/tmp/noop.go",
		Language: "go",
	}

	rawOutput := "some non-code output"
	node := &compiler.GraphNode{
		ID:           "explore_context",
		OutputFormat: "", // Not source_code
	}

	action, err := hook.AfterNode(context.Background(), "test-task", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	// Output should NOT be modified
	if rawOutput != "some non-code output" {
		t.Errorf("non-source_code nodes should not be modified, got: %s", rawOutput)
	}
}
