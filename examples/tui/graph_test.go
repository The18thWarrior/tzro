package tui

import (
	"strings"
	"testing"
	"tzro/internal/compiler"
)

func TestTUI_GraphDrawingLayouts(t *testing.T) {
	// 1. Build a mock Kahn topologically sorted Execution Graph
	g := &compiler.ExecutionGraph{
		TaskID: "task_test_123",
		Nodes: []compiler.GraphNode{
			{ID: "node1", Action: "fetch_hubspot", Status: "completed"},
			{ID: "node2", Action: "dedup_pg", Status: "running"},
			{ID: "node3", Action: "post_slack", Status: "pending"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "node1", TargetID: "node2"},
			{SourceID: "node2", TargetID: "node3"},
		},
	}

	levels := [][]string{
		{"node1"},
		{"node2"},
		{"node3"},
	}

	// 2. Test Column Layout rendering
	colView := renderColumnLayout(g, levels)
	if !strings.Contains(colView, "fetch_hubspot") {
		t.Error("expected Column layout view to contain fetch_hubspot")
	}
	if !strings.Contains(colView, "dedup_pg") {
		t.Error("expected Column layout view to contain dedup_pg")
	}
	if !strings.Contains(colView, "SUCCESS") {
		t.Error("expected Column layout view to render completed status successfully")
	}

	// 3. Test Row-Hierarchy Tree Layout rendering
	treeView := renderTreeLayout(g, levels)
	if !strings.Contains(treeView, "fetch_hubspot") {
		t.Error("expected Tree layout view to contain fetch_hubspot")
	}
	if !strings.Contains(treeView, "├──") && !strings.Contains(treeView, "└──") && !strings.Contains(treeView, "  ↳") {
		t.Error("expected Tree layout view to contain hierarchical branch symbols")
	}
}
