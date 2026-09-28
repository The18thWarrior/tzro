package decision_test

import (
	"context"
	"testing"
	"time"

	"tzro/pkg/decision"
	"tzro/pkg/executor"
)

type mockProvider struct {
	lastReq *decision.DecisionRequest
}

func (m *mockProvider) Evaluate(ctx context.Context, req *decision.DecisionRequest) (*decision.DecisionResponse, error) {
	m.lastReq = req
	return &decision.DecisionResponse{
		Answer:     "pkg/proxy/proxy.go",
		Confidence: 0.94,
		Scores: map[string]float64{
			"pkg/proxy/proxy.go": 0.94,
			"pkg/store/store.go": 0.06,
		},
		LatencyMs: 14,
	}, nil
}

func (m *mockProvider) Close() error {
	return nil
}

func TestDeciderAdapter_Decide(t *testing.T) {
	provider := &mockProvider{}
	squasher := decision.NewStateSquasher(2048)
	adapter := decision.NewDeciderAdapter(provider, squasher)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &executor.DecisionInput{
		QuestionType: "choice",
		Prompt:       "Which file is responsible?",
		Options:      []string{"pkg/proxy/proxy.go", "pkg/store/store.go"},
		State: map[string]interface{}{
			"diagnostic": "panic: nil pointer",
		},
	}

	out, err := adapter.Decide(ctx, input)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}

	if out.Answer != "pkg/proxy/proxy.go" {
		t.Errorf("expected answer pkg/proxy/proxy.go, got %s", out.Answer)
	}
	if out.Confidence != 0.94 {
		t.Errorf("expected confidence 0.94, got %f", out.Confidence)
	}
	if provider.lastReq.QuestionType != decision.QuestionTypeChoice {
		t.Errorf("expected QuestionTypeChoice, got %v", provider.lastReq.QuestionType)
	}
}
