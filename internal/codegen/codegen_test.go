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

func TestBuildCodeDAG_Structure(t *testing.T) {
	graph := BuildCodeDAG("task_1", "implement Foo", "/tmp/foo.go", "go", 500)

	if graph.TaskID != "task_1" {
		t.Errorf("expected taskID 'task_1', got %q", graph.TaskID)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(graph.Nodes))
	}

	// Verify node IDs and types
	nodeMap := make(map[string]string)
	toolMap := make(map[string][]string)
	for _, n := range graph.Nodes {
		nodeMap[n.ID] = n.Type
		toolMap[n.ID] = n.AllowedTools
	}

	if nodeMap["check_context"] != "deterministic" {
		t.Errorf("check_context should be deterministic, got %q", nodeMap["check_context"])
	}
	if nodeMap["reason_code"] != "action" {
		t.Errorf("reason_code should be action, got %q", nodeMap["reason_code"])
	}
	if nodeMap["write_code"] != "deterministic" {
		t.Errorf("write_code should be deterministic, got %q", nodeMap["write_code"])
	}

	// Verify allowed tools
	if len(toolMap["reason_code"]) != 0 {
		t.Errorf("reason_code should have no allowed tools, got %v", toolMap["reason_code"])
	}
	checkTools := toolMap["check_context"]
	if len(checkTools) != 2 || checkTools[0] != "read_file" || checkTools[1] != "list_dir" {
		t.Errorf("check_context tools should be [read_file, list_dir], got %v", checkTools)
	}
	writeTools := toolMap["write_code"]
	if len(writeTools) != 1 || writeTools[0] != "write_file" {
		t.Errorf("write_code tools should be [write_file], got %v", writeTools)
	}

	// Verify edges
	if len(graph.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != "check_context" || graph.Edges[0].TargetID != "reason_code" {
		t.Errorf("edge 0 should be check_context -> reason_code, got %s -> %s",
			graph.Edges[0].SourceID, graph.Edges[0].TargetID)
	}
	if graph.Edges[1].SourceID != "reason_code" || graph.Edges[1].TargetID != "write_code" {
		t.Errorf("edge 1 should be reason_code -> write_code, got %s -> %s",
			graph.Edges[1].SourceID, graph.Edges[1].TargetID)
	}

	// Verify instructions contain the spec
	for _, n := range graph.Nodes {
		if n.ID == "reason_code" && !strings.Contains(n.Instructions, "implement Foo") {
			t.Error("reason_code instructions should contain the spec")
		}
	}
}
