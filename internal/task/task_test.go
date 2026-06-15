package task

import (
	"context"
	"os"
	"strings"
	"testing"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
)

func TestPlan_HeuristicFallback(t *testing.T) {
	// 1. Test heartbeat planning
	graphHeartbeat := buildHeuristicGraph("t_heartbeat", "run system check", "heartbeat")
	if len(graphHeartbeat.Nodes) != 2 {
		t.Errorf("expected 2 nodes for heartbeat, got %d", len(graphHeartbeat.Nodes))
	}
	if graphHeartbeat.Nodes[0].ID != "cron_trigger" || graphHeartbeat.Nodes[1].ID != "metrics_slack" {
		t.Errorf("incorrect heartbeat node sequence: %v", graphHeartbeat.Nodes)
	}

	// 2. Test Salesforce/Sheet keywords
	graphSF := buildHeuristicGraph("t_salesforce", "Fetch leads from Google Sheets and dedup contacts", "")
	if len(graphSF.Nodes) != 3 {
		t.Errorf("expected 3 nodes for Salesforce leads flow, got %d", len(graphSF.Nodes))
	}
	if graphSF.Nodes[0].ID != "fetch_sheet_records" || graphSF.Nodes[1].ID != "dedup_contacts" || graphSF.Nodes[2].ID != "slack_confirm" {
		t.Errorf("incorrect salesforce node sequence: %v", graphSF.Nodes)
	}

	// 3. Test Slack keywords
	graphSlack := buildHeuristicGraph("t_slack", "Post message to slack: Job complete", "")
	if len(graphSlack.Nodes) != 1 {
		t.Errorf("expected 1 node for Slack post, got %d", len(graphSlack.Nodes))
	}
	if graphSlack.Nodes[0].ID != "slack_confirm" {
		t.Errorf("expected node slack_confirm, got %s", graphSlack.Nodes[0].ID)
	}

	// 4. Test Generic Fallback
	graphGeneric := buildHeuristicGraph("t_generic", "Do some work", "")
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

func TestPlan_NoHeuristicFallback(t *testing.T) {
	ctx := context.Background()
	oldConfig := config.Get()
	defer config.Override(&oldConfig)

	// Force empty API Key to trigger failure
	cfg := oldConfig
	cfg.CloudAPIKey = ""
	config.Override(&cfg)

	_, err := Plan(ctx, "Do some work", ExecuteOptions{TaskID: "t_test"})
	if err == nil {
		t.Error("expected Plan to fail when Cloud API key is missing, but it succeeded")
	} else if !strings.Contains(err.Error(), "no local backend available") {
		t.Errorf("expected error containing 'no local backend available', got: %v", err)
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

type mockBackend struct {
	CalledCallModel bool
	ResponseContent string
	ResponseErr     error
}

func (m *mockBackend) CallModel(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (*inference.InferenceResult, error) {
	m.CalledCallModel = true
	if m.ResponseErr != nil {
		return nil, m.ResponseErr
	}
	return &inference.InferenceResult{
		Content: m.ResponseContent,
	}, nil
}

func (m *mockBackend) CallModelStream(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, meta inference.StreamMeta) (*inference.InferenceResult, error) {
	return nil, nil
}

func (m *mockBackend) Status() string {
	return "active"
}

func (m *mockBackend) Start(ctx context.Context) error {
	return nil
}

func (m *mockBackend) Stop() error {
	return nil
}

func TestPlan_BackendFallback(t *testing.T) {
	ctx := context.Background()
	oldConfig := config.Get()
	defer config.Override(&oldConfig)

	// Force empty API Key to trigger fallback
	cfg := oldConfig
	cfg.CloudAPIKey = ""
	config.Override(&cfg)

	// Set mock active backend
	mock := &mockBackend{
		ResponseContent: `{"taskId": "t_mock", "maxCycles": 5, "nodes": [{"id": "node_1", "type": "action", "action": "slack_message", "instructions": "alert", "allowedTools": ["slack_message"], "status": "pending"}], "edges": []}`,
	}
	oldBackend := inference.ActiveBackend
	inference.ActiveBackend = mock
	defer func() { inference.ActiveBackend = oldBackend }()

	graph, err := Plan(ctx, "Send alert", ExecuteOptions{TaskID: "t_mock"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if !mock.CalledCallModel {
		t.Error("expected CallModel to be called on active backend, but it was not")
	}

	if len(graph.Nodes) == 0 {
		t.Error("expected graph to contain expanded nodes, but got 0")
	}
}
