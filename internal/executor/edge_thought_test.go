package executor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// TestEvaluateActivationThresholdTriggersSpawn verifies that when edge thought
// confidence is below the target node's activation threshold, spawn is triggered.
func TestEvaluateActivationThresholdTriggersSpawn(t *testing.T) {
	et := &memory.EdgeThought{
		GoalConfidence: 0.3,
		GoalAchieved:   false,
	}
	targetNode := &compiler.GraphNode{
		ActivationThreshold: 0.7,
	}

	action := evaluateActivationThreshold(et, targetNode)
	if action != ActivationSpawn {
		t.Errorf("expected ActivationSpawn when confidence (0.3) < threshold (0.7), got %v", action)
	}
}

// TestEvaluateActivationThresholdContinues verifies that when confidence meets
// the threshold, execution continues normally (no spawn).
func TestEvaluateActivationThresholdContinues(t *testing.T) {
	et := &memory.EdgeThought{
		GoalConfidence: 0.8,
		GoalAchieved:   false,
	}
	targetNode := &compiler.GraphNode{
		ActivationThreshold: 0.7,
	}

	action := evaluateActivationThreshold(et, targetNode)
	if action != ActivationContinue {
		t.Errorf("expected ActivationContinue when confidence (0.8) >= threshold (0.7), got %v", action)
	}
}

// TestEvaluateActivationThresholdHalt verifies the halt flag stops execution.
func TestEvaluateActivationThresholdHalt(t *testing.T) {
	et := &memory.EdgeThought{
		GoalConfidence: 0.95,
		GoalAchieved:   true, // halt flag
	}
	targetNode := &compiler.GraphNode{
		ActivationThreshold: 0.5,
	}

	action := evaluateActivationThreshold(et, targetNode)
	if action != ActivationHalt {
		t.Errorf("expected ActivationHalt when goalAchieved is true, got %v", action)
	}
}

// TestEvaluateActivationThresholdZeroDisabled verifies that threshold 0.0
// means no activation threshold evaluation (edge thought not generated).
func TestEvaluateActivationThresholdZeroDisabled(t *testing.T) {
	et := &memory.EdgeThought{
		GoalConfidence: 0.1,
	}
	targetNode := &compiler.GraphNode{
		ActivationThreshold: 0.0, // disabled
	}

	action := evaluateActivationThreshold(et, targetNode)
	if action != ActivationContinue {
		t.Errorf("expected ActivationContinue when threshold is 0.0 (disabled), got %v", action)
	}
}

// TestShouldGenerateEdgeThought verifies that edge thoughts are only generated
// when the target node has a non-zero activation threshold.
func TestShouldGenerateEdgeThought(t *testing.T) {
	if shouldGenerateEdgeThought(&compiler.GraphNode{ActivationThreshold: 0.0}) {
		t.Error("should not generate edge thought when threshold is 0.0")
	}
	if !shouldGenerateEdgeThought(&compiler.GraphNode{ActivationThreshold: 0.5}) {
		t.Error("should generate edge thought when threshold is 0.5")
	}
	if !shouldGenerateEdgeThought(&compiler.GraphNode{ActivationThreshold: 1.0}) {
		t.Error("should generate edge thought when threshold is 1.0")
	}
	// Analyze and probe nodes should never generate edge thoughts (Deterministic Shield).
	if shouldGenerateEdgeThought(&compiler.GraphNode{Type: "analyze", ActivationThreshold: 0.7}) {
		t.Error("should not generate edge thought for analyze nodes")
	}
	if shouldGenerateEdgeThought(&compiler.GraphNode{Type: "list", ActivationThreshold: 0.7}) {
		t.Error("should not generate edge thought for probe nodes")
	}
}

// TestEvaluateActivationThresholdAnalyzeProtected verifies that analyze nodes
// are protected by the Deterministic Shield — goalAchieved=true should NOT halt them.
func TestEvaluateActivationThresholdAnalyzeProtected(t *testing.T) {
	et := &memory.EdgeThought{
		GoalConfidence: 0.95,
		GoalAchieved:   true,
	}
	targetNode := &compiler.GraphNode{
		Type:                "analyze",
		ActivationThreshold: 0.7,
	}

	action := evaluateActivationThreshold(et, targetNode)
	if action != ActivationContinue {
		t.Errorf("expected ActivationContinue for analyze node with goalAchieved=true, got %v", action)
	}
}

// TestEvaluateActivationThresholdListProtected verifies that list nodes
// are protected by the Deterministic Shield — goalAchieved=true should NOT halt them.
func TestEvaluateActivationThresholdListProtected(t *testing.T) {
	et := &memory.EdgeThought{
		GoalConfidence: 0.95,
		GoalAchieved:   true,
	}
	targetNode := &compiler.GraphNode{
		Type:                "list",
		ActivationThreshold: 0.7,
	}

	action := evaluateActivationThreshold(et, targetNode)
	if action != ActivationContinue {
		t.Errorf("expected ActivationContinue for list node with goalAchieved=true, got %v", action)
	}
}

// MockEdgeThoughtInference implements EdgeThoughtInference for testing.
type MockEdgeThoughtInference struct {
	confidence float64
	halt       bool
	thought    string
	callCount  int
}

func (m *MockEdgeThoughtInference) GenerateEdgeThought(
	ctx context.Context,
	taskID string,
	sourceNode *compiler.GraphNode,
	targetNode *compiler.GraphNode,
	sourceOutput string,
	stepIndex int,
) (*memory.EdgeThought, error) {
	m.callCount++
	return &memory.EdgeThought{
		ID:             fmt.Sprintf("et_%d", m.callCount),
		TaskID:         taskID,
		SourceNode:     sourceNode.ID,
		TargetNode:     targetNode.ID,
		Thought:        m.thought,
		GoalConfidence: m.confidence,
		GoalAchieved:   m.halt,
		StepIndex:      stepIndex,
		CreatedAt:      time.Now().Unix(),
	}, nil
}

// TestEdgeThoughtIntegrationWithReadyQueue verifies that the edge thought
// evaluation path works end-to-end: spawn when confidence is low.
func TestEdgeThoughtIntegrationWithReadyQueue(t *testing.T) {
	// This test verifies the data flow, not the full execution (no DB needed)
	mockInference := &MockEdgeThoughtInference{
		confidence: 0.3,
		halt:       false,
		thought:    "Need more exploration",
	}

	source := &compiler.GraphNode{ID: "A", Type: "action", Action: "tool_a", Status: "completed"}
	target := &compiler.GraphNode{ID: "B", Type: "action", Action: "tool_b", ActivationThreshold: 0.7}

	et, err := mockInference.GenerateEdgeThought(
		context.Background(), "task-1", source, target, `{"result": "partial"}`, 1,
	)
	if err != nil {
		t.Fatalf("GenerateEdgeThought failed: %v", err)
	}

	action := evaluateActivationThreshold(et, target)
	if action != ActivationSpawn {
		t.Errorf("expected ActivationSpawn, got %v", action)
	}
	if et.GoalConfidence != 0.3 {
		t.Errorf("expected confidence 0.3, got %f", et.GoalConfidence)
	}
}
