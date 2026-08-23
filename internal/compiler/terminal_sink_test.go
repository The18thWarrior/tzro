package compiler

import (
	"testing"
)

func TestCompiler_InjectsTerminalWriteFileSink(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID:     "task_terminal_sink_test",
		GoalPrompt: "Generate complete module documentation and save to module_docs.md",
		Nodes: []GraphNode{
			{
				ID:           "probe_node",
				Type:         "probe",
				Instructions: "Explore the codebase modules",
				AllowedTools: []string{"read_file", "list_dir"},
			},
			{
				ID:           "synthesis_node",
				Type:         "synthesis",
				Instructions: "Compile all module findings into markdown",
			},
		},
		Edges: []GraphEdge{
			{SourceID: "probe_node", TargetID: "synthesis_node"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Verify that a write_file node was injected and wired to the synthesis output
	var hasWriteFile bool
	var sinkNode *GraphNode
	for _, n := range expanded.Nodes {
		if n.Action == "write_file" || (n.Type == "action" && n.Action == "write_file") {
			hasWriteFile = true
			sinkNode = &n
			break
		}
	}

	if !hasWriteFile || sinkNode == nil {
		t.Fatalf("expected compiler to inject a terminal write_file sink node, got nodes: %+v", expanded.Nodes)
	}

	// Check dynamic binding to synthesis
	if sinkNode.DynamicBindings == nil {
		t.Errorf("expected dynamicBindings on terminal sink node, got nil")
	}

	// Check that an edge connects the synthesis node to the sink node
	var hasEdge bool
	for _, e := range expanded.Edges {
		if e.TargetID == sinkNode.ID || e.TargetID == sinkNode.ID+"_validator" {
			hasEdge = true
			break
		}
	}

	if !hasEdge {
		t.Errorf("expected dependency edge from synthesis to terminal sink node %s", sinkNode.ID)
	}
}

func TestCompiler_SkipsSinkIfWriteFileAlreadyExists(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID:     "task_sink_exists_test",
		GoalPrompt: "Generate documentation and write to docs.md",
		Nodes: []GraphNode{
			{
				ID:           "synthesis_node",
				Type:         "synthesis",
				Instructions: "Compile findings",
			},
			{
				ID:           "explicit_write",
				Type:         "action",
				Action:       "write_file",
				Instructions: "Write to docs.md",
				StaticArgs:   `{"path": "docs.md"}`,
			},
		},
		Edges: []GraphEdge{
			{SourceID: "synthesis_node", TargetID: "explicit_write"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	for _, n := range expanded.Nodes {
		if n.ID == "terminal_write_file" {
			t.Errorf("expected terminal_write_file not to be injected when write_file already exists in graph")
		}
	}
}
