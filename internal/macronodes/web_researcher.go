package macronodes

import (
	"fmt"
	"time"
	"tzro/internal/compiler"
)

type WebResearcher struct{}

func (w *WebResearcher) Name() string {
	return "WebResearcher"
}

func (w *WebResearcher) Description() string {
	return "Search the web for a topic and synthesize a structured report."
}

func (w *WebResearcher) RequiredInputs() []string {
	return []string{"topic"}
}

func (w *WebResearcher) BuildGraph(taskID string, inputs map[string]interface{}) (*compiler.ExecutionGraph, error) {
	topic := fmt.Sprintf("%v", inputs["topic"])

	nodes := []compiler.GraphNode{
		{
			ID:           "probe_web",
			Type:         "probe",
			Action:       "",
			Instructions: fmt.Sprintf("Search the web for '%s'. Read the top results and extract key information.", topic),
			AllowedTools: []string{"web_search"},
			Status:       "pending",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:         fmt.Sprintf("Search the web for '%s'. Read the top results and extract key information.", topic),
				AllowedTools: []string{"web_search"},
				StepBudget:   15,
				CompactEvery: 3,
			},
		},
		{
			ID:           "synthesis",
			Type:         "synthesis",
			Action:       "",
			Instructions: "Synthesize the web research into a structured report.",
			AllowedTools: []string{},
			Status:       "pending",
		},
	}
	edges := []compiler.GraphEdge{
		{SourceID: "probe_web", TargetID: "synthesis"},
	}

	return &compiler.ExecutionGraph{
		TaskID:    taskID,
		CreatedAt: time.Now().Unix(),
		MaxCycles: 5,
		Nodes:     nodes,
		Edges:     edges,
	}, nil
}
