package compiler

import (
	"testing"
	"time"
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
	// 1. step1_validator (semantic_validator)
	// 2. step1_exec (deterministic)
	// 3. step2_validator (semantic_validator)
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
	if nodeMap["step1_validator"].Type != "semantic_validator" || nodeMap["step1_validator"].OutputSchema != `{"mock": "web_search_schema"}` {
		t.Errorf("incorrect step1_validator: %v", nodeMap["step1_validator"])
	}
	if nodeMap["step1_exec"].Type != "deterministic" {
		t.Errorf("incorrect step1_exec: %v", nodeMap["step1_exec"])
	}
	if nodeMap["step2_validator"].Type != "semantic_validator" {
		t.Errorf("incorrect step2_validator: %v", nodeMap["step2_validator"])
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

func TestExpandToSCTGraph_PlanningAware(t *testing.T) {
	// Test 1: Skip Recall if Synthesis child exists
	graphRecallSkip := &ExecutionGraph{
		TaskID: "recall_skip",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore codebase"},
			{ID: "s1", Type: "synthesis", Instructions: "Summarize findings"},
		},
		Edges: []GraphEdge{
			{SourceID: "p1", TargetID: "s1"},
		},
	}

	expanded1, _ := ExpandToSCTGraph(graphRecallSkip, nil)
	hasRecall := false
	for _, n := range expanded1.Nodes {
		if n.ID == "p1_recall" {
			hasRecall = true
		}
	}
	if hasRecall {
		t.Errorf("expected p1_recall to be skipped when synthesis child exists")
	}

	// Test 2: Skip Terminal Synthesis if a synthesis node is already the final leaf
	graphTerminalSkip := &ExecutionGraph{
		TaskID: "terminal_skip",
		Nodes: []GraphNode{
			{ID: "action1", Type: "action", Action: "web_search"},
			{ID: "final_synth", Type: "synthesis", Instructions: "Final report"},
		},
		Edges: []GraphEdge{
			{SourceID: "action1", TargetID: "final_synth"},
		},
	}

	expanded2, _ := ExpandToSCTGraph(graphTerminalSkip, nil)
	hasTerminalSynth := false
	for _, n := range expanded2.Nodes {
		if n.ID == "terminal_synthesis" {
			hasTerminalSynth = true
		}
	}
	if hasTerminalSynth {
		t.Errorf("expected terminal_synthesis to be skipped when a synthesis leaf already exists")
	}
}

func TestComputeTimeBudget_Exploration(t *testing.T) {
	// Typical exploration: 1 probe + 1 synthesis
	graph := &ExecutionGraph{
		Nodes: []GraphNode{
			{ID: "explore", Type: "probe"},
			{ID: "synth", Type: "synthesis"},
		},
	}
	budget := ComputeTimeBudget(graph)
	expected := 10*time.Minute + 90*time.Second // probe + synthesis
	if budget != expected {
		t.Errorf("expected %s, got %s", expected, budget)
	}
}

func TestComputeTimeBudget_Pipeline(t *testing.T) {
	// Multi-step pipeline: 2 actions + 2 validators + 1 synthesis
	graph := &ExecutionGraph{
		Nodes: []GraphNode{
			{ID: "a_validator", Type: "semantic_validator"},
			{ID: "a_exec", Type: "deterministic"},
			{ID: "b_validator", Type: "semantic_validator"},
			{ID: "b_exec", Type: "deterministic"},
			{ID: "synth", Type: "synthesis"},
		},
	}
	budget := ComputeTimeBudget(graph)
	expected := 4*90*time.Second + 90*time.Second // 4×90s (validators+deterministic) + 90s (synthesis)
	if budget != expected {
		t.Errorf("expected %s, got %s", expected, budget)
	}
}

func TestComputeTimeBudget_EmptyGraph(t *testing.T) {
	graph := &ExecutionGraph{Nodes: []GraphNode{}}
	budget := ComputeTimeBudget(graph)
	if budget != 0 {
		t.Errorf("expected 0 budget for empty graph, got %s", budget)
	}
}

func TestComputeTimeBudget_UnknownType(t *testing.T) {
	graph := &ExecutionGraph{
		Nodes: []GraphNode{
			{ID: "custom", Type: "custom_unknown"},
		},
	}
	budget := ComputeTimeBudget(graph)
	expected := 90 * time.Second // default
	if budget != expected {
		t.Errorf("expected %s for unknown type, got %s", expected, budget)
	}
}
