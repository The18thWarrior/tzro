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
		"",
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
		"",
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

func TestBuildCodeDAG_Structure_Legacy(t *testing.T) {
	// nil context triggers the legacy 3-node DAG path
	graph := BuildCodeDAG("task_1", "implement Foo", "/tmp/foo.go", "go", 500, nil)

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

func TestBuildCodeDAG_WithPrecomputedContext(t *testing.T) {
	ctx := &CodeContext{
		Exists:          true,
		ExistingContent: "package foo\n\nfunc Old() {}\n",
		Language:        "go",
		Siblings: map[string]string{
			"types.go": "package foo\n\ntype Config struct{}\n",
		},
	}

	graph := BuildCodeDAG("task_2", "add Bar()", "/tmp/foo.go", "go", 500, ctx)

	// Two-node DAG: reason_code → validate_code
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes (reason_code + validate_code), got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]
	if node.ID != "reason_code" {
		t.Errorf("expected node ID 'reason_code', got %q", node.ID)
	}
	if node.Type != "synthesis" {
		t.Errorf("expected node type 'synthesis', got %q", node.Type)
	}
	if len(node.AllowedTools) != 0 {
		t.Errorf("reason_code should have no allowed tools, got %v", node.AllowedTools)
	}
	if node.OutputFormat != "source_code" {
		t.Errorf("reason_code should have OutputFormat 'source_code', got %q", node.OutputFormat)
	}
	if node.OutputLanguage != "go" {
		t.Errorf("reason_code should have OutputLanguage 'go', got %q", node.OutputLanguage)
	}

	// validate_code node
	valNode := graph.Nodes[1]
	if valNode.ID != "validate_code" {
		t.Errorf("expected second node ID 'validate_code', got %q", valNode.ID)
	}
	if valNode.ActivationThreshold <= 0 {
		t.Errorf("validate_code should have a non-zero ActivationThreshold, got %f", valNode.ActivationThreshold)
	}

	// Edge: reason_code → validate_code
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != "reason_code" || graph.Edges[0].TargetID != "validate_code" {
		t.Errorf("expected edge reason_code→validate_code, got %s→%s",
			graph.Edges[0].SourceID, graph.Edges[0].TargetID)
	}

	// MutationBudget
	if graph.MutationBudget == nil {
		t.Fatal("expected MutationBudget to be set")
	}
	if graph.MutationBudget.MaxSpawns != 2 {
		t.Errorf("expected MaxSpawns=2, got %d", graph.MutationBudget.MaxSpawns)
	}
	if graph.MutationBudget.RemainingSpawns != 2 {
		t.Errorf("expected RemainingSpawns=2, got %d", graph.MutationBudget.RemainingSpawns)
	}

	// Prompt should contain spec, existing content, and siblings
	if !strings.Contains(node.Instructions, "add Bar()") {
		t.Error("prompt should contain the spec")
	}
	if !strings.Contains(node.Instructions, "func Old()") {
		t.Error("prompt should contain existing file content")
	}
	if !strings.Contains(node.Instructions, "types.go") {
		t.Error("prompt should contain sibling file names")
	}
	if !strings.Contains(node.Instructions, "update") {
		t.Error("prompt should say 'update' for existing file")
	}
}

func TestWriteCodeFile_CreateAndLineCount(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "output.go")

	action, lines, err := WriteCodeFile(target, "package foo\n\nfunc Bar() {}\n", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "created" {
		t.Errorf("expected action 'created', got %q", action)
	}
	if lines != 3 {
		t.Errorf("expected 3 lines, got %d", lines)
	}

	// Verify file was written
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if !strings.Contains(string(data), "func Bar()") {
		t.Error("file should contain the generated code")
	}
}

func TestWriteCodeFile_UpdateExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "output.go")

	// Create initial file
	os.WriteFile(target, []byte("package old\n"), 0644)

	action, _, err := WriteCodeFile(target, "package new\n\nfunc Updated() {}\n", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "updated" {
		t.Errorf("expected action 'updated', got %q", action)
	}
}

func TestWriteCodeFile_ExceedsLineLimit(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "output.go")

	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("line\n")
	}

	_, _, err := WriteCodeFile(target, b.String(), 5)
	if err == nil {
		t.Fatal("expected error for exceeding line limit")
	}
	if !strings.Contains(err.Error(), "exceeds maximum line count") {
		t.Errorf("expected line count error, got: %v", err)
	}

	// File should NOT be written when line limit exceeded
	if _, statErr := os.Stat(target); statErr == nil {
		t.Error("file should not be written when line limit is exceeded")
	}
}

func TestWriteCodeFile_StripsFences(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "output.go")

	action, _, err := WriteCodeFile(target, "```go\npackage foo\n\nfunc Fenced() {}\n```", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "created" {
		t.Errorf("expected action 'created', got %q", action)
	}

	data, _ := os.ReadFile(target)
	if strings.Contains(string(data), "```") {
		t.Error("fences should be stripped from written file")
	}
}

func TestStripMarkdownFences_FullFence(t *testing.T) {
	input := "```go\npackage foo\n\nfunc Bar() {}\n```"
	result := StripMarkdownFences(input)
	expected := "package foo\n\nfunc Bar() {}\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestStripMarkdownFences_OpenOnly(t *testing.T) {
	input := "```go\npackage foo\n\nfunc Bar() {}\n"
	result := StripMarkdownFences(input)
	expected := "package foo\n\nfunc Bar() {}\n\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestStripMarkdownFences_NoFence(t *testing.T) {
	input := "package foo\n\nfunc Bar() {}\n"
	result := StripMarkdownFences(input)
	if result != input {
		t.Errorf("should not modify content without fences: got %q", result)
	}
}

func TestCleanGeneratedCode_UnderLimit(t *testing.T) {
	content := "line1\nline2\nline3\n"
	result, err := CleanGeneratedCode(content, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != content {
		t.Errorf("content should pass through unchanged: got %q", result)
	}
}

func TestCleanGeneratedCode_ExceedsLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("line\n")
	}
	_, err := CleanGeneratedCode(b.String(), 5)
	if err == nil {
		t.Fatal("expected error for exceeding line limit")
	}
	if !strings.Contains(err.Error(), "exceeds maximum line count") {
		t.Errorf("expected line count error, got: %v", err)
	}
}

func TestCleanGeneratedCode_StripsFencesBeforeCounting(t *testing.T) {
	// 3 content lines wrapped in fences = 3 lines after stripping
	input := "```go\nline1\nline2\nline3\n```"
	result, err := CleanGeneratedCode(input, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "```") {
		t.Error("fences should be stripped")
	}
}
