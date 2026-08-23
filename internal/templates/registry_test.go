package templates

import (
	"strings"
	"testing"

	"tzro/internal/compiler"
)

func TestGet_ExploreOnly_ReturnsValidGraph(t *testing.T) {
	graph := Get(ExploreOnly)
	if graph == nil {
		t.Fatal("expected non-nil graph for ExploreOnly")
	}

	if len(graph.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]
	if node.Type != "probe" {
		t.Errorf("expected node type 'probe', got %q", node.Type)
	}
	if node.ProbeConfig == nil {
		t.Fatal("expected ProbeConfig on probe node")
	}

	// Verify probe has filesystem exploration tools
	toolSet := make(map[string]bool)
	for _, tool := range node.ProbeConfig.AllowedTools {
		toolSet[tool] = true
	}
	for _, required := range []string{"read_file", "list_dir", "search_files"} {
		if !toolSet[required] {
			t.Errorf("expected ProbeConfig.AllowedTools to contain %q", required)
		}
	}

	if len(graph.Edges) != 0 {
		t.Errorf("expected 0 edges for single-node template, got %d", len(graph.Edges))
	}
}

func TestGet_UnknownCategory_ReturnsNil(t *testing.T) {
	graph := Get("nonexistent")
	if graph != nil {
		t.Error("expected nil for unknown category")
	}
}

func TestCategories_ReturnsAllRegistered(t *testing.T) {
	cats := Categories()
	if len(cats) < 1 {
		t.Fatal("expected at least 1 category")
	}

	found := false
	for _, c := range cats {
		if c == ExploreOnly {
			found = true
		}
	}
	if !found {
		t.Error("expected Categories() to contain ExploreOnly")
	}
}

func TestGet_ReturnsCopy(t *testing.T) {
	g1 := Get(ExploreOnly)
	g2 := Get(ExploreOnly)

	// Mutate g1 and verify g2 is unaffected
	g1.TaskID = "mutated"
	g1.Nodes[0].Instructions = "mutated"

	if g2.TaskID == "mutated" {
		t.Error("Get() returned shared reference — TaskID mutation leaked")
	}
	if g2.Nodes[0].Instructions == "mutated" {
		t.Error("Get() returned shared reference — Node mutation leaked")
	}
}

func TestGet_ExploreOnly_PassesCompilation(t *testing.T) {
	graph := Get(ExploreOnly)
	if graph == nil {
		t.Fatal("expected non-nil graph")
	}

	expanded, err := compiler.ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	_, err = compiler.CompileAndSort(expanded)
	if err != nil {
		t.Fatalf("CompileAndSort failed: %v", err)
	}
}

