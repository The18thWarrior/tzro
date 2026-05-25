package compiler

import (
	"testing"
)

func TestCompileAndSortSuccess(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_task_1",
		Nodes: []GraphNode{
			{ID: "A", Type: "action", Action: "salesforce_query"},
			{ID: "B", Type: "action", Action: "postgres_insert"},
			{ID: "C", Type: "action", Action: "slack_message"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
		},
	}

	levels, err := CompileAndSort(graph)
	if err != nil {
		t.Fatalf("unexpected error compiling graph: %v", err)
	}

	if len(levels) != 3 {
		t.Errorf("expected 3 execution levels, got %d", len(levels))
	}

	if levels[0][0] != "A" || levels[1][0] != "B" || levels[2][0] != "C" {
		t.Errorf("incorrect topological levels sequence: %v", levels)
	}
}

func TestCompileAndSortCycle(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_task_cycle",
		Nodes: []GraphNode{
			{ID: "A", Type: "action"},
			{ID: "B", Type: "action"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "A"},
		},
	}

	_, err := CompileAndSort(graph)
	if err == nil {
		t.Fatalf("expected cyclical compile error, but got nil")
	}
}
