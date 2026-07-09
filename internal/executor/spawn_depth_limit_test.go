package executor

import (
	"testing"

	"tzro/internal/compiler"
)

// TestShouldSpawnWithinDepthLimit verifies that spawning is allowed when
// the current spawn depth is below MaxDepth.
func TestShouldSpawnWithinDepthLimit(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		MutationBudget: &compiler.MutationBudget{
			MaxSpawns:       10,
			RemainingSpawns: 10,
			MaxDepth:        3,
		},
	}

	// Original node (depth 0) — spawning allowed
	if !canSpawnAtDepth(graph, "action_1") {
		t.Error("should allow spawn from original node (depth 0, maxDepth 3)")
	}

	// First-level spawn (depth 1) — spawning allowed
	if !canSpawnAtDepth(graph, "spawned_action_1_1") {
		t.Error("should allow spawn from depth-1 node (maxDepth 3)")
	}
}

// TestShouldBlockSpawnAtMaxDepth verifies that spawning is blocked when
// the current spawn depth reaches MaxDepth.
func TestShouldBlockSpawnAtMaxDepth(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		MutationBudget: &compiler.MutationBudget{
			MaxSpawns:       10,
			RemainingSpawns: 10,
			MaxDepth:        2,
		},
	}

	// Depth 2 node — at max, spawning blocked
	if canSpawnAtDepth(graph, "spawned_spawned_action_1_1_1") {
		t.Error("should block spawn at depth 2 when maxDepth is 2")
	}
}

// TestShouldAllowSpawnWithNoDepthLimit verifies that spawning is unrestricted
// when MaxDepth is 0 (no limit).
func TestShouldAllowSpawnWithNoDepthLimit(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		MutationBudget: &compiler.MutationBudget{
			MaxSpawns:       10,
			RemainingSpawns: 10,
			MaxDepth:        0, // no limit
		},
	}

	if !canSpawnAtDepth(graph, "spawned_spawned_spawned_action_1_1_1_1") {
		t.Error("should allow spawn at any depth when MaxDepth is 0")
	}
}

// TestShouldAllowSpawnWithNoBudget verifies that spawning is allowed when
// there's no MutationBudget at all (backward compatibility).
func TestShouldAllowSpawnWithNoBudget(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		MutationBudget: nil,
	}

	if !canSpawnAtDepth(graph, "spawned_action_1_1") {
		t.Error("should allow spawn when no MutationBudget exists (backward compat)")
	}
}
