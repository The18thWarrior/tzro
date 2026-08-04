package executor

import (
	"strings"
	"testing"
)

func TestPruneEdgeContext_Empty(t *testing.T) {
	result := PruneEdgeContext(nil, 10000)
	if result != "" {
		t.Errorf("expected empty result for nil entries, got %d chars", len(result))
	}

	result = PruneEdgeContext([]EdgeEntry{}, 10000)
	if result != "" {
		t.Errorf("expected empty result for empty entries, got %d chars", len(result))
	}
}

func TestPruneEdgeContext_ZeroBudget(t *testing.T) {
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "web_search", ToolArgs: `{"query":"test"}`, ResultSnippet: "some result", FullResult: "full result data"},
	}
	result := PruneEdgeContext(entries, 0)
	if result != "" {
		t.Errorf("expected empty result for zero budget, got %d chars", len(result))
	}
}

func TestPruneEdgeContext_UsesFullResultOverSnippet(t *testing.T) {
	entries := []EdgeEntry{
		{
			StepIndex:     1,
			ToolName:      "web_search",
			ToolArgs:      `{"query":"Go CVEs 2024"}`,
			ResultSnippet: "truncated snippet",
			FullResult:    "Full uncompacted web search result with real URLs: https://pkg.go.dev/vuln/ and CVE-2024-12345 details",
		},
	}
	result := PruneEdgeContext(entries, 10000)

	// Should use FullResult, not ResultSnippet
	if !strings.Contains(result, "https://pkg.go.dev/vuln/") {
		t.Error("expected pruned context to contain FullResult URL, not the truncated snippet")
	}
	if !strings.Contains(result, "CVE-2024-12345") {
		t.Error("expected pruned context to contain FullResult CVE details")
	}
}

func TestPruneEdgeContext_FallsBackToSnippet(t *testing.T) {
	entries := []EdgeEntry{
		{
			StepIndex:     1,
			ToolName:      "web_search",
			ToolArgs:      `{"query":"test"}`,
			ResultSnippet: "snippet data with important info",
			FullResult:    "", // empty FullResult
		},
	}
	result := PruneEdgeContext(entries, 10000)
	if !strings.Contains(result, "snippet data with important info") {
		t.Error("expected pruned context to fall back to ResultSnippet when FullResult is empty")
	}
}

func TestPruneEdgeContext_RespectsBudget(t *testing.T) {
	// Create entries with large FullResults
	entries := make([]EdgeEntry, 10)
	for i := 0; i < 10; i++ {
		entries[i] = EdgeEntry{
			StepIndex:     i + 1,
			ToolName:      "web_search",
			ToolArgs:      `{"query":"test"}`,
			ResultSnippet: strings.Repeat("s", 500),
			FullResult:    strings.Repeat("f", 5000),
		}
	}

	budget := 3000
	result := PruneEdgeContext(entries, budget)

	// Result must fit within the budget (with some overhead tolerance for headers)
	if len(result) > budget+500 {
		t.Errorf("pruned context (%d chars) significantly exceeds budget (%d chars)", len(result), budget)
	}

	// Should include the header showing dropped entries
	if !strings.Contains(result, "earliest omitted") {
		t.Error("expected header noting omitted entries when budget forces truncation")
	}
}

func TestPruneEdgeContext_MostRecentFirst(t *testing.T) {
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "web_search", ToolArgs: `{"query":"old"}`, FullResult: strings.Repeat("a", 3000)},
		{StepIndex: 2, ToolName: "web_search", ToolArgs: `{"query":"middle"}`, FullResult: strings.Repeat("b", 3000)},
		{StepIndex: 3, ToolName: "web_search", ToolArgs: `{"query":"newest"}`, FullResult: strings.Repeat("c", 3000)},
	}

	// Small budget should keep newest entries and drop oldest
	result := PruneEdgeContext(entries, 2000)

	// Newest should be present
	if !strings.Contains(result, `"query":"newest"`) {
		t.Error("expected newest entry to be included in tight budget")
	}
}

func TestPruneEdgeContext_ChronologicalOrder(t *testing.T) {
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "web_search", ToolArgs: `{"query":"first"}`, FullResult: "result 1"},
		{StepIndex: 2, ToolName: "web_browse", ToolArgs: `{"url":"example.com"}`, FullResult: "result 2"},
		{StepIndex: 3, ToolName: "web_search", ToolArgs: `{"query":"third"}`, FullResult: "result 3"},
	}

	result := PruneEdgeContext(entries, 50000)

	// Output should be in chronological order (Step 1 before Step 2 before Step 3)
	idx1 := strings.Index(result, "Step 1")
	idx2 := strings.Index(result, "Step 2")
	idx3 := strings.Index(result, "Step 3")

	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatal("expected all three steps to be present")
	}
	if idx1 > idx2 || idx2 > idx3 {
		t.Error("expected entries in chronological order (Step 1 < Step 2 < Step 3)")
	}
}

func TestPruneEdgeContext_AllIncludedHeader(t *testing.T) {
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "web_search", ToolArgs: `{"query":"test"}`, FullResult: "result"},
	}

	result := PruneEdgeContext(entries, 50000)

	// When all entries fit, header should not mention "omitted"
	if strings.Contains(result, "omitted") {
		t.Error("expected no 'omitted' in header when all entries fit")
	}
	if !strings.Contains(result, "1 steps") {
		t.Error("expected header showing total step count")
	}
}

func TestPruneEdgeContext_SkipsEmptyEntries(t *testing.T) {
	entries := []EdgeEntry{
		{StepIndex: 1, ToolName: "web_search", ToolArgs: `{"query":"real"}`, FullResult: "real data"},
		{StepIndex: 2, ToolName: "web_search", ToolArgs: `{"query":"empty"}`, FullResult: "", ResultSnippet: ""}, // both empty
		{StepIndex: 3, ToolName: "web_search", ToolArgs: `{"query":"also_real"}`, FullResult: "more data"},
	}

	result := PruneEdgeContext(entries, 50000)

	if strings.Contains(result, "Step 2") {
		t.Error("expected empty entries to be skipped")
	}
	if !strings.Contains(result, "Step 1") || !strings.Contains(result, "Step 3") {
		t.Error("expected non-empty entries to be included")
	}
}
