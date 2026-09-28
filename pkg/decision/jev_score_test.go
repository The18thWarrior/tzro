package decision_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tzro/pkg/decision"
)

func TestLocalDaemonProvider_WithMockWorkerBinary(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decision-bin-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockBin := filepath.Join(tmpDir, "mock_jev_score")
	buildCmd := exec.Command("go", "build", "-o", mockBin, "./testdata/mock_worker.go")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build mock worker binary: %v, out: %s", err, string(out))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := decision.NewLocalDaemonProvider(mockBin)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("failed to start provider: %v", err)
	}
	defer provider.Close()

	// Test choice request
	choiceReq := &decision.DecisionRequest{
		QuestionType: decision.QuestionTypeChoice,
		Prompt:       "Which file caused the regression?",
		Options:      []string{"pkg/proxy/proxy.go", "pkg/store/store.go"},
		State: map[string]interface{}{
			"error": "panic in proxy",
		},
	}

	resp, err := provider.Evaluate(ctx, choiceReq)
	if err != nil {
		t.Fatalf("Evaluate choice failed: %v", err)
	}

	if resp.Answer != "pkg/proxy/proxy.go" {
		t.Errorf("expected answer pkg/proxy/proxy.go, got %s", resp.Answer)
	}
	if resp.Confidence < 0.90 {
		t.Errorf("expected confidence >= 0.90, got %f", resp.Confidence)
	}
	if len(resp.Scores) != 2 {
		t.Errorf("expected 2 option scores, got %d", len(resp.Scores))
	}

	// Test score request
	scoreReq := &decision.DecisionRequest{
		QuestionType: decision.QuestionTypeScore,
		Prompt:       "Rate the severity of the bug",
	}
	scoreResp, err := provider.Evaluate(ctx, scoreReq)
	if err != nil {
		t.Fatalf("Evaluate score failed: %v", err)
	}
	if scoreResp.Answer != "0.85" {
		t.Errorf("expected answer 0.85, got %s", scoreResp.Answer)
	}
}
