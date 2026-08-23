package task

import (
	"context"
	"testing"
)

func TestFastPath_T0BuildGraph(t *testing.T) {
	prompt := "Count the number of rows in data.csv"
	taskID := "task_fast_path_test"

	graph, ok := BuildFastPathGraph(taskID, prompt, "T0")
	if !ok || graph == nil {
		t.Fatalf("expected BuildFastPathGraph to succeed for T0 prompt")
	}

	if graph.TaskID != taskID {
		t.Errorf("expected TaskID %q, got %q", taskID, graph.TaskID)
	}
	if graph.GoalPrompt != prompt {
		t.Errorf("expected GoalPrompt %q, got %q", prompt, graph.GoalPrompt)
	}
	if len(graph.Nodes) != 1 {
		t.Errorf("expected fast-path graph to have exactly 1 execution node, got %d", len(graph.Nodes))
	}
}

func TestFastPath_T1SkipsFastPath(t *testing.T) {
	prompt := "Research modern orchestration trends and compile a full architectural report"
	taskID := "task_fast_path_skip_test"

	graph, ok := BuildFastPathGraph(taskID, prompt, "T1")
	if ok || graph != nil {
		t.Errorf("expected BuildFastPathGraph to return false for T1 prompt")
	}
}

func TestFastPath_ExecuteOptionsSelfContained(t *testing.T) {
	prompt := "Self contained summary calculation"
	opts := ExecuteOptions{
		TaskID:        "task_self_contained_test",
		SelfContained: true,
	}

	graph, err := Plan(context.Background(), prompt, opts)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if graph == nil {
		t.Fatalf("expected non-nil graph from Plan")
	}
	if graph.GoalPrompt != prompt {
		t.Errorf("expected GoalPrompt %q, got %q", prompt, graph.GoalPrompt)
	}
}
