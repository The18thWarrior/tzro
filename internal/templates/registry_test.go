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
	if node.Type != "list" {
		t.Errorf("expected node type 'list', got %q", node.Type)
	}
	if node.ProbeConfig == nil {
		t.Fatal("expected ProbeConfig on list node")
	}

	// List nodes don't need AllowedTools — discovery is deterministic (Orient → Discover)
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

func TestGet_Docgen_HasListAndWriteAction(t *testing.T) {
	graph := Get(Docgen)
	if graph == nil {
		t.Fatal("expected non-nil graph for Docgen")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	// Find list and action nodes
	var listNode, action *compiler.GraphNode
	for i := range graph.Nodes {
		switch graph.Nodes[i].Type {
		case "list":
			listNode = &graph.Nodes[i]
		case "action":
			action = &graph.Nodes[i]
		}
	}
	if listNode == nil {
		t.Fatal("expected a list node")
	}
	if action == nil {
		t.Fatal("expected an action node")
	}
	if action.Action != "write_file" {
		t.Errorf("expected action 'write_file', got %q", action.Action)
	}

	// Verify edge: list → action
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != listNode.ID || graph.Edges[0].TargetID != action.ID {
		t.Errorf("expected edge %s→%s, got %s→%s", listNode.ID, action.ID, graph.Edges[0].SourceID, graph.Edges[0].TargetID)
	}

	// Verify NO dynamic bindings on action node — list output flows via
	// AccumulatedContext (edge-driven), not DynamicBindings.
	if len(action.DynamicBindings) > 0 {
		t.Errorf("expected no DynamicBindings on list→action template, got %v", action.DynamicBindings)
	}
}

func TestGet_Research_HasWebList(t *testing.T) {
	graph := GetWithModality(ListSynthesis, SourceWeb)
	if graph == nil {
		t.Fatal("expected non-nil graph for Research")
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]
	// Research web tasks keep list type but with web source hint
	if node.Type != "list" {
		t.Errorf("expected node type 'list', got %q", node.Type)
	}
	if node.ProbeConfig == nil {
		t.Fatal("expected ProbeConfig")
	}
	if node.ProbeConfig.SourceHint != "web" {
		t.Errorf("expected SourceHint 'web', got %q", node.ProbeConfig.SourceHint)
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

func TestGet_MultiListSynthesis_HasParallelListNodes(t *testing.T) {
	graph := Get(MultiListSynthesis)
	if graph == nil {
		t.Fatal("expected non-nil graph for MultiListSynthesis")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	for _, n := range graph.Nodes {
		if n.Type != "list" {
			t.Errorf("expected all nodes to be list type, got %q", n.Type)
		}
	}

	// Parallel list nodes have no edges between them
	if len(graph.Edges) != 0 {
		t.Errorf("expected 0 edges (parallel list nodes), got %d", len(graph.Edges))
	}
}

func TestGet_Codegen_HasListAndTzroCode(t *testing.T) {
	graph := Get(Codegen)
	if graph == nil {
		t.Fatal("expected non-nil graph for Codegen")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	var listNode, codeAction *compiler.GraphNode
	for i := range graph.Nodes {
		if graph.Nodes[i].Type == "list" {
			listNode = &graph.Nodes[i]
		}
		if graph.Nodes[i].Type == "action" && graph.Nodes[i].Action == "tzro_code" {
			codeAction = &graph.Nodes[i]
		}
	}
	if listNode == nil {
		t.Fatal("expected a list node")
	}
	if codeAction == nil {
		t.Fatal("expected a tzro_code action node")
	}

	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != listNode.ID || graph.Edges[0].TargetID != codeAction.ID {
		t.Errorf("expected edge %s→%s", listNode.ID, codeAction.ID)
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
	g := GetWithModality(ListSynthesis, SourceWeb)
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
}

func TestGetWithModality_HybridHydration(t *testing.T) {
	g := GetWithModality(ListAndWrite, SourceHybrid)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.SourceModality != string(SourceHybrid) {
		t.Errorf("expected SourceModality %q, got %q", SourceHybrid, g.SourceModality)
	}
	listNode := g.Nodes[0]
	if listNode.ProbeConfig == nil || listNode.ProbeConfig.SourceHint != "hybrid" {
		t.Errorf("expected SourceHint 'hybrid', got %v", listNode.ProbeConfig)
	}
}

func TestNodeTypeReferenceCard_ContainsAllNodeTypes(t *testing.T) {
	required := []string{
		"list", "analyze", "action", "conditional", "loop",
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

// --- Legacy alias backward compatibility ---

func TestLegacyAliases_ResolveToNewCategories(t *testing.T) {
	// ProbeSynthesis, ProbeAndWrite, MultiProbeSynthesis should still work
	if Get(ProbeSynthesis) == nil {
		t.Error("ProbeSynthesis alias returned nil")
	}
	if Get(ProbeAndWrite) == nil {
		t.Error("ProbeAndWrite alias returned nil")
	}
	if Get(MultiProbeSynthesis) == nil {
		t.Error("MultiProbeSynthesis alias returned nil")
	}
}
