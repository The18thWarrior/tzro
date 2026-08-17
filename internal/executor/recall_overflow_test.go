package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

type mockRecallInferenceEngine struct {
	capturedPrompts []string
}

func (m *mockRecallInferenceEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, _ ModelTarget) (string, error) {
	m.capturedPrompts = append(m.capturedPrompts, systemPrompt)
	return "<SYNTHESIZE_READY>", nil
}

func (m *mockRecallInferenceEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, _ ModelTarget) (string, error) {
	return "<SYNTHESIZE_READY>", nil
}

func TestRunRecall_ContextOverflowSafety(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_recall_overflow.db")
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbPath)
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	ctx := context.Background()
	taskID := "task_overflow_test"
	recallNodeID := "recall_1"
	upstreamNodeID := "probe_1"

	// Mock DB node state with massive raw output (90,000+ characters)
	massiveOutput := "# Full Codebase Dump\n\n" + strings.Repeat("func handler(w http.ResponseWriter, r *http.Request) { fmt.Println(\"large code output\"); }\n", 1000)
	if err := memory.DB.SetNodeState(taskID, upstreamNodeID, "completed", "output"); err != nil {
		t.Fatalf("SetNodeState failed: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, upstreamNodeID, massiveOutput); err != nil {
		t.Fatalf("SetNodeRawOutput failed: %v", err)
	}

	mockEngine := &mockRecallInferenceEngine{}
	ee := &ExecutionEngine{}

	_, err := ee.RunRecall(ctx, taskID, recallNodeID, []string{upstreamNodeID}, "Explain handler routing", mockEngine)
	if err != nil {
		t.Fatalf("RunRecall failed: %v", err)
	}

	if len(mockEngine.capturedPrompts) == 0 {
		t.Fatalf("expected at least 1 prompt captured")
	}

	// Refinement step prompt (Step 1)
	refinementPrompt := mockEngine.capturedPrompts[0]
	t.Logf("Refinement prompt length: %d chars", len(refinementPrompt))

	// Without PruneUpstreamOutput, refinementPrompt contains the full 90KB raw output (~91,000 chars).
	// With PruneUpstreamOutput, it should be safely pruned to <= 15,000 chars.
	if len(refinementPrompt) > 15000 {
		t.Errorf("expected refinement prompt <= 15000 chars, got %d chars", len(refinementPrompt))
	}
}
