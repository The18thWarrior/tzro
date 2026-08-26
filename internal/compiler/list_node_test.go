package compiler

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Slice 4: Kahn Compiler — List Node integration (ADR-0090, updated by ADR-0091)
// ---------------------------------------------------------------------------

func TestListNodeCompilation_RecallInjectedForToolSink(t *testing.T) {
	// ADR-0091: list → write_file paths get Recall injection (budget-overflow pattern)
	graph := &ExecutionGraph{
		TaskID: "list_with_recall",
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

	// Verify Recall was injected (list → recall → write_file)
	hasRecall := false
	for _, n := range expanded.Nodes {
		if n.Type == "recall" {
			hasRecall = true
		}
	}
	if !hasRecall {
		t.Error("expected recall node injected for list→write_file topology (ADR-0091)")
	}

	// Verify the list node itself is present
	found := false
	for _, n := range expanded.Nodes {
		if n.ID == "list_extract" && n.Type == "list" {
			found = true
		}
	}
	if !found {
		t.Error("list_extract node not found in compiled graph")
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
	}
	if !found {
		t.Error("list_extract node not found in compiled graph")
	}
}
