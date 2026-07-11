package executor

import (
	"testing"

	"tzro/internal/compiler"
)

// TestCountSpawnDepthNonSpawnedNode verifies that original planner-created
// nodes have spawn depth 0.
func TestCountSpawnDepthNonSpawnedNode(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "action_1", Type: "action"},
			{ID: "action_2", Type: "action"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "action_1", TargetID: "action_2"},
		},
	}

	depth := countSpawnDepth(graph, "action_1")
	if depth != 0 {
		t.Errorf("expected depth 0 for non-spawned node, got %d", depth)
	}
}

// TestCountSpawnDepthSingleSpawn verifies that a spawned node has depth 1.
func TestCountSpawnDepthSingleSpawn(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "action_1", Type: "action"},
			{ID: "spawned_action_1_1", Type: "action"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "action_1", TargetID: "spawned_action_1_1"},
		},
	}

	depth := countSpawnDepth(graph, "spawned_action_1_1")
	if depth != 1 {
		t.Errorf("expected depth 1 for single-spawned node, got %d", depth)
	}
}

// TestCountSpawnDepthChainedSpawns verifies that spawn chains track depth
// transitively: spawned_spawned_ prefix = depth 2.
func TestCountSpawnDepthChainedSpawns(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "action_1", Type: "action"},
			{ID: "spawned_action_1_1", Type: "action"},
			{ID: "spawned_spawned_action_1_1_1", Type: "action"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "action_1", TargetID: "spawned_action_1_1"},
			{SourceID: "spawned_action_1_1", TargetID: "spawned_spawned_action_1_1_1"},
		},
	}

	depth := countSpawnDepth(graph, "spawned_spawned_action_1_1_1")
	if depth != 2 {
		t.Errorf("expected depth 2 for double-spawned node, got %d", depth)
	}
}

// TestIsSpawnedNodeDetectsSpawns verifies that isSpawnedNode correctly
// identifies dynamically spawned nodes by their ID prefix.
func TestIsSpawnedNodeDetectsSpawns(t *testing.T) {
	tests := []struct {
		nodeID   string
		expected bool
	}{
		{"action_1", false},
		{"spawned_action_1_1", true},
		{"spawned_spawned_action_1_1_1", true},
		{"synthesis_1", false},
	}

	for _, tc := range tests {
		got := isSpawnedNode(tc.nodeID)
		if got != tc.expected {
			t.Errorf("isSpawnedNode(%q) = %v, want %v", tc.nodeID, got, tc.expected)
		}
	}
}

// TestSpawnedNodeAlwaysSingleShot verifies that spawned nodes should always
// use single-shot evaluation (MCTSBranches=0).
func TestSpawnedNodeAlwaysSingleShot(t *testing.T) {
	// A spawned node with MCTSBranches set by planner should still
	// be treated as single-shot by the executor's shouldUseMultiBranch.
	node := &compiler.GraphNode{
		ID:           "spawned_action_1_1",
		Type:         "action",
		MCTSBranches: 3, // planner tried to set this
	}

	if shouldUseMultiBranch(node) {
		t.Error("spawned nodes should never use multi-branch evaluation")
	}
}

// TestOriginalNodeUsesMultiBranch verifies that original planner-created
// nodes with MCTSBranches > 0 use multi-branch evaluation.
func TestOriginalNodeUsesMultiBranch(t *testing.T) {
	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		MCTSBranches: 3,
	}

	if !shouldUseMultiBranch(node) {
		t.Error("original node with MCTSBranches=3 should use multi-branch")
	}
}

// TestOriginalNodeSingleShotByDefault verifies that nodes without explicit
// MCTSBranches stay in single-shot mode.
func TestOriginalNodeSingleShotByDefault(t *testing.T) {
	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		MCTSBranches: 0,
	}

	if shouldUseMultiBranch(node) {
		t.Error("node with MCTSBranches=0 should not use multi-branch")
	}
}
