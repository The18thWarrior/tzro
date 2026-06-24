package macronodes

import (
	"fmt"
	"time"
	"tzro/internal/compiler"
)

type MemoryIngestionPipeline struct{}

func (m *MemoryIngestionPipeline) Name() string {
	return "MemoryIngestionPipeline"
}

func (m *MemoryIngestionPipeline) Description() string {
	return "Extract facts from raw text and store them into the memory system."
}

func (m *MemoryIngestionPipeline) RequiredInputs() []string {
	return []string{"raw_text", "source"}
}

func (m *MemoryIngestionPipeline) BuildGraph(taskID string, inputs map[string]interface{}) (*compiler.ExecutionGraph, error) {
	rawText := fmt.Sprintf("%v", inputs["raw_text"])
	source := fmt.Sprintf("%v", inputs["source"])

	nodes := []compiler.GraphNode{
		{
			ID:           "extract_facts",
			Type:         "action",
			Action:       "tzro_completion",
			Instructions: fmt.Sprintf("Extract key facts, entities, and relationships from the following text (Source: %s):\n\n%s", source, rawText),
			AllowedTools: []string{"tzro_completion"},
			Status:       "pending",
		},
		{
			ID:           "store_memory",
			Type:         "action",
			Action:       "tzro_memory_ingest",
			Instructions: fmt.Sprintf("Store the extracted facts into the memory system with source '%s'.", source),
			DynamicBindings: map[string]interface{}{
				"content": "extract_facts.output.response",
			},
			AllowedTools: []string{"tzro_memory_ingest"},
			Status:       "pending",
		},
		{
			ID:           "synthesis",
			Type:         "synthesis",
			Action:       "",
			Instructions: "Synthesize the memory ingestion results.",
			AllowedTools: []string{},
			Status:       "pending",
		},
	}
	edges := []compiler.GraphEdge{
		{SourceID: "extract_facts", TargetID: "store_memory"},
		{SourceID: "store_memory", TargetID: "synthesis"},
	}

	return &compiler.ExecutionGraph{
		TaskID:    taskID,
		CreatedAt: time.Now().Unix(),
		MaxCycles: 5,
		Nodes:     nodes,
		Edges:     edges,
	}, nil
}
