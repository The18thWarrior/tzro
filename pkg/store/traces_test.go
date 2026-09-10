package store_test

import (
	"testing"
	"time"
	"tzro/pkg/store"
)

func TestStore_ContextTraces(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	trace := &store.ContextTrace{
		ID:         "trace_001",
		Workspace:  "/workspaces/repo-a",
		CreatedAt:  time.Now().UTC(),
		Query:      "test query",
		Budget:     4000,
		ConfigJSON: `{"budget":4000,"compaction":"smart"}`,
		TraceJSON:  `{"discovery":{"candidates":["a.go","b.go"]}}`,
		SizeBytes:  150,
	}

	if err := s.PutContextTrace(trace); err != nil {
		t.Fatalf("PutContextTrace failed: %v", err)
	}

	// Retrieve trace with matching workspace
	retrieved, err := s.GetContextTrace("trace_001", "/workspaces/repo-a")
	if err != nil {
		t.Fatalf("GetContextTrace failed: %v", err)
	}
	if retrieved.ID != "trace_001" || retrieved.Query != "test query" {
		t.Errorf("unexpected retrieved trace: %+v", retrieved)
	}

	// Workspace isolation check: cross-workspace lookup must fail
	_, err = s.GetContextTrace("trace_001", "/workspaces/repo-b")
	if err == nil {
		t.Errorf("expected cross-workspace lookup to fail")
	}

	// List traces for workspace
	traces, err := s.ListContextTraces("/workspaces/repo-a", 10)
	if err != nil {
		t.Fatalf("ListContextTraces failed: %v", err)
	}
	if len(traces) != 1 || traces[0].ID != "trace_001" {
		t.Errorf("unexpected traces listed: %+v", traces)
	}
}

func TestStore_TraceOutcomes(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// External harness stores outcome data keyed by trace ID
	outcomeData := `{"completed":true,"retries":0,"elapsed_ms":120}`
	if err := s.PutTraceOutcome("trace_001", outcomeData); err != nil {
		t.Fatalf("PutTraceOutcome failed: %v", err)
	}

	got, err := s.GetTraceOutcome("trace_001")
	if err != nil {
		t.Fatalf("GetTraceOutcome failed: %v", err)
	}
	if got != outcomeData {
		t.Errorf("expected %q, got %q", outcomeData, got)
	}

	// Non-existent outcome returns empty/error
	gotNonExistent, err := s.GetTraceOutcome("trace_non_existent")
	if err == nil && gotNonExistent != "" {
		t.Errorf("expected empty outcome for non-existent trace, got %q", gotNonExistent)
	}
}

func TestStore_EvictContextTracesLRU(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Add 3 traces of size 1000 each
	for i := 1; i <= 3; i++ {
		tr := &store.ContextTrace{
			ID:         "tr_" + string(rune('0'+i)),
			Workspace:  "/ws",
			CreatedAt:  time.Now().UTC().Add(time.Duration(i) * time.Minute),
			ConfigJSON: "{}",
			TraceJSON:  "{}",
			SizeBytes:  1000,
		}
		if err := s.PutContextTrace(tr); err != nil {
			t.Fatalf("PutContextTrace failed: %v", err)
		}
	}

	// Evict to maxBytes = 1500 -> should evict the oldest trace
	evicted, err := s.EvictContextTracesLRU("/ws", 1500)
	if err != nil {
		t.Fatalf("EvictContextTracesLRU failed: %v", err)
	}
	if evicted != 2 { // 3000 -> need <= 1500, deleting 1 leaves 2000 (still > 1500), so deleting 2 leaves 1000 (<= 1500)
		t.Errorf("expected 2 traces evicted, got %d", evicted)
	}

	// Check remaining trace is the newest (tr_3)
	traces, _ := s.ListContextTraces("/ws", 10)
	if len(traces) != 1 || traces[0].ID != "tr_3" {
		t.Errorf("expected remaining trace to be tr_3, got %+v", traces)
	}
}
