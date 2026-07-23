package task

import (
	"testing"
)

// Slice 5 test for ADR-0054: SelfContained short-circuit in Plan()

func TestPlan_SelfContained_BypassesPlanner(t *testing.T) {
	prompt := "Analyze these benchmark results: T1=5.00, T2=3.50, T3=4.25. Calculate the average and classify quality."

	graph := buildSelfContainedGraph("test-task-1", prompt)

	// Graph should be non-nil
	if graph == nil {
		t.Fatal("buildSelfContainedGraph returned nil")
	}

	// Should have exactly 1 node
	if len(graph.Nodes) != 1 {
		t.Fatalf("Expected 1 node, got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]

	// Node type should be "probe"
	if node.Type != "probe" {
		t.Errorf("Node type = %q, want %q", node.Type, "probe")
	}

	// Node should have DirectSynthesis enabled
	if node.ProbeConfig == nil {
		t.Fatal("ProbeConfig is nil")
	}
	if !node.ProbeConfig.DirectSynthesis {
		t.Error("ProbeConfig.DirectSynthesis should be true")
	}

	// AllowedTools should contain save_memory
	found := false
	for _, tool := range node.ProbeConfig.AllowedTools {
		if tool == "save_memory" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AllowedTools should contain save_memory, got %v", node.ProbeConfig.AllowedTools)
	}

	// Goal should be the prompt
	if node.ProbeConfig.Goal != prompt {
		t.Errorf("Goal = %q, want prompt", node.ProbeConfig.Goal)
	}

	// No edges
	if len(graph.Edges) != 0 {
		t.Errorf("Expected 0 edges, got %d", len(graph.Edges))
	}

	// TaskID should be set
	if graph.TaskID != "test-task-1" {
		t.Errorf("TaskID = %q, want %q", graph.TaskID, "test-task-1")
	}
}
