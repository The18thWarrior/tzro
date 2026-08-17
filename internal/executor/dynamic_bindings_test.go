package executor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// TestResolveDynamicBindings validates the core resolution function that maps
// DynamicBindings declarations to actual upstream node output values.
func TestResolveDynamicBindings(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_dynamic_bindings.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_dynamic_bindings.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	_ = memory.DB.Init()

	taskID := "task-dynamic-bindings-test"

	// Setup: node_2_exec has a tool response with employee_email and employee_id
	execToolResponse := `{"status": "cleared", "employee_id": "EMP-5581", "employee_email": "maozedong@enterprise.corp", "contract_id": "CONTRACT-89"}`
	_ = memory.DB.SetNodeState(taskID, "node_2_exec", "completed", "[Local Tactician] "+execToolResponse)
	_ = memory.DB.SetNodeRawOutput(taskID, "node_2_exec", execToolResponse)

	// Setup: node_4_exec has a contract response
	contractResponse := `{"contract_id": "CONTRACT-89", "contract_hash": "abc123"}`
	_ = memory.DB.SetNodeState(taskID, "node_4_exec", "completed", "[Local Tactician] "+contractResponse)
	_ = memory.DB.SetNodeRawOutput(taskID, "node_4_exec", contractResponse)

	t.Run("ResolvesAllBindings", func(t *testing.T) {
		bindings := map[string]interface{}{
			"employee_email": "node_2.output.employee_email",
			"contract_id":    "node_4.output.contract_id",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if resolved["employee_email"].Value != "maozedong@enterprise.corp" {
			t.Errorf("Expected employee_email='maozedong@enterprise.corp', got %q", resolved["employee_email"].Value)
		}
		if resolved["contract_id"].Value != "CONTRACT-89" {
			t.Errorf("Expected contract_id='CONTRACT-89', got %q", resolved["contract_id"].Value)
		}
	})

	t.Run("PartialResolution_MissingField", func(t *testing.T) {
		bindings := map[string]interface{}{
			"employee_email": "node_2.output.employee_email",
			"nonexistent":    "node_2.output.field_that_doesnt_exist",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if resolved["employee_email"].Value != "maozedong@enterprise.corp" {
			t.Errorf("Expected employee_email to resolve, got %q", resolved["employee_email"].Value)
		}
		if _, exists := resolved["nonexistent"]; exists {
			t.Errorf("Expected nonexistent to NOT be in resolved map, but it was: %q", resolved["nonexistent"].Value)
		}
	})

	t.Run("MissingUpstreamNode", func(t *testing.T) {
		bindings := map[string]interface{}{
			"some_field": "node_999.output.some_field",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if len(resolved) != 0 {
			t.Errorf("Expected empty resolved map for missing node, got %v", resolved)
		}
	})

	t.Run("InvalidBindingFormat", func(t *testing.T) {
		bindings := map[string]interface{}{
			"bad": "just_a_string",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if len(resolved) != 0 {
			t.Errorf("Expected empty resolved map for invalid format, got %v", resolved)
		}
	})

	t.Run("EmptyBindings_IsNoop", func(t *testing.T) {
		resolved := resolveDynamicBindings(context.Background(), nil, taskID, nil)
		if resolved != nil {
			t.Errorf("Expected nil for empty bindings, got %v", resolved)
		}

		resolved = resolveDynamicBindings(context.Background(), map[string]interface{}{}, taskID, nil)
		if resolved != nil {
			t.Errorf("Expected nil for empty map, got %v", resolved)
		}
	})

	t.Run("NumericValuesAreSkipped", func(t *testing.T) {
		bindings := map[string]interface{}{
			"quantity":       125,
			"employee_email": "node_2.output.employee_email",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if _, exists := resolved["quantity"]; exists {
			t.Errorf("Expected numeric 'quantity' to be skipped, but it resolved: %q", resolved["quantity"].Value)
		}
		if resolved["employee_email"].Value != "maozedong@enterprise.corp" {
			t.Errorf("Expected employee_email to still resolve, got %q", resolved["employee_email"].Value)
		}
	})

	t.Run("WholeOutputBinding_ResolvesFullRawOutput", func(t *testing.T) {
		// 2-segment path "nodeId.output" should resolve to the entire RawOutput
		bindings := map[string]interface{}{
			"dataset": "node_2.output",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if _, exists := resolved["dataset"]; !exists {
			t.Fatal("Expected 'dataset' to be resolved via whole_output binding, but it was missing")
		}
		if resolved["dataset"].Tier != "whole_output" {
			t.Errorf("Expected tier 'whole_output', got %q", resolved["dataset"].Tier)
		}
		if resolved["dataset"].Value != execToolResponse {
			t.Errorf("Expected full raw output, got %q", resolved["dataset"].Value)
		}
	})

	t.Run("WholeOutputBinding_IsSpliceEligible", func(t *testing.T) {
		// whole_output tier should be in the high-confidence partition (splice-eligible)
		bindings := map[string]interface{}{
			"dataset": "node_2.output",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)
		highConf, lowConf := partitionBindings(resolved)

		if _, inHigh := highConf["dataset"]; !inHigh {
			t.Error("Expected whole_output binding in highConf (splice-eligible)")
		}
		if _, inLow := lowConf["dataset"]; inLow {
			t.Error("whole_output binding should NOT be in lowConf")
		}
	})

	t.Run("WholeOutputBinding_MissingNode", func(t *testing.T) {
		bindings := map[string]interface{}{
			"dataset": "nonexistent_node.output",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if _, exists := resolved["dataset"]; exists {
			t.Errorf("Expected missing node to not resolve, but got %q", resolved["dataset"].Value)
		}
	})

	t.Run("ThreeSegmentBinding_StillWorks", func(t *testing.T) {
		// Verify the 3-segment path still resolves normally (regression check)
		bindings := map[string]interface{}{
			"email": "node_2.output.employee_email",
		}

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

		if resolved["email"].Value != "maozedong@enterprise.corp" {
			t.Errorf("Expected 3-segment binding to still work, got %q", resolved["email"].Value)
		}
		if resolved["email"].Tier != "recursive_key" {
			t.Errorf("Expected tier 'recursive_key' for 3-segment binding, got %q", resolved["email"].Tier)
		}
	})
}

// TestDynamicBindingsPostExtractionOverride validates that the post-extraction
// hard-override correctly replaces "null" values extracted by the LLM with
// resolved upstream values.
func TestDynamicBindingsPostExtractionOverride(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_override.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_override.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	_ = memory.DB.Init()

	taskID := "task-override-test"

	// Setup upstream node
	execResponse := `{"employee_email": "test@corp.com", "employee_id": "EMP-001"}`
	_ = memory.DB.SetNodeState(taskID, "node_2_exec", "completed", "[Local Tactician] "+execResponse)
	_ = memory.DB.SetNodeRawOutput(taskID, "node_2_exec", execResponse)

	t.Run("OverridesNullValues", func(t *testing.T) {
		// Simulate LLM-extracted result with "null" for employee_email
		llmExtracted := `{"employee_email": "null", "contract_id": "CONTRACT-89"}`

		bindings := map[string]interface{}{
			"employee_email": "node_2.output.employee_email",
		}

		var parsedResult map[string]interface{}
		_ = json.Unmarshal([]byte(llmExtracted), &parsedResult)

		// Apply override logic (mirrors executor.go post-extraction code)
		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)
		for paramName, rb := range resolved {
			existingStr := ""
			if v, exists := parsedResult[paramName]; exists {
				existingStr = v.(string)
			}
			if existingStr == "null" || existingStr == "" {
				parsedResult[paramName] = rb.Value
			}
		}

		if parsedResult["employee_email"] != "test@corp.com" {
			t.Errorf("Expected employee_email to be overridden to 'test@corp.com', got %v", parsedResult["employee_email"])
		}
		if parsedResult["contract_id"] != "CONTRACT-89" {
			t.Errorf("Expected contract_id to remain unchanged, got %v", parsedResult["contract_id"])
		}
	})

	t.Run("PreservesCorrectValues", func(t *testing.T) {
		// LLM extracted correct values — override should NOT replace
		llmExtracted := `{"employee_email": "correct@corp.com"}`

		bindings := map[string]interface{}{
			"employee_email": "node_2.output.employee_email",
		}

		var parsedResult map[string]interface{}
		_ = json.Unmarshal([]byte(llmExtracted), &parsedResult)

		resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)
		for paramName, rb := range resolved {
			existingStr := ""
			if v, exists := parsedResult[paramName]; exists {
				existingStr = v.(string)
			}
			if existingStr == "null" || existingStr == "" {
				parsedResult[paramName] = rb.Value
			}
		}

		// Should NOT override — existing value is already correct (not null)
		if parsedResult["employee_email"] != "correct@corp.com" {
			t.Errorf("Expected employee_email to remain 'correct@corp.com', got %v", parsedResult["employee_email"])
		}
	})
}

// TestSCTCompilerPropagatesDynamicBindings validates that the SCT compiler
// copies DynamicBindings from the abstract action node to both the
// semantic_validator and deterministic expansion nodes.
func TestSCTCompilerPropagatesDynamicBindings(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test-sct-bindings",
		Nodes: []compiler.GraphNode{
			{
				ID:           "node_1",
				Type:         "action",
				Action:       "mock_tool",
				Instructions: "Do step 1",
				AllowedTools: []string{"mock_tool"},
				Status:       "pending",
			},
			{
				ID:           "node_2",
				Type:         "action",
				Action:       "another_tool",
				Instructions: "Do step 2",
				AllowedTools: []string{"another_tool"},
				DynamicBindings: map[string]interface{}{
					"email": "node_1.output.email",
				},
				Status: "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "node_1", TargetID: "node_2"},
		},
	}

	expanded, err := compiler.ExpandToSCTGraph(graph, nil)
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Find node_2_validator and node_2_exec
	var validatorNode, execNode *compiler.GraphNode
	for i := range expanded.Nodes {
		if expanded.Nodes[i].ID == "node_2_validator" {
			validatorNode = &expanded.Nodes[i]
		}
		if expanded.Nodes[i].ID == "node_2_exec" {
			execNode = &expanded.Nodes[i]
		}
	}

	if validatorNode == nil {
		t.Fatal("node_2_validator not found in expanded graph")
	}
	if execNode == nil {
		t.Fatal("node_2_exec not found in expanded graph")
	}

	// Verify bindings propagated
	if validatorNode.DynamicBindings["email"] != "node_1.output.email" {
		t.Errorf("Expected validator to have binding 'email' -> 'node_1.output.email', got %v", validatorNode.DynamicBindings)
	}
	if execNode.DynamicBindings["email"] != "node_1.output.email" {
		t.Errorf("Expected exec to have binding 'email' -> 'node_1.output.email', got %v", execNode.DynamicBindings)
	}

	// Verify node_1 nodes do NOT have bindings
	for _, n := range expanded.Nodes {
		if strings.HasPrefix(n.ID, "node_1") && len(n.DynamicBindings) > 0 {
			t.Errorf("node_1 expansion should not have bindings, got %v on %s", n.DynamicBindings, n.ID)
		}
	}
}

func TestResolveDynamicBindings_WholeOutputRecall(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_recall_binding.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_recall_binding.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	_ = memory.DB.Init()

	taskID := "task-recall-binding-test"

	// Simulate probe recall node completion with tag prefix
	recallContent := "# Cache Analysis\nExported functions:\n- NewCacheID\n- PruneColumns"
	_ = memory.DB.SetNodeState(taskID, "analyze_cache_recall", "completed", "[Recall] "+recallContent)
	_ = memory.DB.SetNodeRawOutput(taskID, "analyze_cache_recall", "[Recall] "+recallContent)

	// Resolve "analyze_cache.output" (using base probe ID)
	bindings := map[string]interface{}{
		"content": "analyze_cache.output",
	}

	resolved := resolveDynamicBindings(context.Background(), bindings, taskID, nil)

	if rb, ok := resolved["content"]; !ok {
		t.Fatal("expected 'content' binding to be resolved")
	} else {
		if rb.Tier != "whole_output" {
			t.Errorf("expected tier 'whole_output', got %q", rb.Tier)
		}
		if rb.Value != recallContent {
			t.Errorf("expected value %q (stripped of prefix), got %q", recallContent, rb.Value)
		}
	}

	// Verify partitioning puts whole_output into highConf
	highConf, lowConf := partitionBindings(resolved)
	if highConf["content"] != recallContent {
		t.Errorf("expected highConf[\"content\"] = %q, got %q", recallContent, highConf["content"])
	}
	if len(lowConf) != 0 {
		t.Errorf("expected lowConf to be empty, got %v", lowConf)
	}
}
