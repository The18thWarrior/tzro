package macronodes

import (
	"fmt"
	"time"
	"tzro/internal/compiler"
)

type DataAnalyzer struct{}

func (d *DataAnalyzer) Name() string {
	return "DataAnalyzer"
}

func (d *DataAnalyzer) Description() string {
	return "Profile a dataset and return statistical summaries."
}

func (d *DataAnalyzer) RequiredInputs() []string {
	return []string{"dataset_path"}
}

func (d *DataAnalyzer) BuildGraph(taskID string, inputs map[string]interface{}) (*compiler.ExecutionGraph, error) {
	datasetPath := fmt.Sprintf("%v", inputs["dataset_path"])

	nodes := []compiler.GraphNode{
		{
			ID:           "probe_data",
			Type:         "list",
			Action:       "",
			Instructions: fmt.Sprintf("Analyze the dataset at '%s'. Determine its schema, identify key fields, and produce a statistical summary.", datasetPath),
			AllowedTools: []string{"read_file", "search_files"},
			Status:       "pending",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:         fmt.Sprintf("Analyze the dataset at '%s'. Determine its schema, identify key fields, and produce a statistical summary.", datasetPath),
				AllowedTools: []string{"read_file", "search_files"},
				StepBudget:   15,
				CompactEvery: 3,
			},
		},
		{
			ID:           "synthesis",
			Type:         "synthesis",
			Action:       "",
			Instructions: "Synthesize the data analysis.",
			AllowedTools: []string{},
			Status:       "pending",
		},
	}
	edges := []compiler.GraphEdge{
		{SourceID: "probe_data", TargetID: "synthesis"},
	}

	return &compiler.ExecutionGraph{
		TaskID:    taskID,
		CreatedAt: time.Now().Unix(),
		MaxCycles: 5,
		Nodes:     nodes,
		Edges:     edges,
	}, nil
}
