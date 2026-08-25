package executor

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Slice 6: Pre-Flight coverage re-extraction for List Nodes
// ---------------------------------------------------------------------------

func TestCheckListCoverage_AllPresent(t *testing.T) {
	goal := "List all exported functions including ExecuteSQL, CacheGet, and MetricsFlush"
	output := `--- file: cache.go lines: 10-20 ---
func ExecuteSQL(ctx context.Context, query string) error {
--- file: cache.go lines: 30-40 ---
func CacheGet(key string) (interface{}, error) {
--- file: metrics.go lines: 5-15 ---
func MetricsFlush() error {`

	missing := CheckListCoverage(goal, output)
	if len(missing) != 0 {
		t.Errorf("CheckListCoverage: expected no missing items, got %v", missing)
	}
}

func TestCheckListCoverage_SomeMissing(t *testing.T) {
	goal := "List all exported functions including ExecuteSQL, CacheGet, and MetricsFlush"
	output := `--- file: cache.go lines: 10-20 ---
func ExecuteSQL(ctx context.Context, query string) error {`

	missing := CheckListCoverage(goal, output)
	if len(missing) != 2 {
		t.Fatalf("CheckListCoverage: expected 2 missing items, got %d: %v", len(missing), missing)
	}

	// Check that missing items are correct
	joined := strings.Join(missing, ",")
	if !strings.Contains(joined, "CacheGet") {
		t.Errorf("CheckListCoverage: expected 'CacheGet' in missing items, got %v", missing)
	}
	if !strings.Contains(joined, "MetricsFlush") {
		t.Errorf("CheckListCoverage: expected 'MetricsFlush' in missing items, got %v", missing)
	}
}

func TestCheckListCoverage_NoKeyTerms(t *testing.T) {
	goal := "List all exported functions in the package"
	output := "--- file: cache.go lines: 1-10 ---\nfunc SomeFunc() {}"

	// When the goal has no specific identifiable terms, return empty
	missing := CheckListCoverage(goal, output)
	if len(missing) != 0 {
		t.Errorf("CheckListCoverage: expected no missing items for generic goal, got %v", missing)
	}
}
