package codegen

import (
	"strings"
	"testing"
)

func TestBuildRepairPrompt_IncludesCompilerErrors(t *testing.T) {
	prompt := BuildRepairPrompt(
		"package main\n\nfunc Add(a, b int) string { return a + b }",
		"cannot use a + b (value of type int) as string value in return statement",
		"Create an Add function",
		"go",
		500,
		"",
	)
	if !strings.Contains(prompt, "cannot use a + b") {
		t.Error("repair prompt should include compiler errors")
	}
}

func TestBuildRepairPrompt_IncludesOriginalCode(t *testing.T) {
	prompt := BuildRepairPrompt(
		"package main\n\nfunc Add(a, b int) string { return a + b }",
		"type mismatch",
		"Create an Add function",
		"go",
		500,
		"",
	)
	if !strings.Contains(prompt, "func Add") {
		t.Error("repair prompt should include the original generated code")
	}
}

func TestBuildRepairPrompt_IncludesLanguage(t *testing.T) {
	prompt := BuildRepairPrompt(
		"code here",
		"error here",
		"spec here",
		"typescript",
		500,
		"",
	)
	if !strings.Contains(prompt, "typescript") && !strings.Contains(prompt, "TypeScript") {
		t.Error("repair prompt should mention the target language")
	}
}

func TestBuildRepairPrompt_IncludesLineLimit(t *testing.T) {
	prompt := BuildRepairPrompt("code", "error", "spec", "go", 300, "")
	if !strings.Contains(prompt, "300") {
		t.Error("repair prompt should mention the line limit")
	}
}

func TestBuildRepairDAG_SingleNode(t *testing.T) {
	dag := BuildRepairDAG("repair-test-1", "original code", "type error", "spec", "go", 500, "")
	if len(dag.Nodes) != 1 {
		t.Fatalf("repair DAG should have 1 node, got %d", len(dag.Nodes))
	}
	if dag.Nodes[0].ID != "reason_code" {
		t.Errorf("node ID should be reason_code, got %s", dag.Nodes[0].ID)
	}
	if dag.Nodes[0].Type != "synthesis" {
		t.Errorf("node type should be synthesis, got %s", dag.Nodes[0].Type)
	}
	if !strings.Contains(dag.Nodes[0].Instructions, "type error") {
		t.Error("repair DAG instructions should contain the compiler errors")
	}
	if !strings.Contains(dag.Nodes[0].Instructions, "original code") {
		t.Error("repair DAG instructions should contain the original code")
	}
}
