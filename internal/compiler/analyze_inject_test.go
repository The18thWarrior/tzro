package compiler

import (
	"strings"
	"testing"
)

func TestSCT_InjectsAnalyzeNodeForTabularReadFile(t *testing.T) {
	// A read_file action node referencing a tabular file with NO downstream
	// analyze/probe node should get an auto-injected analyze node.
	graph := &ExecutionGraph{
		TaskID: "test_analyze_inject",
		Nodes: []GraphNode{
			{
				ID:           "step_1",
				Type:         "action",
				Action:       "read_file",
				Instructions: "Read the sales_report.csv file",
				AllowedTools: []string{"read_file"},
				Status:       "pending",
			},
		},
		Edges: []GraphEdge{},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Should find an auto-injected analyze node
	var foundAnalyze bool
	for _, node := range expanded.Nodes {
		if node.Type == "analyze" && strings.Contains(node.ID, "step_1") {
			foundAnalyze = true
			// Instructions should be prefixed with cache data instruction
			if !strings.Contains(node.Instructions, "Using the cached tabular data") {
				t.Errorf("analyze node instructions should contain cache prefix, got: %q", node.Instructions)
			}
			// Should contain the original instructions
			if !strings.Contains(node.Instructions, "sales_report.csv") {
				t.Errorf("analyze node should preserve original instructions mentioning the file")
			}
			// Should have cache tools
			if !hasCacheToolsInAllowed(node.AllowedTools) {
				t.Error("analyze node should have cache tools in AllowedTools")
			}
			break
		}
	}
	if !foundAnalyze {
		t.Error("expected an auto-injected analyze node for tabular read_file with no downstream analyze")
	}
}

func TestSCT_SkipsAnalyzeInjectionWhenDownstreamAnalyzeExists(t *testing.T) {
	// When a downstream analyze node already exists, don't inject another one.
	graph := &ExecutionGraph{
		TaskID: "test_no_double_analyze",
		Nodes: []GraphNode{
			{
				ID:           "step_1",
				Type:         "action",
				Action:       "read_file",
				Instructions: "Read data.xlsx spreadsheet",
				AllowedTools: []string{"read_file"},
				Status:       "pending",
			},
			{
				ID:           "step_2",
				Type:         "analyze",
				Instructions: "Analyze the spreadsheet data",
				Status:       "pending",
			},
		},
		Edges: []GraphEdge{
			{SourceID: "step_1", TargetID: "step_2"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Count analyze nodes — should only be the planned one
	analyzeCount := 0
	for _, node := range expanded.Nodes {
		if node.Type == "analyze" {
			analyzeCount++
		}
	}
	if analyzeCount != 1 {
		t.Errorf("expected 1 analyze node (the planned one), got %d", analyzeCount)
	}
}

// TestKahnCompiler_AnalyzeNode_SetsRequiredToolDispatch validates ADR-0068:
// the SCT compiler auto-populates RequiredToolDispatch with ["sql_cached_data"]
// and ensures SourceHint="cache" for analyze nodes.
func TestKahnCompiler_AnalyzeNode_SetsRequiredToolDispatch(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "test_required_dispatch",
		Nodes: []GraphNode{
			{
				ID:           "analyze_leads",
				Type:         "analyze",
				Instructions: "Count leads by country from the cached data",
				Status:       "pending",
			},
		},
		Edges: []GraphEdge{},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Find the analyze node
	var analyzeNode *GraphNode
	for i := range expanded.Nodes {
		if expanded.Nodes[i].Type == "analyze" {
			analyzeNode = &expanded.Nodes[i]
			break
		}
	}
	if analyzeNode == nil {
		t.Fatal("expected an analyze node in the expanded graph")
	}

	// ProbeConfig must exist
	if analyzeNode.ProbeConfig == nil {
		t.Fatal("analyze node should have a ProbeConfig")
	}

	// ADR-0068: RequiredToolDispatch must be auto-populated
	if len(analyzeNode.ProbeConfig.RequiredToolDispatch) == 0 {
		t.Fatal("analyze node ProbeConfig.RequiredToolDispatch should be auto-populated, got empty")
	}
	if analyzeNode.ProbeConfig.RequiredToolDispatch[0] != "sql_cached_data" {
		t.Errorf("expected RequiredToolDispatch=[\"sql_cached_data\"], got %v", analyzeNode.ProbeConfig.RequiredToolDispatch)
	}

	// SourceHint must be "cache"
	if analyzeNode.ProbeConfig.SourceHint != "cache" {
		t.Errorf("expected SourceHint=\"cache\", got %q", analyzeNode.ProbeConfig.SourceHint)
	}
}
