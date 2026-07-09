package executor

import (
	"strings"

	"tzro/internal/compiler"
)

// countSpawnDepth counts the spawn ancestry depth of a node by counting
// the number of "spawned_" prefixes in its ID. Original planner-created
// nodes have depth 0, first-level spawns have depth 1, etc.
//
// This uses the naming convention established by ApplySpawn which prefixes
// spawned node IDs with "spawned_".
func countSpawnDepth(graph *compiler.ExecutionGraph, nodeID string) int {
	depth := 0
	id := nodeID
	for strings.HasPrefix(id, "spawned_") {
		depth++
		id = strings.TrimPrefix(id, "spawned_")
	}
	return depth
}

// isSpawnedNode returns true if the node was dynamically spawned (not
// created by the planner). Uses the "spawned_" prefix convention.
func isSpawnedNode(nodeID string) bool {
	return strings.HasPrefix(nodeID, "spawned_")
}

// shouldUseMultiBranch determines if a node should use multi-branch Edge
// Thought evaluation. Returns false for:
//   - Spawned nodes (always single-shot per Branch 7 decision)
//   - Nodes with MCTSBranches <= 0 (single-shot mode)
func shouldUseMultiBranch(node *compiler.GraphNode) bool {
	if isSpawnedNode(node.ID) {
		return false
	}
	return node.MCTSBranches > 0
}

// canSpawnAtDepth checks whether a spawn is allowed from the given node,
// considering the MutationBudget's MaxDepth constraint (ADR-0045).
// Returns true if:
//   - No MutationBudget exists (backward compatibility)
//   - MaxDepth is 0 (no depth limit)
//   - Current spawn depth < MaxDepth
func canSpawnAtDepth(graph *compiler.ExecutionGraph, nodeID string) bool {
	if graph.MutationBudget == nil {
		return true
	}
	maxDepth := graph.MutationBudget.MaxDepth
	if maxDepth <= 0 {
		return true // no limit
	}
	currentDepth := countSpawnDepth(graph, nodeID)
	return currentDepth < maxDepth
}
