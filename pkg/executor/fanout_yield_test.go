package executor

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

func TestGroupNode_FanOut(t *testing.T) {
	// 5 items should expand into 5 concurrent evaluations bounded by max_concurrency 2
	var evalCount atomic.Int32

	g := &Graph{
		Version: "3.0",
		TaskID:  "fanout-test",
		Nodes: []Node{
			{
				ID:   "source",
				Type: NodeTypeTool,
				Tool: "bash",
				Args: map[string]interface{}{"command": "echo 'files'"},
			},
			{
				ID:        "evaluate_all",
				Type:      NodeTypeGroup,
				DependsOn: []string{"source"},
				Strategy:  "per_item",
				Items: []interface{}{
					"file1.go", "file2.go", "file3.go", "file4.go", "file5.go",
				},
				MaxConcurrency: 2,
				Template: &Node{
					Type: NodeTypeDecision,
					Question: &Question{
						Type:   "noul",
						Prompt: "Is this file relevant?",
					},
				},
			},
		},
	}

	decider := &countingDecider{count: &evalCount}
	engine := NewEngine(
		WithMaxConcurrency(4),
		WithDecider(decider),
	)

	result, err := engine.Execute(context.Background(), g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Source should be completed
	source := result.Outputs["source"]
	if source.Status != "completed" {
		t.Errorf("source node status = %q, want 'completed'", source.Status)
	}

	// Group node should be completed
	group := result.Outputs["evaluate_all"]
	if group.Status != "completed" {
		t.Errorf("group node status = %q, want 'completed'", group.Status)
	}

	// The decider should have been called 5 times (once per item)
	count := evalCount.Load()
	if count != 5 {
		t.Errorf("expected 5 decider evaluations, got %d", count)
	}

	// Check that sub-results are available in the group output's data
	if group.Data == nil {
		t.Fatal("expected group node to have data with sub-results")
	}

	subResults, ok := group.Data["results"]
	if !ok {
		t.Fatal("expected 'results' key in group data")
	}

	resultList, ok := subResults.([]interface{})
	if !ok {
		t.Fatalf("expected results to be a slice, got %T", subResults)
	}

	if len(resultList) != 5 {
		t.Errorf("expected 5 sub-results, got %d", len(resultList))
	}
}

func TestYield_CriteriaUnmet(t *testing.T) {
	// Decision node with min_confidence 0.90, but decider returns 0.65
	g := &Graph{
		Version: "3.0",
		TaskID:  "yield-test",
		Nodes: []Node{
			{
				ID:   "run_test",
				Type: NodeTypeTool,
				Tool: "bash",
				Args: map[string]interface{}{"command": "echo test_output"},
			},
			{
				ID:        "evaluate",
				Type:      NodeTypeDecision,
				DependsOn: []string{"run_test"},
				Input: map[string]interface{}{
					"stdout": map[string]interface{}{
						"$ref": "/nodes/run_test/output/stdout",
					},
				},
				Question: &Question{
					Type:   "noul",
					Prompt: "Did the test pass?",
				},
				Accept: &AcceptCriteria{
					MinConfidence: 0.90,
				},
			},
			{
				ID:        "next_step",
				Type:      NodeTypeTool,
				Tool:      "bash",
				DependsOn: []string{"evaluate"},
				Args:      map[string]interface{}{"command": "echo next"},
			},
		},
	}

	engine := NewEngine(
		WithMaxConcurrency(1),
		WithDecider(&mockDecider{
			answer:     "no",
			confidence: 0.65,
		}),
	)

	result, err := engine.Execute(context.Background(), g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Overall status should be yielded
	if result.Status != "yielded" {
		t.Errorf("expected overall status 'yielded', got %q", result.Status)
	}

	// run_test should be completed
	if result.Outputs["run_test"].Status != "completed" {
		t.Errorf("run_test should be completed, got %q", result.Outputs["run_test"].Status)
	}

	// evaluate should be yielded (criteria unmet)
	evalOutput := result.Outputs["evaluate"]
	if evalOutput.Status != "yielded" {
		t.Errorf("evaluate should be yielded, got %q", evalOutput.Status)
	}

	// next_step should be blocked (depends on yielded node)
	nextOutput := result.Outputs["next_step"]
	if nextOutput.Status != "blocked" {
		t.Errorf("next_step should be blocked, got %q", nextOutput.Status)
	}

	// Verify a YieldEnvelope can be constructed
	envelope := NewYieldEnvelope(
		result.TaskID,
		YieldReasonCriteriaUnmet,
		"evaluate",
		"Decision confidence 0.65 below threshold 0.90",
		result.Outputs,
	)

	if envelope.Status != "yielded" {
		t.Errorf("envelope status = %q, want 'yielded'", envelope.Status)
	}
	if envelope.Reason != YieldReasonCriteriaUnmet {
		t.Errorf("envelope reason = %q, want %q", envelope.Reason, YieldReasonCriteriaUnmet)
	}
	if envelope.TriggerNode != "evaluate" {
		t.Errorf("envelope trigger = %q, want 'evaluate'", envelope.TriggerNode)
	}

	// Verify completed nodes list contains run_test
	foundRunTest := false
	for _, id := range envelope.CompletedNodes {
		if id == "run_test" {
			foundRunTest = true
			break
		}
	}
	if !foundRunTest {
		t.Error("expected 'run_test' in completed nodes")
	}

	// Verify it serializes to valid JSON
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("failed to marshal envelope: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}

func TestYield_IndependentBranchContinues(t *testing.T) {
	// Graph with two independent branches:
	// Branch A: source → evaluate (yields) → blocked_step
	// Branch B: independent → independent_consumer
	// Branch B should complete even though Branch A yields
	g := &Graph{
		Version: "3.0",
		TaskID:  "independent-branch-test",
		Nodes: []Node{
			{
				ID:   "source",
				Type: NodeTypeTool,
				Tool: "bash",
				Args: map[string]interface{}{"command": "echo source"},
			},
			{
				ID:        "evaluate",
				Type:      NodeTypeDecision,
				DependsOn: []string{"source"},
				Question: &Question{
					Type:   "noul",
					Prompt: "Will this yield?",
				},
				Accept: &AcceptCriteria{
					MinConfidence: 0.99, // Will cause yield with 0.5 confidence
				},
			},
			{
				ID:        "blocked_step",
				Type:      NodeTypeTool,
				Tool:      "bash",
				DependsOn: []string{"evaluate"},
				Args:      map[string]interface{}{"command": "echo blocked"},
			},
			{
				ID:   "independent",
				Type: NodeTypeTool,
				Tool: "bash",
				Args: map[string]interface{}{"command": "echo independent"},
			},
			{
				ID:        "independent_consumer",
				Type:      NodeTypeTool,
				Tool:      "bash",
				DependsOn: []string{"independent"},
				Args:      map[string]interface{}{"command": "echo consumed"},
			},
		},
	}

	engine := NewEngine(
		WithMaxConcurrency(2),
		WithDecider(&mockDecider{
			answer:     "maybe",
			confidence: 0.50,
		}),
	)

	result, err := engine.Execute(context.Background(), g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Branch A: source=completed, evaluate=yielded, blocked_step=blocked
	if result.Outputs["source"].Status != "completed" {
		t.Errorf("source status = %q, want 'completed'", result.Outputs["source"].Status)
	}
	if result.Outputs["evaluate"].Status != "yielded" {
		t.Errorf("evaluate status = %q, want 'yielded'", result.Outputs["evaluate"].Status)
	}
	if result.Outputs["blocked_step"].Status != "blocked" {
		t.Errorf("blocked_step status = %q, want 'blocked'", result.Outputs["blocked_step"].Status)
	}

	// Branch B: both should complete normally
	if result.Outputs["independent"].Status != "completed" {
		t.Errorf("independent status = %q, want 'completed'", result.Outputs["independent"].Status)
	}
	if result.Outputs["independent_consumer"].Status != "completed" {
		t.Errorf("independent_consumer status = %q, want 'completed'", result.Outputs["independent_consumer"].Status)
	}

	// Overall status should be yielded because at least one node yielded
	if result.Status != "yielded" {
		t.Errorf("overall status = %q, want 'yielded'", result.Status)
	}
}

// countingDecider counts how many times it's called
type countingDecider struct {
	count *atomic.Int32
}

func (d *countingDecider) Decide(ctx context.Context, req *DecisionInput) (*DecisionOutput, error) {
	d.count.Add(1)
	return &DecisionOutput{
		Answer:     "yes",
		Confidence: 0.95,
	}, nil
}
