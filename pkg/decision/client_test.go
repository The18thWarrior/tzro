package decision_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tzro/pkg/decision"
)

func TestLocalDaemonProvider_TracerBullet_EchoLifecycle(t *testing.T) {
	// Create a mock worker script
	tmpDir, err := os.MkdirTemp("", "decision-tracer-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockWorker := filepath.Join(tmpDir, "mock_worker.sh")
	script := `#!/bin/sh
echo '{"status":"ready","model":"mock"}'
while IFS= read -r line; do
  echo '{"answer":"yes","confidence":0.95,"scores":{"yes":0.95,"no":0.05},"latency_ms":12}'
done
`
	if err := os.WriteFile(mockWorker, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock worker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := decision.NewLocalDaemonProvider("/bin/sh", mockWorker)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("failed to start provider: %v", err)
	}
	defer provider.Close()

	req := &decision.DecisionRequest{
		QuestionType: decision.QuestionTypeNoul,
		Prompt:       "Is the decision engine healthy?",
		State: map[string]interface{}{
			"test": "tracer_bullet",
		},
	}

	resp, err := provider.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	if resp.Answer != "yes" {
		t.Errorf("expected answer 'yes', got '%s'", resp.Answer)
	}
	if resp.Confidence < 0.90 {
		t.Errorf("expected confidence >= 0.90, got %f", resp.Confidence)
	}
	if resp.Scores["yes"] != 0.95 {
		t.Errorf("expected score for 'yes' == 0.95, got %v", resp.Scores)
	}
}

func TestLocalDaemonProvider_Close_CleansUp(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decision-close-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockWorker := filepath.Join(tmpDir, "mock_worker.sh")
	script := `#!/bin/sh
echo '{"status":"ready"}'
while IFS= read -r line; do
  echo '{"answer":"yes","confidence":1.0}'
done
`
	if err := os.WriteFile(mockWorker, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock worker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := decision.NewLocalDaemonProvider("/bin/sh", mockWorker)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("failed to start provider: %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Evaluating after close should fail
	_, err = provider.Evaluate(ctx, &decision.DecisionRequest{
		QuestionType: decision.QuestionTypeNoul,
		Prompt:       "test",
	})
	if err == nil {
		t.Errorf("expected error evaluating closed provider, got nil")
	}
}
