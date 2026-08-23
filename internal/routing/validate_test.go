package routing

import (
	"context"
	"fmt"
	"testing"

	"tzro/internal/compiler"
)

func TestValidateGraph_NilGraph(t *testing.T) {
	err := ValidateGraph(nil, func(string) bool { return true })
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestValidateGraph_EmptyNodes(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test",
		Nodes:  []compiler.GraphNode{},
	}
	err := ValidateGraph(graph, func(string) bool { return true })
	if err == nil {
		t.Error("expected error for empty nodes")
	}
}

func TestValidateGraph_CyclicGraph(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_cycle",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a"},
			{ID: "B", Type: "action", Action: "tool_b"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "A"},
		},
	}
	err := ValidateGraph(graph, func(string) bool { return true })
	if err == nil {
		t.Error("expected error for cyclic graph")
	}
}

func TestValidateGraph_UnknownTool(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_unknown",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "nonexistent_tool"},
		},
	}
	toolExists := func(name string) bool {
		return name == "known_tool"
	}
	err := ValidateGraph(graph, toolExists)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestValidateGraph_ValidGraph(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_valid",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "tool_a"},
			{ID: "B", Type: "action", Action: "tool_b"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
	}
	toolExists := func(name string) bool {
		return name == "tool_a" || name == "tool_b"
	}
	err := ValidateGraph(graph, toolExists)
	if err != nil {
		t.Errorf("expected no error for valid graph, got: %v", err)
	}
}

func TestValidateGraph_ProbeNodeSkipped(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_probe",
		Nodes: []compiler.GraphNode{
			{ID: "probe_1", Type: "probe", Action: ""},
		},
	}
	// toolExists would reject empty action, but probe nodes should be exempt
	toolExists := func(name string) bool {
		return false
	}
	err := ValidateGraph(graph, toolExists)
	if err != nil {
		t.Errorf("expected no error for probe node, got: %v", err)
	}
}

func TestValidateGraph_SynthesisNodeSkipped(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_synthesis",
		Nodes: []compiler.GraphNode{
			{ID: "synth_1", Type: "synthesis", Action: ""},
		},
	}
	err := ValidateGraph(graph, func(string) bool { return false })
	if err != nil {
		t.Errorf("expected no error for synthesis node, got: %v", err)
	}
}

func TestPlanWithEscalation_LocalSucceeds(t *testing.T) {
	validGraph := &compiler.ExecutionGraph{
		TaskID: "test_local_ok",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "known_tool"},
		},
	}

	localPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return validGraph, nil
	}
	cloudCalled := false
	cloudPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		cloudCalled = true
		return nil, fmt.Errorf("should not be called")
	}
	decision := RoutingDecision{Backend: "local", AllowCloudFallback: true}
	toolExists := func(name string) bool { return name == "known_tool" }

	result, err := PlanWithEscalation(context.Background(), localPlan, cloudPlan, decision, toolExists)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.TaskID != "test_local_ok" {
		t.Errorf("expected local graph returned, got taskID: %s", result.TaskID)
	}
	if cloudCalled {
		t.Error("expected cloud plan NOT to be called when local succeeds")
	}
}

func TestPlanWithEscalation_LocalFailsCloudAllowed(t *testing.T) {
	cloudGraph := &compiler.ExecutionGraph{
		TaskID: "test_cloud_fallback",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "known_tool"},
		},
	}

	localPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return nil, fmt.Errorf("local model crashed")
	}
	cloudPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return cloudGraph, nil
	}
	decision := RoutingDecision{Backend: "local", AllowCloudFallback: true}
	toolExists := func(name string) bool { return true }

	result, err := PlanWithEscalation(context.Background(), localPlan, cloudPlan, decision, toolExists)
	if err != nil {
		t.Fatalf("expected cloud fallback to succeed, got: %v", err)
	}
	if result.TaskID != "test_cloud_fallback" {
		t.Errorf("expected cloud graph returned, got taskID: %s", result.TaskID)
	}
}

func TestPlanWithEscalation_LocalFailsCloudBlocked(t *testing.T) {
	localPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return nil, fmt.Errorf("local model crashed")
	}
	cloudPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return nil, fmt.Errorf("should not be called")
	}
	decision := RoutingDecision{Backend: "local", AllowCloudFallback: false, PrivacyQuarantined: true}
	toolExists := func(name string) bool { return true }

	_, err := PlanWithEscalation(context.Background(), localPlan, cloudPlan, decision, toolExists)
	if err == nil {
		t.Error("expected error when local fails and cloud is blocked")
	}
}

