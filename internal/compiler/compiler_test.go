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
	// Test 1: Recall should ALWAYS be injected for probes, even with a synthesis child.
	// Recall compaction (100K→30K chars) is fundamentally different from planned synthesis.
	graphWithSynthChild := &ExecutionGraph{
		TaskID: "recall_always",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore codebase"},
			{ID: "s1", Type: "synthesis", Instructions: "Summarize findings"},
		},
		Edges: []GraphEdge{
			{SourceID: "p1", TargetID: "s1"},
		},
	}

	expanded1, _ := ExpandToSCTGraph(graphWithSynthChild, nil)
	hasRecall := false
	for _, n := range expanded1.Nodes {
		if n.ID == "p1_recall" {
			hasRecall = true
		}
	}
	if !hasRecall {
		t.Errorf("expected p1_recall to be injected even when synthesis child exists")
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

func TestSourceHint_WebInjectsTools(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_source_hint_web",
		Nodes: []GraphNode{
			{
				ID:           "web_research",
				Type:         "probe",
				Instructions: "Research AI frameworks", // Generic — no web keywords
				AllowedTools: []string{"read_file"},    // No web tools
				ProbeConfig: &ProbeConfig{
					Goal:         "Research AI frameworks",
					AllowedTools: []string{"read_file"},
					StepBudget:   15,
					CompactEvery: 3,
					SourceHint:   "web", // Declarative web hint
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
		if node.ID == "web_research" {
			probeNode = &expanded.Nodes[i]
			break
		}
	}
	if probeNode == nil {
		t.Fatal("probe node not found")
	}

	// SourceHint=web should inject web tools even without research keywords
	if !hasWebToolsInAllowed(probeNode.AllowedTools) {
		t.Errorf("expected web tools from SourceHint=web, got: %v", probeNode.AllowedTools)
	}
	if !hasWebToolsInAllowed(probeNode.ProbeConfig.AllowedTools) {
		t.Errorf("expected web tools in ProbeConfig.AllowedTools from SourceHint=web, got: %v", probeNode.ProbeConfig.AllowedTools)
	}
}

func TestSourceHint_FilesystemDefault(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_source_hint_filesystem",
		Nodes: []GraphNode{
			{
				ID:           "explore_code",
				Type:         "probe",
				Instructions: "Explore the project structure",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				ProbeConfig: &ProbeConfig{
					Goal:         "Explore the project",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   20,
					CompactEvery: 3,
					SourceHint:   "filesystem", // Explicit filesystem hint
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
		if node.ID == "explore_code" {
			probeNode = &expanded.Nodes[i]
			break
		}
	}
	if probeNode == nil {
		t.Fatal("probe node not found")
	}

	// Filesystem hint should NOT inject web tools
	if hasWebToolsInAllowed(probeNode.AllowedTools) {
		t.Errorf("filesystem SourceHint should NOT inject web tools, got: %v", probeNode.AllowedTools)
	}
}

func TestSourceHint_EmptyFallsBackToHeuristic(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_source_hint_empty",
		Nodes: []GraphNode{
			{
				ID:           "search_implicit",
				Type:         "probe",
				Instructions: "Search the web for Go security best practices and find authoritative sources",
				AllowedTools: []string{"read_file"},
				ProbeConfig: &ProbeConfig{
					Goal:         "Search for security best practices",
					AllowedTools: []string{"read_file"},
					StepBudget:   15,
					CompactEvery: 3,
					// No SourceHint — should fall back to heuristic
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
		if node.ID == "search_implicit" {
			probeNode = &expanded.Nodes[i]
			break
		}
	}
	if probeNode == nil {
		t.Fatal("probe node not found")
	}

	// Without SourceHint, the heuristic should still detect research intent
	if !hasWebToolsInAllowed(probeNode.AllowedTools) {
		t.Errorf("expected heuristic fallback to inject web tools, got: %v", probeNode.AllowedTools)
	}
}

func TestExpandToSCTGraph_SingleProbe_InjectsRecall(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "single_probe_recall",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore the codebase"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	hasRecall := false
	for _, n := range expanded.Nodes {
		if n.ID == "p1_recall" {
			hasRecall = true
			if n.Type != "recall" {
				t.Errorf("expected p1_recall type=recall, got %s", n.Type)
			}
		}
	}
	if !hasRecall {
		t.Error("expected p1_recall to be injected for single-probe DAG (ADR-0072)")
	}

	// Recall should be the terminal output — no terminal_synthesis injected
	hasTerminal := false
	for _, n := range expanded.Nodes {
		if n.ID == "terminal_synthesis" {
			hasTerminal = true
		}
	}
	if hasTerminal {
		t.Error("expected no terminal_synthesis — Recall Node is the terminal output for single-probe DAGs")
	}
}

func TestExpandToSCTGraph_SingleProbeWithToolSink_SkipsTerminalSynthesis(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "probe_plus_action",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore the codebase"},
			{ID: "a1", Type: "action", Action: "write_file", Instructions: "Save results"},
		},
		Edges: []GraphEdge{
			{SourceID: "p1", TargetID: "a1"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	hasRecall := false
	hasExec := false
	hasTerminal := false
	for _, n := range expanded.Nodes {
		if n.ID == "p1_recall" {
			hasRecall = true
		}
		if n.ID == "a1_exec" {
			hasExec = true
		}
		if n.ID == "terminal_synthesis" {
			hasTerminal = true
		}
	}
	if !hasRecall {
		t.Error("expected p1_recall to be injected when probe feeds tool sink")
	}
	if !hasExec {
		t.Error("expected a1_exec to be generated")
	}
	if hasTerminal {
		t.Error("expected terminal_synthesis to be skipped when terminal leaf is a tool sink (write_file)")
	}
}

func TestExpandToSCTGraph_MultiProbeWithoutSink_InjectsTerminalSynthesis(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "multi_probe_exploration",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore package A"},
			{ID: "p2", Type: "probe", Instructions: "Explore package B"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	hasTerminal := false
	for _, n := range expanded.Nodes {
		if n.ID == "terminal_synthesis" {
			hasTerminal = true
		}
	}
	if !hasTerminal {
		t.Error("expected terminal_synthesis for multi-probe exploration fan-out")
	}
}

func TestExpandToSCTGraph_MultiProbeWithToolSink_SkipsTerminalSynthesis(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "multi_probe_sink",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore package A"},
			{ID: "p2", Type: "probe", Instructions: "Explore package B"},
			{ID: "w1", Type: "action", Action: "write_file", Instructions: "Write summary to file"},
		},
		Edges: []GraphEdge{
			{SourceID: "p1", TargetID: "w1"},
			{SourceID: "p2", TargetID: "w1"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	hasTerminal := false
	for _, n := range expanded.Nodes {
		if n.ID == "terminal_synthesis" {
			hasTerminal = true
		}
	}
	if hasTerminal {
		t.Error("expected terminal_synthesis to be skipped when multi-probe DAG terminates in write_file")
	}
}

// testExpander implements NodeExpander for testing.
type testExpander struct {
	expandFunc func(node *GraphNode, graph *ExecutionGraph) (*NodeExpansionResult, error)
}

func (e *testExpander) Expand(node *GraphNode, graph *ExecutionGraph) (*NodeExpansionResult, error) {
	return e.expandFunc(node, graph)
}

func TestActiveExpander_CustomTypeHandled(t *testing.T) {
	// Set up a custom expander that handles "custom_analytics" nodes
	// by splitting them into a validator + exec pair.
	origExpander := ActiveExpander
	defer func() { ActiveExpander = origExpander }()

	ActiveExpander = &testExpander{
		expandFunc: func(node *GraphNode, graph *ExecutionGraph) (*NodeExpansionResult, error) {
			if node.Type != "custom_analytics" {
				return nil, nil // fall through to built-in
			}
			return &NodeExpansionResult{
				ReplacementNodes: []GraphNode{
					{ID: node.ID + "_prep", Type: "custom_analytics", Instructions: "Prepare data"},
					{ID: node.ID + "_run", Type: "custom_analytics", Instructions: "Run analytics"},
				},
				AdditionalEdges: []GraphEdge{
					{SourceID: node.ID + "_prep", TargetID: node.ID + "_run"},
				},
			}, nil
		},
	}

	graph := &ExecutionGraph{
		TaskID: "test_custom_expander",
		Nodes: []GraphNode{
			{ID: "analytics1", Type: "custom_analytics", Instructions: "Analyze sales data"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Should have prep + run nodes, not the original
	nodeIDs := make(map[string]bool)
	for _, n := range expanded.Nodes {
		nodeIDs[n.ID] = true
	}

	if !nodeIDs["analytics1_prep"] {
		t.Error("expected analytics1_prep node from custom expander")
	}
	if !nodeIDs["analytics1_run"] {
		t.Error("expected analytics1_run node from custom expander")
	}
	if nodeIDs["analytics1"] {
		t.Error("original node should have been replaced by custom expander")
	}
}

func TestActiveExpander_NilFallthrough(t *testing.T) {
	// When ActiveExpander returns nil, built-in logic runs.
	origExpander := ActiveExpander
	defer func() { ActiveExpander = origExpander }()

	ActiveExpander = &testExpander{
		expandFunc: func(node *GraphNode, graph *ExecutionGraph) (*NodeExpansionResult, error) {
			return nil, nil // always fall through
		},
	}

	graph := &ExecutionGraph{
		TaskID: "test_nil_expander",
		Nodes: []GraphNode{
			{ID: "p1", Type: "probe", Instructions: "Explore codebase"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Should still have probe node (built-in logic handles it)
	found := false
	for _, n := range expanded.Nodes {
		if n.ID == "p1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected probe node p1 to pass through via built-in logic")
	}
}

func TestExpandToSCTGraph_WriteFileAutoBinding(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_write_file_binding",
		Nodes: []GraphNode{
			{ID: "explore", Type: "probe", Instructions: "Explore codebase at internal/cache"},
			{ID: "write_docs", Type: "action", Action: "write_file", Instructions: "Write function index to function_index.md"},
		},
		Edges: []GraphEdge{
			{SourceID: "explore", TargetID: "write_docs"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, func(action string) (string, error) {
		return `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`, nil
	})
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Verify write_docs_validator and write_docs_exec both received DynamicBindings["content"]
	var validatorNode, execNode *GraphNode
	for i := range expanded.Nodes {
		if expanded.Nodes[i].ID == "write_docs_validator" {
			validatorNode = &expanded.Nodes[i]
		}
		if expanded.Nodes[i].ID == "write_docs_exec" {
			execNode = &expanded.Nodes[i]
		}
	}

	if validatorNode == nil {
		t.Fatal("expected write_docs_validator node")
	}
	if execNode == nil {
		t.Fatal("expected write_docs_exec node")
	}

	if validatorNode.DynamicBindings == nil || validatorNode.DynamicBindings["content"] != "explore_recall.output" {
		t.Errorf("expected validatorNode.DynamicBindings[\"content\"] = \"explore_recall.output\", got %v", validatorNode.DynamicBindings)
	}
	if execNode.DynamicBindings == nil || execNode.DynamicBindings["content"] != "explore_recall.output" {
		t.Errorf("expected execNode.DynamicBindings[\"content\"] = \"explore_recall.output\", got %v", execNode.DynamicBindings)
	}
}

// ── Slice 4: Dependency-Gated Recall Injection Tests (ADR-0079) ─────────────

func TestExpandToSCTGraph_MultiProbe_OmitsIntermediateRecall(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_multi_probe_exploration",
		Nodes: []GraphNode{
			{ID: "explore_core", Type: "probe", Instructions: "Explore core layer"},
			{ID: "explore_local", Type: "probe", Instructions: "Explore local layer"},
			{ID: "explore_routing", Type: "probe", Instructions: "Explore routing layer"},
			{ID: "synthesize_final", Type: "synthesis", Instructions: "Synthesize all layers"},
		},
		Edges: []GraphEdge{
			{SourceID: "explore_core", TargetID: "synthesize_final"},
			{SourceID: "explore_local", TargetID: "synthesize_final"},
			{SourceID: "explore_routing", TargetID: "synthesize_final"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Verify that intermediate recall nodes were NOT injected for pure exploration probes
	for _, n := range expanded.Nodes {
		if n.ID == "explore_core_recall" || n.ID == "explore_local_recall" || n.ID == "explore_routing_recall" {
			t.Errorf("unexpected intermediate recall node %q in multi-probe exploration graph", n.ID)
		}
	}
}

func TestExpandToSCTGraph_MultiProbe_InjectsRecallForToolSink(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_multi_probe_with_sink",
		Nodes: []GraphNode{
			{ID: "explore_core", Type: "probe", Instructions: "Explore core layer"},
			{ID: "write_core_docs", Type: "action", Action: "write_file", Instructions: "Write core docs"},
			{ID: "explore_support", Type: "probe", Instructions: "Explore support layer"},
		},
		Edges: []GraphEdge{
			{SourceID: "explore_core", TargetID: "write_core_docs"},
			{SourceID: "explore_support", TargetID: "write_core_docs"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Because both probes feed write_core_docs (a Tool Sink), recall nodes MUST be injected
	hasCoreRecall := false
	hasSupportRecall := false
	for _, n := range expanded.Nodes {
		if n.ID == "explore_core_recall" {
			hasCoreRecall = true
		}
		if n.ID == "explore_support_recall" {
			hasSupportRecall = true
		}
	}

	if !hasCoreRecall {
		t.Error("expected explore_core_recall to be injected when feeding write_file tool sink")
	}
	if !hasSupportRecall {
		t.Error("expected explore_support_recall to be injected when feeding write_file tool sink")
	}
}

func TestExpandToSCTGraph_SingleProbe_AlwaysInjectsRecall(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_single_probe_safety",
		Nodes: []GraphNode{
			{ID: "explore_single", Type: "probe", Instructions: "Explore directory structure"},
		},
		Edges: []GraphEdge{},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Single probe with no children MUST inject recall per ADR-0072
	hasRecall := false
	for _, n := range expanded.Nodes {
		if n.ID == "explore_single_recall" {
			hasRecall = true
			break
		}
	}

	if !hasRecall {
		t.Error("expected explore_single_recall to be injected for single-probe DAG (ADR-0072)")
	}
}

func TestExpandToSCTGraph_MultiProbe_Sequential_OmitsIntermediateRecall(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_multi_probe_sequential",
		Nodes: []GraphNode{
			{ID: "explore_core", Type: "probe", Instructions: "Explore core layer"},
			{ID: "explore_local", Type: "probe", Instructions: "Explore local layer"},
			{ID: "explore_routing", Type: "probe", Instructions: "Explore routing layer"},
		},
		Edges: []GraphEdge{
			{SourceID: "explore_core", TargetID: "explore_local"},
			{SourceID: "explore_local", TargetID: "explore_routing"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// explore_core and explore_local should NOT get recall nodes because they feed another probe, not a tool sink
	for _, n := range expanded.Nodes {
		if n.ID == "explore_core_recall" || n.ID == "explore_local_recall" {
			t.Errorf("unexpected intermediate recall node %q for probe feeding another probe", n.ID)
		}
	}
}

// --- Slice 2 RED (Run 32 fix): per-leaf synthesis injection ---

// TestSCT_SynthesisGoalAnalyzeLeaf_DoesNotMaskExecLeaf verifies that when an
// analyze node (whose instructions contain "synthesize") exists alongside a
// real non-sink action leaf, terminal_synthesis IS injected for the exec leaf.
//
// Root cause in lead_source_by_owner: isSynthesisGoal(node.Instructions) fired
// on the analyze node, set hasSynthesisLeaf=true, and suppressed synthesis
// injection for the actual exec leaf.
// The fix: needsSynthesis checks node.Type only — isSynthesisGoal() on
// arbitrary instructions no longer gates injection.
func TestSCT_SynthesisGoalAnalyzeLeaf_DoesNotMaskExecLeaf(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID:     "lead_source_test",
		GoalPrompt: "list leads by owner",
		Nodes: []GraphNode{
			{ID: "read_leads", Type: "probe",
				Instructions: "read the leads csv"},
			// analyze_leads: instructions contain "synthesize" — old isSynthesisGoal
			// would set hasSynthesisLeaf=true and suppress synthesis for exec_agg.
			{ID: "analyze_leads", Type: "analyze",
				Instructions: "analyze and synthesize the lead source breakdown",
				ProbeConfig:  &ProbeConfig{SourceHint: ""}},
			// exec_agg: non-sink action leaf — must trigger terminal_synthesis injection.
			{ID: "exec_agg", Type: "action", Action: "sql_cached_data",
				Instructions: "aggregate leads by owner"},
		},
		Edges: []GraphEdge{
			{SourceID: "read_leads", TargetID: "analyze_leads"},
			{SourceID: "analyze_leads", TargetID: "exec_agg"},
		},
	}
	compiled, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}
	var hasSynth bool
	for _, n := range compiled.Nodes {
		if n.ID == "terminal_synthesis" {
			hasSynth = true
		}
	}
	if !hasSynth {
		t.Error("expected terminal_synthesis to be injected: exec_agg is a non-sink terminal leaf; " +
			"analyze_leads 'synthesize' in instructions must NOT suppress injection via isSynthesisGoal()")
	}
}

// TestSCT_WriteFileLeaf_NoSynthesisInjected verifies that a graph whose only
// terminal leaf is a write_file action does NOT receive an auto-injected
// terminal_synthesis node.
func TestSCT_WriteFileLeaf_NoSynthesisInjected(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID:     "write_test",
		GoalPrompt: "write the readme file",
		Nodes: []GraphNode{
			{ID: "probe_explore", Type: "probe", Instructions: "explore the codebase"},
			{ID: "write_exec", Type: "action", Action: "write_file",
				Instructions: "write the README.md file"},
		},
		Edges: []GraphEdge{
			{SourceID: "probe_explore", TargetID: "write_exec"},
		},
	}
	compiled, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}
	for _, n := range compiled.Nodes {
		if n.ID == "terminal_synthesis" {
			t.Error("terminal_synthesis must NOT be injected when all terminal leaves are tool sinks (write_file)")
		}
	}
}
