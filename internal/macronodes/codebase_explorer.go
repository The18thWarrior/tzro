package macronodes

import (
	"fmt"
	"time"
	"tzro/internal/compiler"
)

type CodebaseExplorer struct{}

func (c *CodebaseExplorer) Name() string {
	return "CodebaseExplorer"
}

func (c *CodebaseExplorer) Description() string {
	return "Explore a codebase and generate an architectural summary."
}

func (c *CodebaseExplorer) RequiredInputs() []string {
	return []string{"target_dir"}
}

func (c *CodebaseExplorer) BuildGraph(taskID string, inputs map[string]interface{}) (*compiler.ExecutionGraph, error) {
	targetDir := fmt.Sprintf("%v", inputs["target_dir"])

	nodes := []compiler.GraphNode{
		{
			ID:           "probe_explorer",
			Type:         "probe",
			Action:       "",
			Instructions: fmt.Sprintf("Explore the codebase at '%s'. Understand the top-level structure, identify key components, and produce a high-level architectural map.", targetDir),
			AllowedTools: []string{"read_file", "list_dir", "search_files"},
			Status:       "pending",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:         fmt.Sprintf("Explore the codebase at '%s'. Understand the top-level structure, identify key components, and produce a high-level architectural map.", targetDir),
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				StepBudget:   20,
				CompactEvery: 3,
			},
		},
		{
			ID:           "synthesis",
			Type:         "synthesis",
			Action:       "",
			Instructions: "Synthesize the architecture map.",
			AllowedTools: []string{},
			Status:       "pending",
		},
	}
	edges := []compiler.GraphEdge{
		{SourceID: "probe_explorer", TargetID: "synthesis"},
	}

	return &compiler.ExecutionGraph{
		TaskID:    taskID,
		CreatedAt: time.Now().Unix(),
		MaxCycles: 5,
		Nodes:     nodes,
		Edges:     edges,
	}, nil
}
