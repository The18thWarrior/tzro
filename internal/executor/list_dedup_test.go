package executor

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Slice 5: dedupParentPaths — parent-directory deduplication
// ---------------------------------------------------------------------------

func TestDedupParentPaths_ChildSupersedesParent(t *testing.T) {
	// When both "internal" and "internal/cache" are detected, the parent
	// should be dropped because the child is more specific (v5 bug fix).
	input := []string{
		"/project/internal",
		"/project/internal/cache",
	}

	result := dedupParentPaths(input)

	if len(result) != 1 {
		t.Fatalf("dedupParentPaths: expected 1 path, got %d: %v", len(result), result)
	}
	if result[0] != "/project/internal/cache" {
		t.Errorf("dedupParentPaths: expected '/project/internal/cache', got %q", result[0])
	}
}

func TestDedupParentPaths_NonOverlappingPreserved(t *testing.T) {
	input := []string{
		"/project/internal/cache",
		"/project/docs/adr",
	}

	result := dedupParentPaths(input)

	if len(result) != 2 {
		t.Fatalf("dedupParentPaths: expected 2 paths, got %d: %v", len(result), result)
	}
}

func TestDedupParentPaths_MultipleChildren(t *testing.T) {
	// If "internal", "internal/cache", and "internal/executor" are all present,
	// only the two children should survive.
	input := []string{
		"/project/internal",
		"/project/internal/cache",
		"/project/internal/executor",
	}

	result := dedupParentPaths(input)

	if len(result) != 2 {
		t.Fatalf("dedupParentPaths: expected 2 paths, got %d: %v", len(result), result)
	}
	// Verify parent was dropped
	for _, p := range result {
		if p == "/project/internal" {
			t.Error("dedupParentPaths: parent '/project/internal' should have been dropped")
		}
	}
}

func TestDedupParentPaths_EmptyInput(t *testing.T) {
	result := dedupParentPaths(nil)
	if result != nil {
		t.Errorf("dedupParentPaths(nil): expected nil, got %v", result)
	}
}

func TestDedupParentPaths_SinglePath(t *testing.T) {
	input := []string{"/project/internal/cache"}
	result := dedupParentPaths(input)

	if len(result) != 1 {
		t.Fatalf("dedupParentPaths: expected 1 path, got %d", len(result))
	}
	if result[0] != "/project/internal/cache" {
		t.Errorf("dedupParentPaths: expected original path, got %q", result[0])
	}
}

func TestDedupParentPaths_DeepNesting(t *testing.T) {
	// Three levels: /a, /a/b, /a/b/c — only the deepest child survives
	input := []string{
		"/project/a",
		"/project/a/b",
		"/project/a/b/c",
	}

	result := dedupParentPaths(input)

	if len(result) != 1 {
		t.Fatalf("dedupParentPaths: expected 1 path (deepest only), got %d: %v", len(result), result)
	}
	if result[0] != "/project/a/b/c" {
		t.Errorf("dedupParentPaths: expected '/project/a/b/c', got %q", result[0])
	}
}
