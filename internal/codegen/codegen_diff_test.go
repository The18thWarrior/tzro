package codegen

import (
	"strings"
	"testing"

	"tzro/internal/compiler"
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

func TestBuildDiffDAGWithExploration_Structure(t *testing.T) {
	ctx := &CodeContext{
		Exists:          true,
		ExistingContent: "package foo\n\nfunc Old() {}\n",
		Language:        "go",
		Siblings: map[string]string{
			"types.go": "package foo\n\ntype Config struct{}\n",
		},
	}

	graph := BuildDiffDAGWithExploration("task_diff_exp", "add logging", "/tmp/foo.go", "go", ctx)

	// Should have 3 nodes
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(graph.Nodes))
	}

	nodeMap := make(map[string]compiler.GraphNode)
	for _, n := range graph.Nodes {
		nodeMap[n.ID] = n
	}

	// explore_context node
	explore, ok := nodeMap["explore_context"]
	if !ok {
		t.Fatal("expected explore_context node")
	}
	if explore.Type != "action" {
		t.Errorf("explore_context type should be 'action', got %q", explore.Type)
	}
	if explore.ActivationThreshold != 0.8 {
		t.Errorf("explore_context ActivationThreshold should be 0.8, got %f", explore.ActivationThreshold)
	}

	// reason_code node with diff schema
	reason, ok := nodeMap["reason_code"]
	if !ok {
		t.Fatal("expected reason_code node")
	}
	if reason.Type != "synthesis" {
		t.Errorf("reason_code type should be 'synthesis', got %q", reason.Type)
	}
	if reason.OutputSchema == "" {
		t.Error("reason_code OutputSchema should be set for GBNF constraint")
	}
	if !strings.Contains(reason.OutputSchema, "hunks") {
		t.Error("reason_code OutputSchema should contain 'hunks' definition")
	}
	if !strings.Contains(reason.Instructions, "searchContent") {
		t.Error("reason_code instructions should use diff prompt format")
	}

	// validate_code node
	validate, ok := nodeMap["validate_code"]
	if !ok {
		t.Fatal("expected validate_code node")
	}
	if validate.Type != "deterministic" {
		t.Errorf("validate_code type should be 'deterministic', got %q", validate.Type)
	}
	if validate.ActivationThreshold != 0.7 {
		t.Errorf("validate_code ActivationThreshold should be 0.7, got %f", validate.ActivationThreshold)
	}

	// Edges: explore_context → reason_code → validate_code
	if len(graph.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != "explore_context" || graph.Edges[0].TargetID != "reason_code" {
		t.Errorf("edge 0 should be explore_context → reason_code, got %s → %s",
			graph.Edges[0].SourceID, graph.Edges[0].TargetID)
	}
	if graph.Edges[1].SourceID != "reason_code" || graph.Edges[1].TargetID != "validate_code" {
		t.Errorf("edge 1 should be reason_code → validate_code, got %s → %s",
			graph.Edges[1].SourceID, graph.Edges[1].TargetID)
	}

	// MutationBudget should be set
	if graph.MutationBudget == nil {
		t.Error("MutationBudget should not be nil")
	}
}
