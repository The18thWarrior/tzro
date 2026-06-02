package compiler

import (
	"testing"
)

func TestCompileAndSortSuccess(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_task_1",
		Nodes: []GraphNode{
			{ID: "A", Type: "action", Action: "salesforce_query"},
			{ID: "B", Type: "action", Action: "postgres_insert"},
			{ID: "C", Type: "action", Action: "slack_message"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
		},
	}

	levels, err := CompileAndSort(graph)
	if err != nil {
		t.Fatalf("unexpected error compiling graph: %v", err)
	}

	if len(levels) != 3 {
		t.Errorf("expected 3 execution levels, got %d", len(levels))
	}

	if levels[0][0] != "A" || levels[1][0] != "B" || levels[2][0] != "C" {
		t.Errorf("incorrect topological levels sequence: %v", levels)
	}
}

func TestCompileAndSortCycle(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_task_cycle",
		Nodes: []GraphNode{
			{ID: "A", Type: "action"},
			{ID: "B", Type: "action"},
		},
		Edges: []GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "A"},
		},
	}

	_, err := CompileAndSort(graph)
	if err == nil {
		t.Fatalf("expected cyclical compile error, but got nil")
	}
}

func TestStrategicGraphToSCTExpansion(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "sct_test_1",
		Nodes: []GraphNode{
			{ID: "step1", Type: "action", Action: "web_search", Instructions: "Find Qwen GGUF details"},
			{ID: "step2", Type: "action", Action: "slack_message", Instructions: "Send details to engineering channel"},
		},
		Edges: []GraphEdge{
			{SourceID: "step1", TargetID: "step2"},
		},
	}

	mockSchemaResolver := func(action string) (string, error) {
		if action == "web_search" {
			return `{"mock": "web_search_schema"}`, nil
		}
		return `{"mock": "slack_schema"}`, nil
	}

	expanded, err := ExpandToSCTGraph(graph, mockSchemaResolver)
	if err != nil {
		t.Fatalf("failed to expand strategic graph to SCT graph: %v", err)
	}

	// We expect:
	// 1. step1_bridge (gbnf_bridge)
	// 2. step1_exec (deterministic)
	// 3. step2_bridge (gbnf_bridge)
	// 4. step2_exec (deterministic)
	// 5. terminal_synthesis (synthesis)
	// Total 5 nodes
	if len(expanded.Nodes) != 5 {
		t.Errorf("expected 5 expanded nodes, got: %d", len(expanded.Nodes))
	}

	nodeMap := make(map[string]GraphNode)
	for _, n := range expanded.Nodes {
		nodeMap[n.ID] = n
	}

	// Assert correct node types and structures
	if nodeMap["step1_bridge"].Type != "gbnf_bridge" || nodeMap["step1_bridge"].OutputSchema != `{"mock": "web_search_schema"}` {
		t.Errorf("incorrect step1_bridge: %v", nodeMap["step1_bridge"])
	}
	if nodeMap["step1_exec"].Type != "deterministic" {
		t.Errorf("incorrect step1_exec: %v", nodeMap["step1_exec"])
	}
	if nodeMap["step2_bridge"].Type != "gbnf_bridge" {
		t.Errorf("incorrect step2_bridge: %v", nodeMap["step2_bridge"])
	}
	if nodeMap["step2_exec"].Type != "deterministic" {
		t.Errorf("incorrect step2_exec: %v", nodeMap["step2_exec"])
	}
	if nodeMap["terminal_synthesis"].Type != "synthesis" {
		t.Errorf("incorrect terminal_synthesis: %v", nodeMap["terminal_synthesis"])
	}

	// Assert topological sorting works on sct graph (should be green/cycle-free)
	levels, err := CompileAndSort(expanded)
	if err != nil {
		t.Fatalf("topological sort failed on expanded SCT graph: %v", err)
	}

	if len(levels) != 5 {
		t.Errorf("expected 5 topological levels, got: %d (%v)", len(levels), levels)
	}
}
