package compiler

import (
	"testing"
)

// TestIncrementalSortFiltersCompletedNodes verifies that IncrementalSort
// produces execution levels containing only non-completed nodes, preserving
// topological order for the remaining graph.
func TestIncrementalSortFiltersCompletedNodes(t *testing.T) {
	// Graph: A → B → C → D
	// A and B are already completed. IncrementalSort should return levels for [C, D] only.
	graph := &ExecutionGraph{
		TaskID: "task-inc-sort-1",
		Nodes: []GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "completed"},
			{ID: "C", Type: "action", Action: "tool_c", Status: "pending"},
			{ID: "D", Type: "action", Action: "tool_d", Status: "pending"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
			{SourceID: "C", TargetID: "D"},
		},
	}

	completedNodes := map[string]bool{"A": true, "B": true}

	levels, err := IncrementalSort(graph, completedNodes)
	if err != nil {
		t.Fatalf("IncrementalSort failed: %v", err)
	}

	// Should produce 2 levels: [[C], [D]]
	if len(levels) != 2 {
		t.Fatalf("expected 2 levels, got %d: %v", len(levels), levels)
	}

	if len(levels[0]) != 1 || levels[0][0] != "C" {
		t.Errorf("expected level 0 = [C], got %v", levels[0])
	}
	if len(levels[1]) != 1 || levels[1][0] != "D" {
		t.Errorf("expected level 1 = [D], got %v", levels[1])
	}
}

// TestIncrementalSortParallelPendingNodes verifies that nodes whose completed
// dependencies are all satisfied appear in the same level (parallel execution).
func TestIncrementalSortParallelPendingNodes(t *testing.T) {
	// Graph: A → B, A → C (A completed, B and C pending → parallel)
	graph := &ExecutionGraph{
		TaskID: "task-inc-sort-2",
		Nodes: []GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "pending"},
			{ID: "C", Type: "action", Action: "tool_c", Status: "pending"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "A", TargetID: "C"},
		},
	}

	completedNodes := map[string]bool{"A": true}

	levels, err := IncrementalSort(graph, completedNodes)
	if err != nil {
		t.Fatalf("IncrementalSort failed: %v", err)
	}

	// B and C should be in the same level (parallel)
	if len(levels) != 1 {
		t.Fatalf("expected 1 level with B and C parallel, got %d levels: %v", len(levels), levels)
	}

	if len(levels[0]) != 2 {
		t.Errorf("expected 2 nodes in level 0, got %d: %v", len(levels[0]), levels[0])
	}

	nodeSet := map[string]bool{}
	for _, id := range levels[0] {
		nodeSet[id] = true
	}
	if !nodeSet["B"] || !nodeSet["C"] {
		t.Errorf("expected B and C in level 0, got %v", levels[0])
	}
}

// TestIncrementalSortDetectsCycleAfterMutation verifies that cycle detection
// still works when an invalid mutation introduces a cycle among pending nodes.
func TestIncrementalSortDetectsCycleAfterMutation(t *testing.T) {
	// Graph: A (completed) → B → C → B (cycle among pending nodes)
	graph := &ExecutionGraph{
		TaskID: "task-inc-sort-cycle",
		Nodes: []GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "pending"},
			{ID: "C", Type: "action", Action: "tool_c", Status: "pending"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
			{SourceID: "C", TargetID: "B"}, // cycle!
		},
	}

	completedNodes := map[string]bool{"A": true}

	_, err := IncrementalSort(graph, completedNodes)
	if err == nil {
		t.Fatal("expected cycle detection error, got nil")
	}
}

// TestIncrementalSortAllCompleted verifies that when all nodes are completed,
// the result is an empty level set (nothing left to execute).
func TestIncrementalSortAllCompleted(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "task-inc-sort-done",
		Nodes: []GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "completed"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
	}

	completedNodes := map[string]bool{"A": true, "B": true}

	levels, err := IncrementalSort(graph, completedNodes)
	if err != nil {
		t.Fatalf("IncrementalSort failed: %v", err)
	}

	if len(levels) != 0 {
		t.Errorf("expected 0 levels when all nodes completed, got %d: %v", len(levels), levels)
	}
}
