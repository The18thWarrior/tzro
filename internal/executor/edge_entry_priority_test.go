package executor

import (
	"strings"
	"testing"
)

func TestScoreEdgeEntry_Errors(t *testing.T) {
	firstSeen := map[string]bool{}
	if got := ScoreEdgeEntry("read_file", "Error: file not found", firstSeen); got != EdgePriorityThrowaway {
		t.Errorf("error output should be throwaway, got %d", got)
	}
	if got := ScoreEdgeEntry("sql_cached_data", "error: table not found", firstSeen); got != EdgePriorityThrowaway {
		t.Errorf("lowercase error output should be throwaway, got %d", got)
	}
}

func TestScoreEdgeEntry_SchemaDiscovery(t *testing.T) {
	firstSeen := map[string]bool{}
	if got := ScoreEdgeEntry("introspect_cache", `{"columns": ["id", "name"]}`, firstSeen); got != EdgePriorityHigh {
		t.Errorf("introspect_cache should always be high priority, got %d", got)
	}
}

func TestScoreEdgeEntry_FirstOfTool(t *testing.T) {
	firstSeen := map[string]bool{}

	// First sql_cached_data should be high priority
	if got := ScoreEdgeEntry("sql_cached_data", `[{"id": 1}]`, firstSeen); got != EdgePriorityHigh {
		t.Errorf("first sql_cached_data should be high priority, got %d", got)
	}
	firstSeen["sql_cached_data"] = true

	// Second should be standard
	if got := ScoreEdgeEntry("sql_cached_data", `[{"id": 2}]`, firstSeen); got != EdgePriorityStandard {
		t.Errorf("subsequent sql_cached_data should be standard, got %d", got)
	}
}

func TestScoreEdgeEntry_EmptyListDir(t *testing.T) {
	firstSeen := map[string]bool{"list_dir": true}
	if got := ScoreEdgeEntry("list_dir", "[]", firstSeen); got != EdgePriorityLow {
		t.Errorf("empty list_dir should be low priority, got %d", got)
	}
}

func TestTruncateEdgeLogByPriority_EvictsLowest(t *testing.T) {
	// Create entries with different priorities. Budget is tight enough to
	// require eviction. Verify low-priority entries are dropped while
	// high-priority ones survive regardless of position.
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "introspect_cache", ToolArgs: `{"id":"c1"}`, ResultSnippet: "schema", Priority: EdgePriorityHigh},
		{StepIndex: 2, ToolName: "read_file", ToolArgs: `{"p":"a"}`, ResultSnippet: "func A", Priority: EdgePriorityStandard},
		{StepIndex: 3, ToolName: "read_file", ToolArgs: `{"p":"b"}`, ResultSnippet: "err", Priority: EdgePriorityThrowaway},
		{StepIndex: 4, ToolName: "list_dir", ToolArgs: `{"p":"/e"}`, ResultSnippet: "[]", Priority: EdgePriorityLow},
		{StepIndex: 5, ToolName: "sql_cached_data", ToolArgs: `{"sql":"Q"}`, ResultSnippet: "rows", Priority: EdgePriorityHigh},
		{StepIndex: 6, ToolName: "read_file", ToolArgs: `{"p":"c"}`, ResultSnippet: "func C", Priority: EdgePriorityStandard},
	}

	// Budget fits ~4 entries but not all 6 — forces eviction
	budget := 350
	result := truncateEdgeLogByPriority(entries, budget)

	// High-priority entries should survive
	if !strings.Contains(result, "Step 1") {
		t.Error("high-priority entry (step 1) should survive eviction")
	}

	// Throwaway entry should be evicted first
	if strings.Contains(result, "Step 3") {
		t.Error("throwaway entry (step 3) should be evicted")
	}

	// Low-priority entry should be evicted before standard
	if strings.Contains(result, "Step 4") && !strings.Contains(result, "Step 2") {
		t.Error("low-priority (step 4) should be evicted before standard (step 2)")
	}

	// Should contain eviction stats
	if !strings.Contains(result, "evicted") {
		t.Error("result should contain eviction statistics")
	}
}

func TestTruncateEdgeLogByPriority_PreservesChronologicalOrder(t *testing.T) {
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "list_dir", ToolArgs: "{}", ResultSnippet: "files", Priority: EdgePriorityStandard},
		{StepIndex: 5, ToolName: "read_file", ToolArgs: "{}", ResultSnippet: "code", Priority: EdgePriorityStandard},
		{StepIndex: 10, ToolName: "search_files", ToolArgs: "{}", ResultSnippet: "found", Priority: EdgePriorityStandard},
	}

	result := truncateEdgeLogByPriority(entries, 100_000) // large budget, no eviction

	// Steps should appear in chronological order
	idx1 := strings.Index(result, "Step 1")
	idx5 := strings.Index(result, "Step 5")
	idx10 := strings.Index(result, "Step 10")

	if idx1 < 0 || idx5 < 0 || idx10 < 0 {
		t.Fatal("all entries should be present")
	}
	if idx1 > idx5 || idx5 > idx10 {
		t.Error("entries should appear in chronological order")
	}
}
