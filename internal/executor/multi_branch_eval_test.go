package executor

import (
	"context"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/config"
)

// TestEvaluateMultiBranchReturnsWinnerAction verifies that the multi-branch
// evaluation pipeline generates candidates, scores them, and returns the
// best action for the node to execute.
func TestEvaluateMultiBranchReturnsWinnerAction(t *testing.T) {
	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		Action:       "read_file",
		MCTSBranches: 3,
	}
	goalPrompt := "Find the database configuration"
	sourceOutput := "Listed directory: config/, src/, docs/"
	ceil := config.GetMCTSSpeculationCeil()

	result, err := evaluateMultiBranch(context.Background(), node, goalPrompt, sourceOutput, ceil)
	if err != nil {
		t.Fatalf("evaluateMultiBranch failed: %v", err)
	}

	// Result should be non-nil when candidates are available
	if result == nil {
		t.Fatal("evaluateMultiBranch should return a non-nil result")
	}

	// Winner should have a positive score (non-pruned)
	if result.Score <= 0 {
		t.Errorf("winning candidate should have positive score, got %f", result.Score)
	}
}

// TestEvaluateMultiBranchSkipsSpawnedNodes verifies that spawned nodes
// bypass multi-branch entirely and return nil (single-shot execution).
func TestEvaluateMultiBranchSkipsSpawnedNodes(t *testing.T) {
	node := &compiler.GraphNode{
		ID:           "spawned_action_1_1",
		Type:         "action",
		MCTSBranches: 3, // would trigger if not spawned
	}

	result, err := evaluateMultiBranch(context.Background(), node, "goal", "output", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("spawned nodes should return nil (single-shot)")
	}
}

// TestEvaluateMultiBranchSkipsZeroBranches verifies that nodes with
// MCTSBranches=0 bypass multi-branch evaluation.
func TestEvaluateMultiBranchSkipsZeroBranches(t *testing.T) {
	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		MCTSBranches: 0,
	}

	result, err := evaluateMultiBranch(context.Background(), node, "goal", "output", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("nodes with MCTSBranches=0 should return nil")
	}
}
