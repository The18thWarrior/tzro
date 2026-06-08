package executor

import (
	"fmt"

	"tzro/internal/compiler"
)

const (
	// maxConsecutiveFailuresBeforeDampening is the threshold after which
	// consecutive failed spawned nodes suppress further spawning.
	maxConsecutiveFailuresBeforeDampening = 3
)

// ApplySpawn inserts a new node into the graph, re-wiring edges from the source
// node's downstream targets to flow through the new node. This implements the
// "spawn-only mutation" model from ADR-0024.
//
// If sourceNodeID has downstream targets (outgoing edges), the new node is
// inserted between the source and all its targets:
//
//	Before: source → target1, source → target2
//	After:  source → newNode → target1, newNode → target2
//
// If sourceNodeID is a leaf (no outgoing edges), the new node is appended:
//
//	Before: source
//	After:  source → newNode
//
// Budget enforcement: if MutationBudget is non-nil, RemainingSpawns must be > 0.
// Failure dampening: if ConsecutiveFailures >= maxConsecutiveFailuresBeforeDampening, spawn is rejected.
func ApplySpawn(graph *compiler.ExecutionGraph, sourceNodeID string, newNode compiler.GraphNode) error {
	// Budget enforcement
	if graph.MutationBudget != nil {
		if graph.MutationBudget.RemainingSpawns <= 0 {
			return fmt.Errorf("mutation budget exhausted: %d/%d spawns used",
				graph.MutationBudget.MaxSpawns, graph.MutationBudget.MaxSpawns)
		}
		if graph.MutationBudget.ConsecutiveFailures >= maxConsecutiveFailuresBeforeDampening {
			return fmt.Errorf("mutation dampened: %d consecutive failures exceed threshold %d",
				graph.MutationBudget.ConsecutiveFailures, maxConsecutiveFailuresBeforeDampening)
		}
	}

	// Find all outgoing edges from sourceNodeID
	var downstreamEdges []int
	for i, edge := range graph.Edges {
		if edge.SourceID == sourceNodeID {
			downstreamEdges = append(downstreamEdges, i)
		}
	}

	// Add the new node to the graph
	graph.Nodes = append(graph.Nodes, newNode)

	if len(downstreamEdges) == 0 {
		// Leaf node: just add edge from source → newNode
		graph.Edges = append(graph.Edges, compiler.GraphEdge{
			SourceID: sourceNodeID,
			TargetID: newNode.ID,
		})
	} else {
		// Re-wire: source → newNode, newNode → each old target
		// Collect old targets first
		var oldTargets []string
		for _, idx := range downstreamEdges {
			oldTargets = append(oldTargets, graph.Edges[idx].TargetID)
		}

		// Remove old edges (iterate in reverse to preserve indices)
		for i := len(downstreamEdges) - 1; i >= 0; i-- {
			idx := downstreamEdges[i]
			graph.Edges = append(graph.Edges[:idx], graph.Edges[idx+1:]...)
		}

		// Add source → newNode
		graph.Edges = append(graph.Edges, compiler.GraphEdge{
			SourceID: sourceNodeID,
			TargetID: newNode.ID,
		})

		// Add newNode → each old target
		for _, target := range oldTargets {
			graph.Edges = append(graph.Edges, compiler.GraphEdge{
				SourceID: newNode.ID,
				TargetID: target,
			})
		}
	}

	// Decrement budget
	if graph.MutationBudget != nil {
		graph.MutationBudget.RemainingSpawns--
	}

	return nil
}