func TestPlanWithEscalation_ValidationFailsRepairSucceeds(t *testing.T) {
	// Local returns a graph with an unknown tool → repair replaces with probe
	invalidGraph := &compiler.ExecutionGraph{
		TaskID: "test_invalid",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "nonexistent_tool", Instructions: "do something"},
		},
	}
	cloudGraph := &compiler.ExecutionGraph{
		TaskID: "test_cloud_rescue",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "known_tool"},
		},
	}

	localPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return invalidGraph, nil
	}
	cloudPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return cloudGraph, nil
	}
	decision := RoutingDecision{Backend: "local", AllowCloudFallback: true}
	toolExists := func(name string) bool { return name == "known_tool" }

	result, err := PlanWithEscalation(context.Background(), localPlan, cloudPlan, decision, toolExists)
	if err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	// Repair replaces the invalid action node with a probe node,
	// so the returned graph is the repaired local graph
	if result.TaskID != "test_invalid" {
		t.Errorf("expected repaired local graph, got taskID: %s", result.TaskID)
	}
	// Verify the invalid action node was replaced with a probe
	foundProbe := false
	for _, node := range result.Nodes {
		if node.Type == "probe" {
			foundProbe = true
			break
		}
	}
	if !foundProbe {
		t.Errorf("expected repair to insert a probe node, but none found")
	}
}

func TestPlanWithEscalation_ValidationFailsCloudAllowed(t *testing.T) {
	// Test that repair produces a valid graph even with cyclic edges
	// between invalid-tool nodes (the cycle is removed when the nodes are replaced).
	invalidGraph := &compiler.ExecutionGraph{
		TaskID: "test_invalid",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "bad_tool_1", Instructions: "step 1"},
			{ID: "B", Type: "action", Action: "bad_tool_2", Instructions: "step 2"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "A"},
		},
	}

	localPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return invalidGraph, nil
	}
	cloudPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return nil, fmt.Errorf("cloud should not be called")
	}
	decision := RoutingDecision{Backend: "local", AllowCloudFallback: true}
	toolExists := func(name string) bool { return name == "known_tool" }

	// Repair replaces both invalid nodes with a single probe, removing the cycle
	result, err := PlanWithEscalation(context.Background(), localPlan, cloudPlan, decision, toolExists)
	if err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if result.TaskID != "test_invalid" {
		t.Errorf("expected repaired local graph, got taskID: %s", result.TaskID)
	}
	// Verify the probe node exists and both action nodes are gone
	for _, node := range result.Nodes {
		if node.Type == "action" {
			t.Errorf("expected no action nodes after repair, found: %s", node.ID)
		}
	}
}

func TestRepairGraphWithProbe_WebModality(t *testing.T) {
	webGraph := &compiler.ExecutionGraph{
		TaskID:         "test_web_repair",
		SourceModality: "web",
		GoalPrompt:     "Search the web for LLM trends",
		Nodes: []compiler.GraphNode{
			{ID: "node_1", Type: "action", Action: "hallucinated_scraper", Instructions: "scrape results"},
		},
	}

	invalidTools := []InvalidTool{{NodeID: "node_1", ToolName: "hallucinated_scraper"}}
	repaired := repairGraphWithProbe(webGraph, invalidTools)

	if len(repaired.Nodes) != 1 {
		t.Fatalf("expected 1 repair node, got %d", len(repaired.Nodes))
	}
	repairNode := repaired.Nodes[0]
	if repairNode.Type != "probe" {
		t.Errorf("expected probe node, got %s", repairNode.Type)
	}
	if repairNode.ProbeConfig == nil || repairNode.ProbeConfig.SourceHint != "web" {
		t.Errorf("expected SourceHint 'web', got %v", repairNode.ProbeConfig)
	}
	hasWebSearch := false
	for _, tool := range repairNode.AllowedTools {
		if tool == "web_search" {
			hasWebSearch = true
		}
	}
	if !hasWebSearch {
		t.Errorf("expected AllowedTools to contain web_search, got %v", repairNode.AllowedTools)
	}
}

func TestPlanWithEscalation_BaselineFallback_LocalOnly(t *testing.T) {
	// Plan with structural cycle between valid tools that cannot be fixed by tool replacement
	cyclicGraph := &compiler.ExecutionGraph{
		TaskID:         "test_cyclic_fallback",
		SourceModality: "web",
		GoalPrompt:     "Research local LLMs on the web",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "known_tool"},
			{ID: "B", Type: "action", Action: "known_tool"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "A"},
		},
	}

	localPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return cyclicGraph, nil
	}
	cloudPlan := func(ctx context.Context) (*compiler.ExecutionGraph, error) {
		return nil, fmt.Errorf("cloud should not be called in local-only mode")
	}
	decision := RoutingDecision{Backend: "local", AllowCloudFallback: false, PrivacyQuarantined: true}
	toolExists := func(name string) bool { return name == "known_tool" }

	result, err := PlanWithEscalation(context.Background(), localPlan, cloudPlan, decision, toolExists)
	if err != nil {
		t.Fatalf("expected baseline fallback to succeed without error, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil fallback graph")
	}
	if result.TaskID != "test_cyclic_fallback" {
		t.Errorf("expected taskID preserved, got %s", result.TaskID)
	}
	if result.SourceModality != "web" {
		t.Errorf("expected SourceModality 'web', got %s", result.SourceModality)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected non-empty fallback nodes")
	}
	// Verify the fallback graph compiles without cycles
	if _, err := compiler.CompileAndSort(result); err != nil {
		t.Errorf("fallback graph failed cycle check: %v", err)
	}
}
