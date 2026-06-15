package executor

import (
	"testing"

	"tzro/internal/compiler"
)

// TestApplySpawnInsertsNodeAndRewires verifies that ApplySpawn inserts a new node
// between a source node and its downstream targets, re-wiring edges correctly.
func TestApplySpawnInsertsNodeAndRewires(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-spawn-1",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "pending"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		MutationBudget: &compiler.MutationBudget{MaxSpawns: 5, RemainingSpawns: 5},
	}

	newNode := compiler.GraphNode{
		ID:           "spawned_1",
		Type:         "action",
		Action:       "tool_x",
		Instructions: "Explore further",
		Status:       "pending",
	}

	err := ApplySpawn(graph, "A", newNode)
	if err != nil {
		t.Fatalf("ApplySpawn failed: %v", err)
	}

	// Should now have 3 nodes
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes after spawn, got %d", len(graph.Nodes))
	}

	// Should have re-wired: A→spawned_1, spawned_1→B (old A→B removed)
	if len(graph.Edges) != 2 {
		t.Fatalf("expected 2 edges after spawn, got %d: %v", len(graph.Edges), graph.Edges)
	}

	edgeMap := map[string]string{}
	for _, e := range graph.Edges {
		edgeMap[e.SourceID] = e.TargetID
	}

	if edgeMap["A"] != "spawned_1" {
		t.Errorf("expected edge A→spawned_1, got A→%s", edgeMap["A"])
	}
	if edgeMap["spawned_1"] != "B" {
		t.Errorf("expected edge spawned_1→B, got spawned_1→%s", edgeMap["spawned_1"])
	}

	// Budget should have decremented
	if graph.MutationBudget.RemainingSpawns != 4 {
		t.Errorf("expected 4 remaining spawns, got %d", graph.MutationBudget.RemainingSpawns)
	}
}

// TestApplySpawnBudgetExhausted verifies that spawning is rejected when the
// mutation budget is exhausted.
func TestApplySpawnBudgetExhausted(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-spawn-exhausted",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "pending"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		MutationBudget: &compiler.MutationBudget{MaxSpawns: 2, RemainingSpawns: 0},
	}

	newNode := compiler.GraphNode{
		ID: "spawned_blocked", Type: "action", Action: "tool_y", Status: "pending",
	}

	err := ApplySpawn(graph, "A", newNode)
	if err == nil {
		t.Fatal("expected error when budget exhausted, got nil")
	}

	// Node count should be unchanged
	if len(graph.Nodes) != 2 {
		t.Errorf("expected 2 nodes (no spawn), got %d", len(graph.Nodes))
	}
}

// TestApplySpawnFailureDampening verifies that consecutive failures suppress
// spawning even when budget remains.
func TestApplySpawnFailureDampening(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-spawn-dampen",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
			{ID: "B", Type: "action", Action: "tool_b", Status: "pending"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		MutationBudget: &compiler.MutationBudget{
			MaxSpawns:           10,
			RemainingSpawns:     10,
			ConsecutiveFailures: 3, // 3 consecutive failures → dampened
		},
	}

	newNode := compiler.GraphNode{
		ID: "spawned_dampened", Type: "action", Action: "tool_z", Status: "pending",
	}

	err := ApplySpawn(graph, "A", newNode)
	if err == nil {
		t.Fatal("expected error when dampened by consecutive failures, got nil")
	}

	if len(graph.Nodes) != 2 {
		t.Errorf("expected 2 nodes (dampening prevented spawn), got %d", len(graph.Nodes))
	}
}

// TestApplySpawnAtLeafNode verifies spawning after a leaf node (no downstream targets)
// correctly adds the new node with a single edge from source to spawned node.
func TestApplySpawnAtLeafNode(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-spawn-leaf",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
		},
		Edges:          []compiler.GraphEdge{},
		MutationBudget: &compiler.MutationBudget{MaxSpawns: 5, RemainingSpawns: 5},
	}

	newNode := compiler.GraphNode{
		ID: "spawned_leaf", Type: "action", Action: "tool_leaf", Status: "pending",
	}

	err := ApplySpawn(graph, "A", newNode)
	if err != nil {
		t.Fatalf("ApplySpawn at leaf failed: %v", err)
	}

	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}

	if graph.Edges[0].SourceID != "A" || graph.Edges[0].TargetID != "spawned_leaf" {
		t.Errorf("expected edge A→spawned_leaf, got %s→%s", graph.Edges[0].SourceID, graph.Edges[0].TargetID)
	}
}

// TestApplySpawnNilBudgetDefaultsToAllowed verifies that a nil MutationBudget
// allows spawning (backward compatibility - graphs without budgets).
func TestApplySpawnNilBudgetDefaultsToAllowed(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-spawn-nil-budget",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a", Status: "completed"},
		},
		Edges:          []compiler.GraphEdge{},
		MutationBudget: nil, // No budget set
	}

	newNode := compiler.GraphNode{
		ID: "spawned_default", Type: "action", Action: "tool_d", Status: "pending",
	}

	err := ApplySpawn(graph, "A", newNode)
	if err != nil {
		t.Fatalf("ApplySpawn with nil budget should succeed, got: %v", err)
	}

	if len(graph.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(graph.Nodes))
	}
}
