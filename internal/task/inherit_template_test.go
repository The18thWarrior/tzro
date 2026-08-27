package task

import (
	"testing"

	"tzro/internal/compiler"
)

func TestInheritTemplateProperties(t *testing.T) {
	t.Run("InheritRecallPolicyByID", func(t *testing.T) {
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore", Type: "list", RecallPolicy: ""},
				{ID: "write_output", Type: "action"},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore", Type: "list", RecallPolicy: "skip"},
				{ID: "write_output", Type: "action"},
			},
		}

		inheritTemplateProperties(graph, tmpl)

		if graph.Nodes[0].RecallPolicy != "skip" {
			t.Errorf("expected RecallPolicy 'skip', got %q", graph.Nodes[0].RecallPolicy)
		}
	})

	t.Run("InheritRecallPolicyByTypeFallback", func(t *testing.T) {
		// Planner renamed "explore" to "explore_cache"
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore_cache", Type: "list", RecallPolicy: ""},
				{ID: "write_output", Type: "action"},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore", Type: "list", RecallPolicy: "skip"},
				{ID: "write_output", Type: "action"},
			},
		}

		inheritTemplateProperties(graph, tmpl)

		if graph.Nodes[0].RecallPolicy != "skip" {
			t.Errorf("expected RecallPolicy 'skip' via type fallback, got %q", graph.Nodes[0].RecallPolicy)
		}
	})

	t.Run("PreserveExplicitPlannerRecallPolicy", func(t *testing.T) {
		// Planner explicitly set RecallPolicy to "always"
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore", Type: "list", RecallPolicy: "always"},
				{ID: "write_output", Type: "action"},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore", Type: "list", RecallPolicy: "skip"},
				{ID: "write_output", Type: "action"},
			},
		}

		inheritTemplateProperties(graph, tmpl)

		if graph.Nodes[0].RecallPolicy != "always" {
			t.Errorf("expected explicit RecallPolicy 'always' preserved, got %q", graph.Nodes[0].RecallPolicy)
		}
	})

	t.Run("NilInputsNoop", func(t *testing.T) {
		inheritTemplateProperties(nil, nil)
		inheritTemplateProperties(&compiler.ExecutionGraph{}, nil)
		inheritTemplateProperties(nil, &compiler.ExecutionGraph{})
	})
}
