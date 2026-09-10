package signaldensity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCircuitBreaker_HaltOnMaxCost(t *testing.T) {
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		resp := chatResponse{
			Choices: []chatChoice{
				{
					Message: chatMessage{
						Role:    "assistant",
						Content: "Answer with Get && Set && Delete",
					},
				},
			},
			Usage: chatUsage{
				PromptTokens:     1000,
				CompletionTokens: 50,
				Cost:             0.75, // $0.75 per turn
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Set max cost to $1.00. First call costs $0.75 (accum = $0.75 < $1.00).
	// Second call costs $0.75 (accum = $1.50 >= $1.00 -> circuit breaker triggers).
	// Subsequent calls must be halted.
	cfg := BenchmarkConfig{
		Model:     "test-model",
		Tier:      TierMicro,
		Primitive: PrimitiveSkeleton,
		MaxCost:   1.00,
		BaseURL:   server.URL,
		NoCache:   true,
	}

	runner := NewRunner(cfg)
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if report.Metadata.TotalCostUSD < 1.00 {
		t.Errorf("expected spend >= 1.00, got %f", report.Metadata.TotalCostUSD)
	}

	// Verify requestCount is capped (should be 2 turns before halting, far fewer than full battery)
	if atomic.LoadInt32(&requestCount) > 4 {
		t.Errorf("circuit breaker failed to halt early: requests=%d", requestCount)
	}
}
