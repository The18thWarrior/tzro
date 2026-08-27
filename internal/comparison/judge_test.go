package comparison

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Pure JSON",
			input:    `{"score": 5}`,
			expected: `{"score": 5}`,
		},
		{
			name:     "Markdown JSON block",
			input:    "```json\n{\"score\": 5}\n```",
			expected: `{"score": 5}`,
		},
		{
			name:     "Markdown block no language",
			input:    "```\n{\"score\": 5}\n```",
			expected: `{"score": 5}`,
		},
		{
			name:     "Text before and after",
			input:    "Sure! Here it is:\n```json\n{\"score\": 5}\n```\nHope this helps!",
			expected: `{"score": 5}`,
		},
		{
			name:     "Unterminated block",
			input:    "```json\n{\"score\": 5}",
			expected: `{"score": 5}`,
		},
		{
			name:     "Garbage prefix",
			input:    "{\"not\": \"json\"} text ```json\n{\"score\": 5}\n```",
			expected: `{"score": 5}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCodeFences(tt.input)
			if got != tt.expected {
				t.Errorf("stripCodeFences() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseFlatJudgeResponseDetailed_WithRatings(t *testing.T) {
	rubric := QualityRubric{
		Criteria: []RubricCriterion{
			{Name: "Accuracy", Description: "Factual correctness"},
			{Name: "Completeness", Description: "All topics covered"},
		},
		MaxScore: 5.0,
	}

	rawJSON := `{
		"criteria": [
			{"name": "Accuracy", "score": 4.5, "reasoning": "Accurate"},
			{"name": "Completeness", "score": 4.0, "reasoning": "Complete"}
		],
		"ratings": {
			"goalAlignment": 4.8,
			"factualGrounding": 4.5,
			"coherence": 5.0,
			"completeness": 4.0
		},
		"overallScore": 4.4,
		"summary": "Great analysis"
	}`

	resp, err := parseFlatJudgeResponseDetailed(rawJSON, rubric)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GoalAlignment != 4.8 {
		t.Errorf("expected GoalAlignment 4.8, got %f", resp.GoalAlignment)
	}
	if resp.FactualGrounding != 4.5 {
		t.Errorf("expected FactualGrounding 4.5, got %f", resp.FactualGrounding)
	}
	if resp.Coherence != 5.0 {
		t.Errorf("expected Coherence 5.0, got %f", resp.Coherence)
	}
	if resp.Completeness != 4.0 {
		t.Errorf("expected Completeness 4.0, got %f", resp.Completeness)
	}
	if resp.OverallScore != 4.4 {
		t.Errorf("expected OverallScore 4.4, got %f", resp.OverallScore)
	}
}

// validJudgeJSON returns a valid judge response body for httptest servers.
func validJudgeJSON() string {
	return `{"choices":[{"message":{"content":"{\"criteria\":[{\"name\":\"Accuracy\",\"score\":4.0}],\"overallScore\":4.0,\"summary\":\"Good\"}"}}]}`
}

func TestJudgeRetry_SucceedsOnThirdAttempt(t *testing.T) {
	// Override backoffs to zero for fast tests
	judgeRetryBackoffsOverride = []time.Duration{0, 0, 0}
	defer func() { judgeRetryBackoffsOverride = nil }()

	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n < 3 {
			// First two attempts fail with 500
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error": "service unavailable"}`)
			return
		}
		// Third attempt succeeds
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validJudgeJSON())
	}))
	defer srv.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{{Name: "Accuracy", Description: "test"}},
		MaxScore: 5.0,
	}
	opts := JudgeOptions{Endpoint: srv.URL, Category: CategoryDocgen}

	resp, err := JudgeOutputDetailedWithRetry(context.Background(), "test output", rubric, opts)
	if err != nil {
		t.Fatalf("expected success on third attempt, got error: %v", err)
	}
	if resp.OverallScore <= 0 {
		t.Errorf("expected positive quality score, got %f", resp.OverallScore)
	}
	if atomic.LoadInt32(&callCount) != 3 {
		t.Errorf("expected 3 API calls, got %d", atomic.LoadInt32(&callCount))
	}
}

func TestJudgeRetry_AllAttemptsExhausted(t *testing.T) {
	// Override backoffs to zero for fast tests
	judgeRetryBackoffsOverride = []time.Duration{0, 0, 0}
	defer func() { judgeRetryBackoffsOverride = nil }()

	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": "always failing"}`)
	}))
	defer srv.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{{Name: "Accuracy", Description: "test"}},
		MaxScore: 5.0,
	}
	opts := JudgeOptions{Endpoint: srv.URL, Category: CategoryDocgen}

	resp, err := JudgeOutputDetailedWithRetry(context.Background(), "test output", rubric, opts)
	if err == nil {
		t.Fatal("expected error after all retries exhausted, got nil")
	}
	if resp != nil {
		t.Errorf("expected nil response on exhausted retries, got %+v", resp)
	}
	// Should have made 4 attempts total (1 initial + 3 retries)
	if atomic.LoadInt32(&callCount) != 4 {
		t.Errorf("expected 4 API calls (1 initial + 3 retries), got %d", atomic.LoadInt32(&callCount))
	}
}

func TestJudgeRetry_SucceedsFirstAttempt(t *testing.T) {
	// Override backoffs to zero for fast tests
	judgeRetryBackoffsOverride = []time.Duration{0, 0, 0}
	defer func() { judgeRetryBackoffsOverride = nil }()

	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validJudgeJSON())
	}))
	defer srv.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{{Name: "Accuracy", Description: "test"}},
		MaxScore: 5.0,
	}
	opts := JudgeOptions{Endpoint: srv.URL, Category: CategoryDocgen}

	resp, err := JudgeOutputDetailedWithRetry(context.Background(), "test output", rubric, opts)
	if err != nil {
		t.Fatalf("expected success on first attempt, got error: %v", err)
	}
	if resp.OverallScore <= 0 {
		t.Errorf("expected positive quality score, got %f", resp.OverallScore)
	}
	// Should have made exactly 1 call — no unnecessary retries
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 API call (no retries needed), got %d", atomic.LoadInt32(&callCount))
	}
}
