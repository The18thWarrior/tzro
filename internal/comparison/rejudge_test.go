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

func TestRejudgeResults_OnlyRejudgesFailedEntries(t *testing.T) {
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

	// Load a real task ID from the embedded suite so findTaskByID works
	tasks, err := LoadTasksByCategory(CategoryDocgen, 0)
	if err != nil {
		t.Fatalf("failed to load docgen tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("no docgen tasks available")
	}
	realTaskID := tasks[0].ID

	results := []ComparisonResult{
		{
			TaskID:       realTaskID,
			Condition:    ConditionCooperative,
			QualityScore: 4.5,
			OutputText:   "# Cache Function Index\n\n- `PruneColumns`\n- `Process`\n",
			// No JudgeError and QualityScore > 0 — should NOT call LLM judge
		},
		{
			TaskID:       realTaskID,
			Condition:    ConditionCloudReAct,
			QualityScore: -1,
			JudgeError:   "ERR_JUDGE_UNAVAILABLE",
			OutputText:   "# Cache Function Index\n\n- `PruneColumns`\n- `Process`\n",
		},
		{
			TaskID:       realTaskID,
			Condition:    ConditionCloudDAG,
			QualityScore: 0, // Zero score but no judge error — should be re-judged
			OutputText:   "# Cache Function Index\n\n- `PruneColumns`\n- `Process`\n",
		},
	}

	opts := JudgeOptions{
		Endpoint: srv.URL,
		Category: CategoryDocgen,
	}

	updated, rejudged, err := RejudgeResults(context.Background(), results, opts)
	if err != nil {
		t.Fatalf("RejudgeResults failed: %v", err)
	}

	// Should have re-judged exactly 2 entries with LLM
	if rejudged != 2 {
		t.Errorf("expected 2 rejudged entries, got %d", rejudged)
	}

	// First result should have deterministic score attached
	if updated[0].DeterministicScore <= 0 {
		t.Errorf("first result should have deterministic score, got %f", updated[0].DeterministicScore)
	}

	// Second result should now have a valid score
	if updated[1].QualityScore <= 0 {
		t.Errorf("second result should have positive score after rejudge, got %f", updated[1].QualityScore)
	}
	if updated[1].JudgeError != "" {
		t.Errorf("second result JudgeError should be cleared, got %q", updated[1].JudgeError)
	}

	// Third result should also be re-judged
	if updated[2].QualityScore <= 0 {
		t.Errorf("third result should have positive score after rejudge, got %f", updated[2].QualityScore)
	}

	// Verify only 2 judge API calls were made (not 3)
	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 judge API calls, got %d", atomic.LoadInt32(&callCount))
	}

	// Verify original input was not mutated
	if results[1].JudgeError != "ERR_JUDGE_UNAVAILABLE" {
		t.Error("original results slice should not be mutated")
	}
}

func TestRejudgeResultsWithOptions_DeterministicOnly(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validJudgeJSON())
	}))
	defer srv.Close()

	tasks, err := LoadTasksByCategory(CategoryDocgen, 0)
	if err != nil || len(tasks) == 0 {
		t.Fatal("no docgen tasks")
	}
	realTaskID := tasks[0].ID

	results := []ComparisonResult{
		{
			TaskID:       realTaskID,
			Condition:    ConditionCooperative,
			QualityScore: 4.0,
			OutputText:   "# Cache Function Index\n\n## Exported Functions\n- `PruneColumns`\n- `Process`\n",
			Logs:         "[Probe] Tool call: read_file internal/cache/cache.go",
		},
		{
			TaskID:       realTaskID,
			Condition:    ConditionCloudReAct,
			QualityScore: -1,
			JudgeError:   "ERR_JUDGE_UNAVAILABLE",
			OutputText:   "# Cache Function Index\n\n## Exported Functions\n- `PruneColumns`\n- `Process`\n",
			Logs:         "[Probe] Tool call: read_file internal/cache/cache.go",
		},
	}

	opts := RejudgeOptions{
		DeterministicOnly: true,
		DetWeight:         0.5,
	}

	updated, rejudged, err := RejudgeResultsWithOptions(context.Background(), results, opts)
	if err != nil {
		t.Fatalf("RejudgeResultsWithOptions failed: %v", err)
	}

	if rejudged != 2 {
		t.Errorf("expected 2 rejudged in deterministic-only mode, got %d", rejudged)
	}

	// Zero API calls in deterministic-only mode
	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("expected 0 judge API calls in deterministic-only mode, got %d", atomic.LoadInt32(&callCount))
	}

	// Verify both results have positive DeterministicScore and QualityScore
	for i, r := range updated {
		if r.DeterministicScore <= 0 {
			t.Errorf("result %d missing DeterministicScore", i)
		}
		if r.QualityScore <= 0 {
			t.Errorf("result %d missing positive QualityScore", i)
		}
		if r.DeterministicChecks == nil {
			t.Errorf("result %d missing DeterministicChecks scorecard", i)
		}
	}
}

func TestRejudgeResultsWithOptions_All(t *testing.T) {
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

	tasks, err := LoadTasksByCategory(CategoryDocgen, 0)
	if err != nil || len(tasks) == 0 {
		t.Fatal("no docgen tasks")
	}
	realTaskID := tasks[0].ID

	results := []ComparisonResult{
		{
			TaskID:       realTaskID,
			Condition:    ConditionCooperative,
			QualityScore: 4.5,
			OutputText:   "# Cache Function Index\n\n- `PruneColumns`\n",
		},
		{
			TaskID:       realTaskID,
			Condition:    ConditionCloudReAct,
			QualityScore: 3.5,
			OutputText:   "# Cache Function Index\n\n- `PruneColumns`\n",
		},
	}

	opts := RejudgeOptions{
		JudgeOptions: JudgeOptions{
			Endpoint: srv.URL,
			Category: CategoryDocgen,
		},
		All: true,
	}

	updated, rejudged, err := RejudgeResultsWithOptions(context.Background(), results, opts)
	if err != nil {
		t.Fatalf("RejudgeResultsWithOptions failed: %v", err)
	}

	if rejudged != 2 {
		t.Errorf("expected 2 rejudged when All: true, got %d", rejudged)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 judge API calls when All: true, got %d", atomic.LoadInt32(&callCount))
	}

	if updated[0].LLMScore <= 0 || updated[1].LLMScore <= 0 {
		t.Errorf("expected LLMScore populated for all entries")
	}
}

func TestRejudgeResults_SkipsExecutionErrors(t *testing.T) {
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

	tasks, err := LoadTasksByCategory(CategoryDocgen, 0)
	if err != nil || len(tasks) == 0 {
		t.Fatal("no docgen tasks")
	}

	results := []ComparisonResult{
		{
			TaskID:       tasks[0].ID,
			Condition:    ConditionCooperative,
			QualityScore: 0,
			Error:        "execution failed",
			OutputText:   "",
		},
	}

	opts := JudgeOptions{Endpoint: srv.URL, Category: CategoryDocgen}
	_, rejudged, err := RejudgeResults(context.Background(), results, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rejudged != 0 {
		t.Errorf("expected 0 rejudged (execution error should be skipped), got %d", rejudged)
	}
	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("expected 0 judge API calls, got %d", atomic.LoadInt32(&callCount))
	}
}
