package compiler

import (
	"fmt"
	"time"
)

// ExpandToSCTGraph dynamically expands a simplified Strategy Plan (high-level DAG)
// into a fine-grained execution graph with paired Semantic-Validator, execution, and synthesis nodes.
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
			validatorID := node.ID + "_validator"
			execID := node.ID + "_exec"

			// Get the tool schema dynamically using the resolver
			var schemaStr string
			if schemaResolver != nil {
				if sch, err := schemaResolver(node.Action); err == nil {
					schemaStr = sch
				}
			}

			// 1. Semantic Validator node
			sctNodes = append(sctNodes, GraphNode{
				ID:                  validatorID,
				Type:                "semantic_validator",
				Action:              node.Action,
				Instructions:        node.Instructions,
				AllowedTools:        node.AllowedTools,
				OutputSchema:        schemaStr,
				SuggestedSkills:     node.SuggestedSkills,
				DynamicBindings:     node.DynamicBindings,
				Status:              "pending",
				ActivationThreshold: node.ActivationThreshold,
			})

			// 2. Deterministic Tool execution node
			sctNodes = append(sctNodes, GraphNode{
				ID:              execID,
				Type:            "deterministic",
				Action:          node.Action,
				Instructions:    fmt.Sprintf("Execute tool '%s' using the structured arguments extracted by the validator node {{nodes.%s.output}}", node.Action, validatorID),
				AllowedTools:    node.AllowedTools,
				DynamicBindings: node.DynamicBindings,
				Status:          "pending",
			})

			// Validator -> Exec edge
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: validatorID,
				TargetID: execID,
			})

			execNodeMap[node.ID] = execID
			bridgeNodeMap[node.ID] = validatorID
		} else {
			// Keep other structural nodes (branch, merge, probe) as is.
			// Probe nodes run their own internal Thought Chain loop and
			// do not need bridge/exec decomposition.
			//
			// Default CompactionLevel for probe nodes to "preserve" to prevent
			// destructive summarization of raw tool output. This is the root cause
			// fix for the cloud_dag quality regression (4.80 → 3.30 in benchmark-results4).
			if node.Type == "probe" && node.ProbeConfig != nil && node.ProbeConfig.CompactionLevel == "" {
				node.ProbeConfig.CompactionLevel = CompactPreserve
			}
			sctNodes = append(sctNodes, node)
			execNodeMap[node.ID] = node.ID
		}
	}

	// Reconnect original high-level dependencies
	for _, edge := range graph.Edges {
		srcExecID, srcExists := execNodeMap[edge.SourceID]

		// Target ID resolution: link to its validator node if it exists, otherwise the target ID itself
		var targetID string
		if tgtValidatorID, tgtExists := bridgeNodeMap[edge.TargetID]; tgtExists {
			targetID = tgtValidatorID
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
		Instructions: "Summarize and compile all prior action outputs into a final cohesive response. IMPORTANT: If you did not successfully find or read the relevant information, state that you did not find it. Do NOT guess or invent implementation details.",
		Status:       "pending",
	})

	// Link all execution endpoints (leaves in the original graph) to the terminal synthesis node
	// A node is an endpoint if it is an execution node and has no outbound edges to other high-level steps.
	isSourceMap := make(map[string]bool)
	for _, edge := range sctEdges {
		isSourceMap[edge.SourceID] = true
	}

	for _, node := range sctNodes {
		if (node.Type == "deterministic" || node.Type == "action" || node.Type == "probe" || node.Type == "sub_dag") && !isSourceMap[node.ID] {
			sctEdges = append(sctEdges, GraphEdge{
				SourceID: node.ID,
				TargetID: synthID,
			})
		}
	}

	return &ExecutionGraph{
		TaskID:     graph.TaskID,
		GoalPrompt: graph.GoalPrompt,
		Nodes:      sctNodes,
		Edges:      sctEdges,
		MaxCycles:  graph.MaxCycles,
		CreatedAt:  time.Now().Unix(),
	}, nil
}
