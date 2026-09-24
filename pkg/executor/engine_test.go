package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestEngine_TracerBullet_TwoNodes(t *testing.T) {
	// Two-node graph: bash echo → decision that depends on node1's stdout
	graphJSON := `{
		"version": "3.0",
		"task_id": "tracer-bullet",
		"nodes": [
			{
				"id": "echo_hello",
				"type": "tool",
				"tool": "bash",
				"args": {"command": "echo hello"}
			},
			{
				"id": "check_output",
				"type": "decision",
				"depends_on": ["echo_hello"],
				"input": {
					"stdout": {"$ref": "/nodes/echo_hello/output/stdout"}
				},
				"question": {
					"type": "noul",
					"prompt": "Does the output contain a greeting?"
				}
			}
		],
		"returns": ["/nodes/echo_hello/output/stdout"]
	}`

	var g Graph
	if err := json.Unmarshal([]byte(graphJSON), &g); err != nil {
		t.Fatalf("failed to parse graph: %v", err)
	}

	// Create engine with a mock decider
	engine := NewEngine(
		WithMaxConcurrency(1),
		WithDecider(&mockDecider{
			answer:     "yes",
			confidence: 0.95,
		}),
	)

	result, err := engine.Execute(context.Background(), &g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify task ID propagation
	if result.TaskID != "tracer-bullet" {
		t.Errorf("expected task_id 'tracer-bullet', got %q", result.TaskID)
	}

	// Verify status is completed
	if result.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", result.Status)
	}

	// Verify echo node executed successfully
	echoOutput, ok := result.Outputs["echo_hello"]
	if !ok {
		t.Fatal("expected output for node 'echo_hello'")
	}
	if echoOutput.Status != "completed" {
		t.Errorf("expected echo node status 'completed', got %q", echoOutput.Status)
	}
	if echoOutput.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", echoOutput.ExitCode)
	}
	if !strings.Contains(echoOutput.Stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", echoOutput.Stdout)
	}

	// Verify decision node completed
	decisionOutput, ok := result.Outputs["check_output"]
	if !ok {
		t.Fatal("expected output for node 'check_output'")
	}
	if decisionOutput.Status != "completed" {
		t.Errorf("expected decision node status 'completed', got %q", decisionOutput.Status)
	}

	// Verify returns were collected
	if result.Returns == nil {
		t.Fatal("expected returns to be populated")
	}
}

func TestEngine_CycleDetection(t *testing.T) {
	g := &Graph{
		Version: "3.0",
		TaskID:  "cycle-test",
		Nodes: []Node{
			{ID: "a", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo a"}, DependsOn: []string{"b"}},
			{ID: "b", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo b"}, DependsOn: []string{"a"}},
		},
	}

	engine := NewEngine(WithMaxConcurrency(1))
	_, err := engine.Execute(context.Background(), g)
	if err == nil {
		t.Fatal("expected error for cyclic graph")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got: %v", err)
	}
}

func TestEngine_ParallelIndependentNodes(t *testing.T) {
	// Three independent nodes should all complete
	g := &Graph{
		Version: "3.0",
		TaskID:  "parallel-test",
		Nodes: []Node{
			{ID: "a", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo a"}},
			{ID: "b", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo b"}},
			{ID: "c", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo c"}},
		},
	}

	engine := NewEngine(WithMaxConcurrency(3))
	result, err := engine.Execute(context.Background(), g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result.Outputs) != 3 {
		t.Errorf("expected 3 outputs, got %d", len(result.Outputs))
	}

	for _, id := range []string{"a", "b", "c"} {
		out, ok := result.Outputs[id]
		if !ok {
			t.Errorf("missing output for node %q", id)
			continue
		}
		if out.Status != "completed" {
			t.Errorf("node %q status = %q, want 'completed'", id, out.Status)
		}
	}
}

func TestEngine_PointerInterpolation(t *testing.T) {
	// Producer echoes a value; consumer uses $ref to read it and echo it back.
	// We use a decision node as the consumer to verify pointer resolution into input.
	g := &Graph{
		Version: "3.0",
		TaskID:  "pointer-test",
		Nodes: []Node{
			{
				ID:   "producer",
				Type: NodeTypeTool,
				Tool: "bash",
				Args: map[string]interface{}{"command": "echo resolved_value"},
			},
			{
				ID:        "consumer",
				Type:      NodeTypeDecision,
				DependsOn: []string{"producer"},
				Input: map[string]interface{}{
					"stdout": map[string]interface{}{
						"$ref": "/nodes/producer/output/stdout",
					},
				},
				Question: &Question{
					Type:   "noul",
					Prompt: "Is this valid?",
				},
			},
		},
	}

	// Use a mock decider that echoes back the state it received
	decider := &inspectingDecider{}
	engine := NewEngine(WithMaxConcurrency(1), WithDecider(decider))
	result, err := engine.Execute(context.Background(), g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	consumer := result.Outputs["consumer"]
	if consumer.Status != "completed" {
		t.Errorf("consumer status = %q, want 'completed'", consumer.Status)
	}

	// Verify the decider received the resolved pointer value
	if decider.lastState == nil {
		t.Fatal("decider was not called")
	}
	stdout, ok := decider.lastState["stdout"]
	if !ok {
		t.Fatal("decider state missing 'stdout' key")
	}
	if stdout != "resolved_value" {
		t.Errorf("pointer resolved to %q, want 'resolved_value'", stdout)
	}
}

func TestEngine_DependencyOrdering(t *testing.T) {
	// Linear chain: a → b → c
	// Execution order must respect dependencies
	g := &Graph{
		Version: "3.0",
		TaskID:  "ordering-test",
		Nodes: []Node{
			{ID: "step1", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo step1"}},
			{ID: "step2", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo step2"}, DependsOn: []string{"step1"}},
			{ID: "step3", Type: NodeTypeTool, Tool: "bash", Args: map[string]interface{}{"command": "echo step3"}, DependsOn: []string{"step2"}},
		},
	}

	engine := NewEngine(WithMaxConcurrency(1))
	result, err := engine.Execute(context.Background(), g)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	for _, id := range []string{"step1", "step2", "step3"} {
		out, ok := result.Outputs[id]
		if !ok {
			t.Errorf("missing output for node %q", id)
			continue
		}
		if out.Status != "completed" {
			t.Errorf("node %q: status = %q, want 'completed'", id, out.Status)
		}
	}
}

// mockDecider implements the Decider interface for testing
type mockDecider struct {
	answer     string
	confidence float64
}

func (m *mockDecider) Decide(ctx context.Context, req *DecisionInput) (*DecisionOutput, error) {
	return &DecisionOutput{
		Answer:     m.answer,
		Confidence: m.confidence,
	}, nil
}

// inspectingDecider records the state it receives for assertion in tests
type inspectingDecider struct {
	lastState map[string]interface{}
}

func (d *inspectingDecider) Decide(ctx context.Context, req *DecisionInput) (*DecisionOutput, error) {
	d.lastState = req.State
	return &DecisionOutput{
		Answer:     "yes",
		Confidence: 0.99,
	}, nil
}
