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

func TestLooksLikeResearchNode(t *testing.T) {
	tests := []struct {
		instructions string
		want         bool
	}{
		{"Search the web for the latest AI orchestration frameworks", true},
		{"Use web_search to find authoritative sources on Go CVEs", true},
		{"Browse the official documentation and extract pricing info", true},
		{"Find sources discussing GGUF quantization techniques online", true},
		{"Search for market trends in local-first inference on the internet", true},
		{"Read the codebase and summarize the architecture", false},
		{"Explore the directory structure and list all Go files", false},
		{"Analyze the CSV data and compute averages", false},
		{"Write a function that calculates compound interest", false},
	}

	for _, tt := range tests {
		got := looksLikeResearchNode(tt.instructions)
		if got != tt.want {
			preview := tt.instructions
			if len(preview) > 50 {
				preview = preview[:50]
			}
			t.Errorf("looksLikeResearchNode(%q) = %v, want %v", preview, got, tt.want)
		}
	}
}

func TestHasWebToolsInAllowed(t *testing.T) {
	if !hasWebToolsInAllowed([]string{"read_file", "web_search"}) {
		t.Error("should detect web_search")
	}
	if !hasWebToolsInAllowed([]string{"web_browse", "list_dir"}) {
		t.Error("should detect web_browse")
	}
	if hasWebToolsInAllowed([]string{"read_file", "list_dir", "search_files"}) {
		t.Error("should not match codebase tools")
	}
	if hasWebToolsInAllowed([]string{}) {
		t.Error("should not match empty list")
	}
}

func TestResearchToolPropagation_InjectsWebTools(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_research_propagation",
		Nodes: []GraphNode{
			{
				ID:           "search_frameworks",
				Type:         "probe",
				Instructions: "Search the web for the top 3 LLM orchestration frameworks and find authoritative sources.",
				AllowedTools: []string{"read_file", "list_dir"}, // Planner forgot web tools
				ProbeConfig: &ProbeConfig{
					Goal:         "Search the web for LLM frameworks",
					AllowedTools: []string{"read_file", "list_dir"},
					StepBudget:   15,
					CompactEvery: 3,
				},
			},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Find the probe node in the expanded graph
	var probeNode *GraphNode
	for i, node := range expanded.Nodes {
		if node.ID == "search_frameworks" {
			probeNode = &expanded.Nodes[i]
			break
		}
	}
	if probeNode == nil {
		t.Fatal("probe node not found in expanded graph")
	}

	// Verify web tools were injected into AllowedTools
	if !hasWebToolsInAllowed(probeNode.AllowedTools) {
		t.Errorf("expected web tools in AllowedTools, got: %v", probeNode.AllowedTools)
	}

	// Verify web tools were injected into ProbeConfig.AllowedTools
	if probeNode.ProbeConfig == nil {
		t.Fatal("ProbeConfig should not be nil")
	}
	if !hasWebToolsInAllowed(probeNode.ProbeConfig.AllowedTools) {
		t.Errorf("expected web tools in ProbeConfig.AllowedTools, got: %v", probeNode.ProbeConfig.AllowedTools)
	}
}

func TestResearchToolPropagation_DoesNotInjectForCodebaseProbe(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_no_research_propagation",
		Nodes: []GraphNode{
			{
				ID:           "explore_codebase",
				Type:         "probe",
				Instructions: "Read the codebase and explain the architecture of the executor package.",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				ProbeConfig: &ProbeConfig{
					Goal:         "Explore the executor package",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   20,
					CompactEvery: 3,
				},
			},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	var probeNode *GraphNode
	for i, node := range expanded.Nodes {
		if node.ID == "explore_codebase" {
			probeNode = &expanded.Nodes[i]
			break
		}
	}
	if probeNode == nil {
		t.Fatal("probe node not found")
	}

	// Should NOT have web tools injected
	if hasWebToolsInAllowed(probeNode.AllowedTools) {
		t.Errorf("web tools should NOT be injected for codebase exploration, got: %v", probeNode.AllowedTools)
	}
}

func TestResearchToolPropagation_NoDoubleInjection(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_no_double_injection",
		Nodes: []GraphNode{
			{
				ID:           "search_node",
				Type:         "probe",
				Instructions: "Search the web for Go security vulnerabilities",
				AllowedTools: []string{"web_search", "web_browse"}, // Already has web tools
				ProbeConfig: &ProbeConfig{
					Goal:         "Search for Go CVEs",
					AllowedTools: []string{"web_search", "web_browse"},
					StepBudget:   15,
					CompactEvery: 3,
				},
			},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	var probeNode *GraphNode
	for i, node := range expanded.Nodes {
		if node.ID == "search_node" {
			probeNode = &expanded.Nodes[i]
			break
		}
	}
	if probeNode == nil {
		t.Fatal("probe node not found")
	}

	// Count web_search occurrences — should be exactly 1 (no double injection)
	count := 0
	for _, tool := range probeNode.AllowedTools {
		if tool == "web_search" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 web_search in AllowedTools, got %d (tools: %v)", count, probeNode.AllowedTools)
	}
}

