package compiler

import (
	"fmt"
	"time"
)

// ExpandToSCTGraph dynamically expands a simplified Strategy Plan (high-level DAG)
// into a fine-grained execution graph with paired GBNF-Bridge, execution, and synthesis nodes.
func ExpandToSCTGraph(graph *ExecutionGraph, schemaResolver func(string) (string, error)) (*ExecutionGraph, error) {
	var sctNodes []GraphNode
	var sctEdges []GraphEdge

	// We map original node IDs to their respective low-level target endpoint nodes (the execution nodes)
	// so we can correctly wire parent-child dependency edges in the expanded graph.
	execNodeMap := make(map[string]string)
	bridgeNodeMap := make(map[string]string)

	for _, node := range graph.Nodes {
		// Only expand "action" or "deterministic" steps that require execution
		if node.Type == "action" || node.Type == "deterministic" {
			bridgeID := node.ID + "_bridge"
			execID := node.ID + "_exec"

			// Get the GBNF constraint schema dynamically using the resolver
			var schemaStr string
			if schemaResolver != nil {
				if sch, err := schemaResolver(node.Action); err == nil {
					schemaStr = sch
				}
			}

			// 1. Logit-level GBNF Grammar bridge node
			sctNodes = append(sctNodes, GraphNode{
				ID:              bridgeID,
				Type:            "gbnf_bridge",
				Action:          node.Action,
				Instructions:    node.Instructions,
				AllowedTools:    node.AllowedTools,
				OutputSchema:    schemaStr,
				SuggestedSkills: node.SuggestedSkills,
				Status:          "pending",
			})

			// 2. Deterministic Tool execution node
			sctNodes = append(sctNodes, GraphNode{
				ID:           execID,
				Type:         "deterministic",
				Action:       node.Action,
				Instructions: fmt.Sprintf("Execute tool '%s' using the structured arguments extracted by the bridge node {{nodes.%s.output}}", node.Action, bridgeID),
				AllowedTools: node.AllowedTools,
				Status:       "pending",
			})

			// Bridge -> Exec edge
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: bridgeID,
				TargetID: execID,
			})

			execNodeMap[node.ID] = execID
			bridgeNodeMap[node.ID] = bridgeID
		} else {
			// Keep other structural nodes (like branch, merge) as is
			sctNodes = append(sctNodes, node)
			execNodeMap[node.ID] = node.ID
		}
	}

	// Reconnect original high-level dependencies
	for _, edge := range graph.Edges {
		srcExecID, srcExists := execNodeMap[edge.SourceID]

		// Target ID resolution: link to its bridge node if it exists, otherwise the target ID itself
		var targetID string
		if tgtBridgeID, tgtExists := bridgeNodeMap[edge.TargetID]; tgtExists {
			targetID = tgtBridgeID
		} else {
			targetID = execNodeMap[edge.TargetID]
		}

		if srcExists {
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: srcExecID,
				TargetID: targetID,
			})
		} else {
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: edge.SourceID,
				TargetID: targetID,
			})
		}
	}

	// 3. Inject terminal synthesis node
	synthID := "terminal_synthesis"
	sctNodes = append(sctNodes, GraphNode{
		ID:           synthID,
		Type:         "synthesis",
		Instructions: "Summarize and compile all prior action outputs into a final cohesive response.",
		Status:       "pending",
	})

	// Link all execution endpoints (leaves in the original graph) to the terminal synthesis node
	// A node is an endpoint if it is an execution node and has no outbound edges to other high-level steps.
	isSourceMap := make(map[string]bool)
	for _, edge := range sctEdges {
		isSourceMap[edge.SourceID] = true
	}

	for _, node := range sctNodes {
		if (node.Type == "deterministic" || node.Type == "action") && !isSourceMap[node.ID] {
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: node.ID,
				TargetID: synthID,
			})
		}
	}

	return &ExecutionGraph{
		TaskID:    graph.TaskID,
		Nodes:     sctNodes,
		Edges:     sctEdges,
		MaxCycles: graph.MaxCycles,
		CreatedAt: time.Now().Unix(),
	}, nil
}
