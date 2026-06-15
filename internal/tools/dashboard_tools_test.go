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

	// 5. Test compose_layout tool
	composeTool := &ComposeLayoutTool{}
	args := map[string]interface{}{
		"version":     1.0,
		"generatedAt": float64(now),
		"ttlSeconds":  14400.0,
		"layout": map[string]interface{}{
			"type": "Stack",
			"children": []interface{}{
				map[string]interface{}{
					"type": "MetricCard",
					"props": map[string]interface{}{
						"label": "Total Tasks",
						"value": "100",
					},
				},
			},
		},
	}
	composeRes, err := composeTool.Call(ctx, args)
	if err != nil {
		t.Fatalf("compose_layout Call failed: %v", err)
	}
	var composeOutput map[string]interface{}
	if err := json.Unmarshal([]byte(composeRes), &composeOutput); err != nil {
		t.Fatalf("Failed to unmarshal compose_layout output: %v", err)
	}
	if composeOutput["version"].(float64) != 1.0 {
		t.Errorf("Expected version 1.0, got %v", composeOutput["version"])
	}

	// 6. Test terminal_synthesis tool
	terminalTool := &TerminalSynthesisTool{}
	terminalArgs := map[string]interface{}{
		"spec": args,
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

	// Verify that the spec is indeed saved in database
	savedSpec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		t.Fatalf("Failed to query latest spec from database: %v", err)
	}
	if savedSpec == nil {
		t.Fatal("Expected saved spec in database, got nil")
	}
}
