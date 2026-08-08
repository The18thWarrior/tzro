package task

import (
	"testing"

	"tzro/internal/compiler"
)

func TestRepairDynamicBindings(t *testing.T) {
	t.Run("ValidBindingsUnchanged", func(t *testing.T) {
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "explore.output.synthesis",
				}},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "explore.output.synthesis",
				}},
			},
		}

		repairDynamicBindings(graph, tmpl)

		binding := graph.Nodes[1].DynamicBindings["content"].(string)
		if binding != "explore.output.synthesis" {
			t.Errorf("valid binding should not be modified, got %q", binding)
		}
	})

	t.Run("RepairByPrefixMatch", func(t *testing.T) {
		// Model renamed "explore" → "explore_cache_source" but binding still
		// references "explore" — prefix match should repair it.
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore_cache_source"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "explore.output.synthesis",
				}},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "explore.output.synthesis",
				}},
			},
		}

		repairDynamicBindings(graph, tmpl)

		binding := graph.Nodes[1].DynamicBindings["content"].(string)
		if binding != "explore_cache_source.output.synthesis" {
			t.Errorf("expected prefix-matched repair, got %q", binding)
		}
	})

	t.Run("RepairByTemplateFallback", func(t *testing.T) {
		// Model hallucinated a completely different source node ("synthesize_index")
		// that has no prefix match. Should fall back to template binding if
		// the template's source node exists in the mutated graph.
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "synthesize_index.output.text",
				}},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "explore.output.synthesis",
				}},
			},
		}

		repairDynamicBindings(graph, tmpl)

		binding := graph.Nodes[1].DynamicBindings["content"].(string)
		if binding != "explore.output.synthesis" {
			t.Errorf("expected template fallback repair, got %q", binding)
		}
	})

	t.Run("NilGraphIsNoop", func(t *testing.T) {
		repairDynamicBindings(nil, nil) // should not panic
	})

	t.Run("NoBindingsIsNoop", func(t *testing.T) {
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output"},
			},
		}
		repairDynamicBindings(graph, nil) // should not panic
	})

	t.Run("NonStringBindingSkipped", func(t *testing.T) {
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": 42, // non-string
				}},
			},
		}
		repairDynamicBindings(graph, nil)

		// Should be unchanged
		if graph.Nodes[1].DynamicBindings["content"] != 42 {
			t.Error("non-string binding should not be modified")
		}
	})

	t.Run("UnrepairableBindingLogsWarning", func(t *testing.T) {
		// No prefix match and template source doesn't exist in graph either
		graph := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "completely_different_node"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "hallucinated_node.output.text",
				}},
			},
		}
		tmpl := &compiler.ExecutionGraph{
			Nodes: []compiler.GraphNode{
				{ID: "explore"},
				{ID: "write_output", DynamicBindings: map[string]interface{}{
					"content": "explore.output.synthesis",
				}},
			},
		}

		repairDynamicBindings(graph, tmpl)

		// Binding should remain unchanged (unrepairable)
		binding := graph.Nodes[1].DynamicBindings["content"].(string)
		if binding != "hallucinated_node.output.text" {
			t.Errorf("unrepairable binding should not be modified, got %q", binding)
		}
	})
}
