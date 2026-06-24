package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
	"tzro/internal/memory"
)

func setupToolsTestDB(t *testing.T) func() {
	t.Helper()
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbName := "test_tools_dashboard.db"
	memory.DB.SetDBPathForTesting(dbName)

	_ = os.Remove(dbName)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}

	return func() {
		_ = memory.DB.Close()
		_ = os.Remove(dbName)
		memory.DB.SetDBPathForTesting(oldDBPath)
	}
}

func TestDashboardTools(t *testing.T) {
	cleanup := setupToolsTestDB(t)
	defer cleanup()

	// Initialize tools registry
	if err := Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx := context.Background()

	// 1. Test gather_metrics tool
	metricsTool := &GatherMetricsTool{}
	if metricsTool.Name() != "gather_metrics" {
		t.Errorf("Expected name 'gather_metrics', got '%s'", metricsTool.Name())
	}
	metricsSchema, _ := metricsTool.GetSchema()
	if metricsSchema == "" {
		t.Error("Expected non-empty schema")
	}

	// Save some fake node states to memory DB to simulate historical tasks
	db := memory.DB.RawDB()
	now := time.Now().Unix()
	_, _ = db.Exec("INSERT INTO node_states (task_id, node_id, status, completed_at) VALUES (?, ?, ?, ?)", "task_today", "node_1", "completed", now-3600)
	_, _ = db.Exec("INSERT INTO node_states (task_id, node_id, status, completed_at) VALUES (?, ?, ?, ?)", "task_yesterday", "node_1", "completed", now-3600*30)

	metricsRes, err := metricsTool.Call(ctx, nil)
	if err != nil {
		t.Fatalf("gather_metrics Call failed: %v", err)
	}
	var metricItems []MetricItem
	if err := json.Unmarshal([]byte(metricsRes), &metricItems); err != nil {
		t.Fatalf("Failed to unmarshal metrics output: %v", err)
	}
	if len(metricItems) == 0 {
		t.Error("Expected non-empty metric items")
	}

	// 2. Test gather_tasks tool
	tasksTool := &GatherTasksTool{}
	tasksRes, err := tasksTool.Call(ctx, nil)
	if err != nil {
		t.Fatalf("gather_tasks Call failed: %v", err)
	}
	var tasksOutput map[string]interface{}
	if err := json.Unmarshal([]byte(tasksRes), &tasksOutput); err != nil {
		t.Fatalf("Failed to unmarshal tasks output: %v", err)
	}
	if _, ok := tasksOutput["recentTasks"]; !ok {
		t.Error("Expected 'recentTasks' key in gather_tasks output")
	}

	// 3. Test gather_config tool
	configTool := &GatherConfigTool{}
	configRes, err := configTool.Call(ctx, nil)
	if err != nil {
		t.Fatalf("gather_config Call failed: %v", err)
	}
	var configOutput map[string]interface{}
	if err := json.Unmarshal([]byte(configRes), &configOutput); err != nil {
		t.Fatalf("Failed to unmarshal config output: %v", err)
	}
	if _, ok := configOutput["config"]; !ok {
		t.Error("Expected 'config' key in gather_config output")
	}

	// 4. Test gather_workflows tool
	workflowsTool := &GatherWorkflowsTool{}
	workflowsRes, err := workflowsTool.Call(ctx, nil)
	if err != nil {
		t.Fatalf("gather_workflows Call failed: %v", err)
	}
	var workflowsOutput map[string]interface{}
	if err := json.Unmarshal([]byte(workflowsRes), &workflowsOutput); err != nil {
		t.Fatalf("Failed to unmarshal workflows output: %v", err)
	}
	if _, ok := workflowsOutput["workflows"]; !ok {
		t.Error("Expected 'workflows' key in gather_workflows output")
	}

	// 5. Test compose_layout tool (flat element schema → deterministic layout assembly)
	composeTool := &ComposeLayoutTool{}
	composeArgs := map[string]interface{}{
		"elements": []interface{}{
			map[string]interface{}{
				"type": "MetricCard",
				"props": map[string]interface{}{
					"label": "Total Tasks",
					"value": "100",
					"trend": "up",
				},
			},
			map[string]interface{}{
				"type": "MetricCard",
				"props": map[string]interface{}{
					"label": "Success Rate",
					"value": "85%",
					"trend": "stable",
				},
			},
			map[string]interface{}{
				"type":  "TaskTable",
				"props": map[string]interface{}{},
			},
			map[string]interface{}{
				"type":  "ConfigPanel",
				"props": map[string]interface{}{},
			},
			map[string]interface{}{
				"type":  "SidecarStatus",
				"props": map[string]interface{}{},
			},
		},
		"theme": "Subtle Glass",
	}
	composeRes, err := composeTool.Call(ctx, composeArgs)
	if err != nil {
		t.Fatalf("compose_layout Call failed: %v", err)
	}
	var composeOutput map[string]interface{}
	if err := json.Unmarshal([]byte(composeRes), &composeOutput); err != nil {
		t.Fatalf("Failed to unmarshal compose_layout output: %v", err)
	}
	// Verify deterministic assembly produced expected structure
	if composeOutput["version"].(float64) != 1.0 {
		t.Errorf("Expected version 1.0, got %v", composeOutput["version"])
	}
	layout, ok := composeOutput["layout"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'layout' key in compose output")
	}
	if layout["type"].(string) != "Stack" {
		t.Errorf("Expected layout type 'Stack', got '%v'", layout["type"])
	}
	layoutChildren, ok := layout["children"].([]interface{})
	if !ok || len(layoutChildren) == 0 {
		t.Fatal("Expected non-empty children in layout Stack")
	}
	// First child should be the MetricCard grid section
	firstSection, ok := layoutChildren[0].(map[string]interface{})
	if !ok || firstSection["type"].(string) != "Section" {
		t.Errorf("Expected first child to be Section, got %v", firstSection["type"])
	}
	// Count total primitives
	primCount := countPrimitives(layout)
	if primCount < 5 {
		t.Errorf("Expected at least 5 primitives, got %d", primCount)
	}

	// 6. Test terminal_synthesis tool — should succeed with valid spec
	terminalTool := &TerminalSynthesisTool{}
	terminalArgs := map[string]interface{}{
		"spec": composeOutput,
	}
	terminalRes, err := terminalTool.Call(ctx, terminalArgs)
	if err != nil {
		t.Fatalf("terminal_synthesis Call failed: %v", err)
	}
	var terminalOutput map[string]interface{}
	if err := json.Unmarshal([]byte(terminalRes), &terminalOutput); err != nil {
		t.Fatalf("Failed to unmarshal terminal_synthesis output: %v", err)
	}
	if terminalOutput["status"].(string) != "completed" {
		t.Errorf("Expected status 'completed', got '%v'", terminalOutput["status"])
	}

	// 7. Test terminal_synthesis minimum-element validation gate
	emptySpec := map[string]interface{}{
		"spec": map[string]interface{}{
			"version":     1.0,
			"generatedAt": float64(time.Now().Unix()),
			"ttlSeconds":  14400.0,
			"layout": map[string]interface{}{
				"type": "Stack",
				// No children — this should be rejected
			},
		},
	}
	_, err = terminalTool.Call(ctx, emptySpec)
	if err == nil {
		t.Error("Expected terminal_synthesis to reject empty spec, but it succeeded")
	}

	// Verify that the valid spec is indeed saved in database
	savedSpec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		t.Fatalf("Failed to query latest spec from database: %v", err)
	}
	if savedSpec == nil {
		t.Fatal("Expected saved spec in database, got nil")
	}
}
