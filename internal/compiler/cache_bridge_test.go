package compiler

import (
	"testing"
)

// Phase 4: CacheBridgeNode injection tests

func TestSCT_InjectsCacheBridgeForTabularReference(t *testing.T) {
	// Build a graph where a node's instructions reference a CSV file
	graph := &ExecutionGraph{
		TaskID: "test_bridge_inject",
		Nodes: []GraphNode{
			{
				ID:           "step_1",
				Type:         "action",
				Action:       "read_file",
				Instructions: "Read the leads.csv file and analyze the data",
				AllowedTools: []string{"read_file"},
				Status:       "pending",
			},
			{
				ID:           "step_2",
				Type:         "synthesis",
				Instructions: "Summarize the findings from the data analysis",
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

	// Look for a cache_bridge node
	var foundBridge bool
	for _, node := range expanded.Nodes {
		if node.ID == "cache_bridge_step_1" {
			foundBridge = true
			if node.Type != "action" {
				t.Errorf("cache bridge Type = %q, want %q", node.Type, "action")
			}
			if node.Action != "jq_cached_data" {
				t.Errorf("cache bridge Action = %q, want %q", node.Action, "jq_cached_data")
			}
			if node.ActivationThreshold != 0.0 {
				t.Errorf("cache bridge ActivationThreshold = %f, want 0.0", node.ActivationThreshold)
			}
			// Should have cache tools
			hasTools := false
			for _, tool := range node.AllowedTools {
				if tool == "jq_cached_data" {
					hasTools = true
				}
			}
			if !hasTools {
				t.Error("cache bridge should have jq_cached_data in AllowedTools")
			}
		}
	}
	if !foundBridge {
		t.Error("expected a cache_bridge_step_1 node to be injected for CSV reference")
	}

	// Verify edge wiring: step_1_exec → cache_bridge → step_2
	hasEdgeToBridge := false
	hasEdgeFromBridge := false
	for _, edge := range expanded.Edges {
		if edge.TargetID == "cache_bridge_step_1" {
			hasEdgeToBridge = true
		}
		if edge.SourceID == "cache_bridge_step_1" {
			hasEdgeFromBridge = true
		}
	}
	if !hasEdgeToBridge {
		t.Error("expected an edge TO cache_bridge_step_1")
	}
	if !hasEdgeFromBridge {
		t.Error("expected an edge FROM cache_bridge_step_1")
	}
}

func TestSCT_SkipsBridgeWhenDownstreamHasCacheTools(t *testing.T) {
	// Build a graph where the downstream node already has cache tools
	graph := &ExecutionGraph{
		TaskID: "test_bridge_dedup",
		Nodes: []GraphNode{
			{
				ID:           "step_1",
				Type:         "action",
				Action:       "read_file",
				Instructions: "Read the leads.csv file",
				AllowedTools: []string{"read_file"},
				Status:       "pending",
			},
			{
				ID:           "step_2",
				Type:         "action",
				Action:       "jq_cached_data",
				Instructions: "Query the cached data using jq",
				AllowedTools: []string{"jq_cached_data", "introspect_cache"},
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

	// Should NOT have a cache bridge node (downstream already has cache tools)
	for _, node := range expanded.Nodes {
		if node.ID == "cache_bridge_step_1" {
			t.Error("cache bridge should NOT be injected when downstream node already has cache tools")
		}
	}
}

func TestSCT_NoBridgeForNonTabularInstructions(t *testing.T) {
	// Build a graph where no tabular extension is referenced
	graph := &ExecutionGraph{
		TaskID: "test_no_bridge",
		Nodes: []GraphNode{
			{
				ID:           "step_1",
				Type:         "action",
				Action:       "read_file",
				Instructions: "Read the config.json file",
				AllowedTools: []string{"read_file"},
				Status:       "pending",
			},
			{
				ID:           "step_2",
				Type:         "synthesis",
				Instructions: "Summarize the configuration",
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

	for _, node := range expanded.Nodes {
		if node.ID == "cache_bridge_step_1" {
			t.Error("cache bridge should NOT be injected for non-tabular file references")
		}
	}
}

func TestSCT_ProbeAllowedToolsExpansion(t *testing.T) {
	// Probe node referencing tabular data should get cache tools added to allowedTools
	graph := &ExecutionGraph{
		TaskID: "test_probe_expansion",
		Nodes: []GraphNode{
			{
				ID:           "explore_data",
				Type:         "probe",
				Instructions: "Explore the sales_data.csv and analyze patterns",
				ProbeConfig: &ProbeConfig{
					Goal:         "Analyze sales data patterns",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   15,
					CompactEvery: 3,
				},
				Status: "pending",
			},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Find the probe node and check if cache tools were added
	for _, node := range expanded.Nodes {
		if node.ID == "explore_data" && node.Type == "probe" {
			if node.ProbeConfig == nil {
				t.Fatal("probe node should have ProbeConfig")
			}
			hasCacheTool := false
			for _, tool := range node.ProbeConfig.AllowedTools {
				if tool == "jq_cached_data" || tool == "introspect_cache" || tool == "read_cached_data" {
					hasCacheTool = true
					break
				}
			}
			if !hasCacheTool {
				t.Error("probe node referencing .csv should have cache tools added to allowedTools")
			}
		}
	}
}

func TestSCT_ProbeWithReadFileGetsExpansion(t *testing.T) {
	// Spec §5.1: probe with read_file in allowedTools should get cache tools
	// even without tabular file extensions in instructions
	graph := &ExecutionGraph{
		TaskID: "test_probe_readfile_expansion",
		Nodes: []GraphNode{
			{
				ID:           "explore_project",
				Type:         "probe",
				Instructions: "Explore the project and understand its data model",
				ProbeConfig: &ProbeConfig{
					Goal:         "Understand data model",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   15,
					CompactEvery: 3,
				},
				Status: "pending",
			},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	for _, node := range expanded.Nodes {
		if node.ID == "explore_project" && node.Type == "probe" {
			hasCacheTool := false
			for _, tool := range node.ProbeConfig.AllowedTools {
				if tool == "jq_cached_data" {
					hasCacheTool = true
					break
				}
			}
			if !hasCacheTool {
				t.Error("probe with read_file in allowedTools should get cache tools per spec §5.1")
			}
		}
	}
}
