package compiler

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Slice 4: Kahn Compiler — List Node integration (ADR-0090)
// ---------------------------------------------------------------------------

func TestListNodeCompilation_NoRecallInjection(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "list_no_recall",
		Nodes: []GraphNode{
			{
				ID:           "list_extract",
				Type:         "list",
				Instructions: "List all exported functions",
				Status:       "pending",
			},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Verify NO recall node was injected
	for _, n := range expanded.Nodes {
		if n.Type == "recall" {
			t.Errorf("unexpected recall node %q injected for list node — List Nodes should skip Recall injection (ADR-0090)", n.ID)
		}
	}
}

func TestListNodeCompilation_CountedAsDiscovery(t *testing.T) {
	graph := &ExecutionGraph{
		TaskID: "list_discovery_count",
		Nodes: []GraphNode{
			{
				ID:           "list_extract",
				Type:         "list",
				Instructions: "List all exported functions",
				Status:       "pending",
			},
			{
				ID:           "write_output",
				Type:         "action",
				Action:       "write_file",
				Instructions: "Write the extracted content",
				Status:       "pending",
			},
		},
		Edges: []GraphEdge{
			{SourceID: "list_extract", TargetID: "write_output"},
		},
	}

	expanded, err := ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Verify the list node itself is present in the compiled graph
	found := false
	for _, n := range expanded.Nodes {
		if n.ID == "list_extract" && n.Type == "list" {
			found = true
		}
		// Still no recall for list nodes
		if n.Type == "recall" {
			t.Errorf("unexpected recall node %q for list node in graph with write action", n.ID)
		}
	}
	if !found {
		t.Error("list_extract node not found in compiled graph")
	}
}
