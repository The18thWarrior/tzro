package main

import (
	"encoding/json"

	"tzro/pkg/executor"
	"tzro/pkg/store"
)

// selectedGraphResult keeps intermediate evidence local. If it cannot be
// retained, return the full result rather than an unrecoverable omission.
func selectedGraphResult(g *executor.Graph, result *executor.ExecutionResult, s *store.Store, workspace string) any {
	if s == nil {
		return result
	}
	full, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result
	}
	id, err := s.PutArtifact(&store.Artifact{Type: "graph", Workspace: workspace, Body: string(full)})
	if err != nil {
		return result
	}
	if _, err := s.GetArtifact(id, workspace); err != nil {
		return result
	}
	statuses := make(map[string]string, len(result.Outputs))
	outputs := make(map[string]executor.NodeOutput)
	intermediate := make(map[string]bool)
	for _, node := range g.Nodes {
		for _, dependency := range node.DependsOn {
			intermediate[dependency] = true
		}
	}
	for name, output := range result.Outputs {
		statuses[name] = output.Status
		if output.Status != "completed" || (len(g.Returns) == 0 && !intermediate[name]) {
			outputs[name] = output
		}
	}
	response := map[string]any{
		"task_id": result.TaskID, "status": result.Status,
		"node_statuses": statuses, "artifact_id": id,
		"expand": "tzro expand " + id,
	}
	if len(outputs) != 0 {
		response["outputs"] = outputs
	}
	if len(result.Returns) != 0 {
		response["returns"] = result.Returns
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > 8000 {
		delete(response, "outputs")
		delete(response, "returns")
		response["omitted"] = "Result exceeds the inline limit; expand the retained graph evidence."
		encoded, _ = json.Marshal(response)
		if len(encoded) > 8000 {
			delete(response, "node_statuses")
		}
	}
	return response
}
