package codegen

import (
	"strings"
	"testing"
)

func TestBuildDiffDAG_Structure(t *testing.T) {
	ctx := &CodeContext{
		Exists:          true,
		ExistingContent: "package foo\n\nfunc Old() {}\n",
		Language:        "go",
		Siblings: map[string]string{
			"types.go": "package foo\n\ntype Config struct{}\n",
		},
	}

	graph := BuildDiffDAG("task_diff_1", "add Bar()", "/tmp/foo.go", "go", ctx)

	// Single node
	if len(graph.Nodes) != 1 {
		t.Fatalf("expected 1 node (reason_code), got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]

	// Node ID and type
	if node.ID != "reason_code" {
		t.Errorf("expected node ID 'reason_code', got %q", node.ID)
	}
	if node.Type != "synthesis" {
		t.Errorf("expected node type 'synthesis', got %q", node.Type)
	}

	// OutputSchema should be set to the diff JSON schema for GBNF constraint
	if node.OutputSchema == "" {
		t.Error("OutputSchema should be set for GBNF constraint")
	}
	if !strings.Contains(node.OutputSchema, "hunks") {
		t.Error("OutputSchema should contain 'hunks' array definition")
	}
	if !strings.Contains(node.OutputSchema, "searchContent") {
		t.Error("OutputSchema should contain 'searchContent' field")
	}

	// No allowed tools
	if len(node.AllowedTools) != 0 {
		t.Errorf("reason_code should have no allowed tools, got %v", node.AllowedTools)
	}

	// No edges (single node graph)
	if len(graph.Edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(graph.Edges))
	}

	// Prompt should contain the diff-specific instructions
	if !strings.Contains(node.Instructions, "add Bar()") {
		t.Error("prompt should contain the spec")
	}
	if !strings.Contains(node.Instructions, "func Old()") {
		t.Error("prompt should contain existing file content")
	}
	if !strings.Contains(node.Instructions, "searchContent") {
		t.Error("prompt should reference searchContent (diff prompt format)")
	}

	// TaskID
	if graph.TaskID != "task_diff_1" {
		t.Errorf("expected taskID 'task_diff_1', got %q", graph.TaskID)
	}
}