func TestGet_Docgen_HasProbeAndWriteAction(t *testing.T) {
	graph := Get(Docgen)
	if graph == nil {
		t.Fatal("expected non-nil graph for Docgen")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	// Find probe and action nodes
	var probe, action *compiler.GraphNode
	for i := range graph.Nodes {
		switch graph.Nodes[i].Type {
		case "probe":
			probe = &graph.Nodes[i]
		case "action":
			action = &graph.Nodes[i]
		}
	}
	if probe == nil {
		t.Fatal("expected a probe node")
	}
	if action == nil {
		t.Fatal("expected an action node")
	}
	if action.Action != "write_file" {
		t.Errorf("expected action 'write_file', got %q", action.Action)
	}

	// Verify edge: probe → action
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != probe.ID || graph.Edges[0].TargetID != action.ID {
		t.Errorf("expected edge %s→%s, got %s→%s", probe.ID, action.ID, graph.Edges[0].SourceID, graph.Edges[0].TargetID)
	}

	// Verify NO dynamic bindings on action node — probe output flows via
	// AccumulatedContext (edge-driven), not DynamicBindings (which assume
	// structured JSON output the probe doesn't produce).
	if len(action.DynamicBindings) > 0 {
		t.Errorf("expected no DynamicBindings on probe→action template, got %v", action.DynamicBindings)
	}
}

func TestGet_Research_HasWebProbe(t *testing.T) {
	graph := GetWithModality(ProbeSynthesis, SourceWeb)
	if graph == nil {
		t.Fatal("expected non-nil graph for Research")
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]
	if node.Type != "probe" {
		t.Errorf("expected node type 'probe', got %q", node.Type)
	}
	if node.ProbeConfig == nil {
		t.Fatal("expected ProbeConfig")
	}
	if node.ProbeConfig.SourceHint != "web" {
		t.Errorf("expected SourceHint 'web', got %q", node.ProbeConfig.SourceHint)
	}

	toolSet := make(map[string]bool)
	for _, tool := range node.ProbeConfig.AllowedTools {
		toolSet[tool] = true
	}
	if !toolSet["web_search"] {
		t.Error("expected AllowedTools to contain 'web_search'")
	}
	if !toolSet["web_browse"] {
		t.Error("expected AllowedTools to contain 'web_browse'")
	}
}



func TestGet_DataAnalysis_HasReadFileAndAnalyze(t *testing.T) {
	graph := Get(DataAnalysis)
	if graph == nil {
		t.Fatal("expected non-nil graph for DataAnalysis")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	var readAction, analyzeNode *compiler.GraphNode
	for i := range graph.Nodes {
		if graph.Nodes[i].Type == "action" && graph.Nodes[i].Action == "read_file" {
			readAction = &graph.Nodes[i]
		}
		if graph.Nodes[i].Type == "analyze" {
			analyzeNode = &graph.Nodes[i]
		}
	}
	if readAction == nil {
		t.Fatal("expected a read_file action node")
	}
	if analyzeNode == nil {
		t.Fatal("expected an analyze node")
	}

	// Verify edge: read_file → analyze
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != readAction.ID || graph.Edges[0].TargetID != analyzeNode.ID {
		t.Errorf("expected edge %s→%s", readAction.ID, analyzeNode.ID)
	}
}

func TestGet_MultiProbeSynthesis_HasParallelProbes(t *testing.T) {
	graph := Get(MultiProbeSynthesis)
	if graph == nil {
		t.Fatal("expected non-nil graph for MultiProbeSynthesis")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	for _, n := range graph.Nodes {
		if n.Type != "probe" {
			t.Errorf("expected all nodes to be probe type, got %q", n.Type)
		}
	}

	// Parallel probes have no edges between them
	if len(graph.Edges) != 0 {
		t.Errorf("expected 0 edges (parallel probes), got %d", len(graph.Edges))
	}
}

func TestGet_Codegen_HasProbeAndTzroCode(t *testing.T) {
	graph := Get(Codegen)
	if graph == nil {
		t.Fatal("expected non-nil graph for Codegen")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	var probe, codeAction *compiler.GraphNode
	for i := range graph.Nodes {
		if graph.Nodes[i].Type == "probe" {
			probe = &graph.Nodes[i]
		}
		if graph.Nodes[i].Type == "action" && graph.Nodes[i].Action == "tzro_code" {
			codeAction = &graph.Nodes[i]
		}
	}
	if probe == nil {
		t.Fatal("expected a probe node")
	}
	if codeAction == nil {
		t.Fatal("expected a tzro_code action node")
	}

	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != probe.ID || graph.Edges[0].TargetID != codeAction.ID {
		t.Errorf("expected edge %s→%s", probe.ID, codeAction.ID)
	}
}

func TestGet_ActionChain_HasSequentialActions(t *testing.T) {
	graph := Get(ActionChain)
	if graph == nil {
		t.Fatal("expected non-nil graph for ActionChain")
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(graph.Nodes))
	}

	for _, n := range graph.Nodes {
		if n.Type != "action" {
			t.Errorf("expected all nodes to be action type, got %q", n.Type)
		}
	}

	// Sequential: 2 edges (a₁→a₂, a₂→a₃)
	if len(graph.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(graph.Edges))
	}
}

func TestGet_AllTemplates_PassCompilation(t *testing.T) {
	for _, cat := range Categories() {
		t.Run(string(cat), func(t *testing.T) {
			graph := Get(cat)
			if graph == nil {
				t.Fatalf("Get(%q) returned nil", cat)
			}

			expanded, err := compiler.ExpandToSCTGraph(graph, nil)
			if err != nil {
				t.Fatalf("ExpandToSCTGraph failed for %q: %v", cat, err)
			}

			_, err = compiler.CompileAndSort(expanded)
			if err != nil {
				t.Fatalf("CompileAndSort failed for %q: %v", cat, err)
			}
		})
	}
}

func TestCategories_Returns6Categories(t *testing.T) {
	cats := Categories()
	if len(cats) != 6 {
		t.Fatalf("expected 6 categories, got %d: %v", len(cats), cats)
	}
}

func TestGetWithModality_WebHydration(t *testing.T) {
	g := GetWithModality(ProbeSynthesis, SourceWeb)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.SourceModality != string(SourceWeb) {
		t.Errorf("expected SourceModality %q, got %q", SourceWeb, g.SourceModality)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(g.Nodes))
	}
	node := g.Nodes[0]
	if node.ProbeConfig == nil || node.ProbeConfig.SourceHint != "web" {
		t.Errorf("expected SourceHint 'web', got %v", node.ProbeConfig)
	}
	if len(node.AllowedTools) != 2 || node.AllowedTools[0] != "web_search" {
		t.Errorf("expected web tools, got %v", node.AllowedTools)
	}
}

func TestGetWithModality_HybridHydration(t *testing.T) {
	g := GetWithModality(ProbeAndWrite, SourceHybrid)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.SourceModality != string(SourceHybrid) {
		t.Errorf("expected SourceModality %q, got %q", SourceHybrid, g.SourceModality)
	}
	probe := g.Nodes[0]
	if probe.ProbeConfig == nil || probe.ProbeConfig.SourceHint != "hybrid" {
		t.Errorf("expected SourceHint 'hybrid', got %v", probe.ProbeConfig)
	}
	if len(probe.AllowedTools) != 5 {
		t.Errorf("expected 5 hybrid tools, got %d (%v)", len(probe.AllowedTools), probe.AllowedTools)
	}
}

func TestNodeTypeReferenceCard_ContainsAllNodeTypes(t *testing.T) {
	required := []string{
		"probe", "analyze", "action", "conditional", "loop",
		"probeConfig", "dynamicBindings", "activationThreshold",
		"tzro_code",
	}
	for _, s := range required {
		if !strings.Contains(NodeTypeReferenceCard, s) {
			t.Errorf("NodeTypeReferenceCard missing %q", s)
		}
	}
}

func TestNodeTypeReferenceCard_UnderLineLimit(t *testing.T) {
	lines := strings.Count(NodeTypeReferenceCard, "\n") + 1
	if lines > 60 {
		t.Errorf("NodeTypeReferenceCard has %d lines, expected ≤60", lines)
	}
}
