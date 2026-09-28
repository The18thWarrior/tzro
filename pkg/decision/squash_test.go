package decision_test

import (
	"strings"
	"testing"

	"tzro/pkg/decision"
)

func TestStateSquasher_Enforces2048Cap(t *testing.T) {
	squasher := decision.NewStateSquasher(2048)

	// Build an oversized state with huge logs, 30 candidates, and long strings
	longLog := strings.Repeat("2026-09-28 ERROR: unexpected nil pointer at trace_exec.go:99\n", 200) // 12,000 chars
	candidates := make([]interface{}, 35)
	for i := 0; i < 35; i++ {
		candidates[i] = map[string]interface{}{
			"file":   "pkg/module/file.go",
			"score":  0.85,
			"symbol": "ExecuteStep",
		}
	}

	rawState := map[string]interface{}{
		"diagnostic": longLog,
		"candidates": candidates,
		"overview":   strings.Repeat("Long overview describing codebase architecture. ", 50),
	}

	compacted, tokens, err := squasher.CompactState(rawState)
	if err != nil {
		t.Fatalf("CompactState returned error: %v", err)
	}

	if tokens > 2048 {
		t.Errorf("expected estimated tokens <= 2048, got %d", tokens)
	}

	// Verify candidate list is budgeted to <= 20
	candList, ok := compacted["candidates"].([]interface{})
	if !ok {
		t.Fatalf("expected candidates to be a slice, got %T", compacted["candidates"])
	}
	if len(candList) > 20 {
		t.Errorf("expected candidates to be capped at 20, got %d", len(candList))
	}

	// Verify diagnostic was compacted with head/tail
	diag, ok := compacted["diagnostic"].(string)
	if !ok {
		t.Fatalf("expected diagnostic to be string")
	}
	if !strings.Contains(diag, "elided") && !strings.Contains(diag, "lines") && len(diag) >= len(longLog) {
		t.Errorf("expected diagnostic to be truncated or compacted")
	}
}
