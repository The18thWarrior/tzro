package task

import (
	"context"
	"os"
	"testing"
	"tzro/internal/compiler"
	"tzro/internal/config"
)

func TestPlan_HeuristicFallback(t *testing.T) {
	ctx := context.Background()

	// 1. Test heartbeat planning
	optsHeartbeat := ExecuteOptions{
		TaskID:     "t_heartbeat",
		IntentType: "heartbeat",
	}
	graphHeartbeat, err := Plan(ctx, "run system check", optsHeartbeat)
	if err != nil {
		t.Fatalf("unexpected error planning heartbeat: %v", err)
	}
	if len(graphHeartbeat.Nodes) != 2 {
		t.Errorf("expected 2 nodes for heartbeat, got %d", len(graphHeartbeat.Nodes))
	}
	if graphHeartbeat.Nodes[0].ID != "cron_trigger" || graphHeartbeat.Nodes[1].ID != "metrics_slack" {
		t.Errorf("incorrect heartbeat node sequence: %v", graphHeartbeat.Nodes)
	}

	// 2. Test Salesforce/Sheet keywords
	optsSF := ExecuteOptions{
		TaskID: "t_salesforce",
	}
	graphSF, err := Plan(ctx, "Fetch leads from Google Sheets and dedup contacts", optsSF)
	if err != nil {
		t.Fatalf("unexpected error planning Salesforce task: %v", err)
	}
	if len(graphSF.Nodes) != 3 {
		t.Errorf("expected 3 nodes for Salesforce leads flow, got %d", len(graphSF.Nodes))
	}
	if graphSF.Nodes[0].ID != "fetch_sheet_records" || graphSF.Nodes[1].ID != "dedup_contacts" || graphSF.Nodes[2].ID != "slack_confirm" {
		t.Errorf("incorrect salesforce node sequence: %v", graphSF.Nodes)
	}

	// 3. Test Slack keywords
	optsSlack := ExecuteOptions{
		TaskID: "t_slack",
	}
	graphSlack, err := Plan(ctx, "Post message to slack: Job complete", optsSlack)
	if err != nil {
		t.Fatalf("unexpected error planning Slack task: %v", err)
	}
	if len(graphSlack.Nodes) != 1 {
		t.Errorf("expected 1 node for Slack post, got %d", len(graphSlack.Nodes))
	}
	if graphSlack.Nodes[0].ID != "slack_confirm" {
		t.Errorf("expected node slack_confirm, got %s", graphSlack.Nodes[0].ID)
	}

	// 4. Test Generic Fallback
	optsGeneric := ExecuteOptions{
		TaskID: "t_generic",
	}
	graphGeneric, err := Plan(ctx, "Do some work", optsGeneric)
	if err != nil {
		t.Fatalf("unexpected error planning generic task: %v", err)
	}
	if len(graphGeneric.Nodes) != 2 {
		t.Errorf("expected 2 nodes for generic fallback, got %d", len(graphGeneric.Nodes))
	}
	if graphGeneric.Nodes[0].ID != "analyze_inputs" || graphGeneric.Nodes[1].ID != "execute_utility" {
		t.Errorf("incorrect generic node sequence: %v", graphGeneric.Nodes)
	}
}

func TestPlan_DelegatedSecrets(t *testing.T) {
	// Import config package inside tests
	// Save the old configuration and restore afterwards
	oldConfig := config.Get()
	defer func() {
		_ = config.Save(&oldConfig)
	}()

	// Case 1: Configured key has a dynamic env reference that is NOT set
	cfg := oldConfig
	cfg.CloudAPIKey = "$PLANNER_TEST_ENV_KEY"
	_ = config.Save(&cfg)

	os.Unsetenv("PLANNER_TEST_ENV_KEY")

	// GetCloudAPIKey should be empty
	if key := config.GetCloudAPIKey(); key != "" {
		t.Errorf("expected GetCloudAPIKey to be empty when env var is unset, got: %s", key)
	}

	// Case 2: Set the environment variable
	os.Setenv("PLANNER_TEST_ENV_KEY", "dummy-secret-12345")
	defer os.Unsetenv("PLANNER_TEST_ENV_KEY")

	// GetCloudAPIKey should resolve the secret
	if key := config.GetCloudAPIKey(); key != "dummy-secret-12345" {
		t.Errorf("expected GetCloudAPIKey to resolve to dummy-secret-12345, got: %s", key)
	}
}

func TestKahnTopologicalSorting(t *testing.T) {
	// Build a valid DAG graph
	graph := &compiler.ExecutionGraph{
		TaskID: "t_kahn_test",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action"},
			{ID: "B", Type: "action"},
			{ID: "C", Type: "action"},
			{ID: "D", Type: "action"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "A", TargetID: "C"},
			{SourceID: "B", TargetID: "D"},
			{SourceID: "C", TargetID: "D"},
		},
	}

	levels, err := compiler.CompileAndSort(graph)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}

	// Levels should be:
	// Level 0: [A]
	// Level 1: [B, C] (or [C, B])
	// Level 2: [D]
	if len(levels) != 3 {
		t.Errorf("expected 3 levels, got %d", len(levels))
	}

	if len(levels[0]) != 1 || levels[0][0] != "A" {
		t.Errorf("level 0 should contain only A, got %v", levels[0])
	}

	if len(levels[1]) != 2 {
		t.Errorf("level 1 should contain B and C, got %v", levels[1])
	}
	hasB := levels[1][0] == "B" || levels[1][1] == "B"
	hasC := levels[1][0] == "C" || levels[1][1] == "C"
	if !hasB || !hasC {
		t.Errorf("level 1 should contain B and C, got %v", levels[1])
	}

	if len(levels[2]) != 1 || levels[2][0] != "D" {
		t.Errorf("level 2 should contain only D, got %v", levels[2])
	}
}
