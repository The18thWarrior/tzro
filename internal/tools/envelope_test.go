package tools

import (
	"testing"
)

// TestToolSuccess_WithHint verifies that success results can carry hints
// to guide the probe toward next-best-action.
func TestToolSuccess_WithHint(t *testing.T) {
	result := ToolSuccess("search results",
		WithHint("Found 3 results — use web_browse to read the most relevant."),
	)

	if !result.Success {
		t.Fatal("expected success=true")
	}
	if result.Hint != "Found 3 results — use web_browse to read the most relevant." {
		t.Errorf("expected hint on success result, got %q", result.Hint)
	}
}

// TestToolSuccess_WithRelatedTools verifies that success results can
// suggest related tools to the probe.
func TestToolSuccess_WithRelatedTools(t *testing.T) {
	result := ToolSuccess("directory listing",
		WithHint("Found 5 files — read_file the most relevant."),
		WithRelatedTools("read_file", "search_files"),
	)

	if !result.Success {
		t.Fatal("expected success=true")
	}
	if len(result.RelatedTools) != 2 {
		t.Fatalf("expected 2 related tools, got %d", len(result.RelatedTools))
	}
	if result.RelatedTools[0] != "read_file" {
		t.Errorf("expected first related tool to be read_file, got %q", result.RelatedTools[0])
	}
}

// TestToolError_WithHint verifies existing error hint behavior still works.
func TestToolError_WithHint(t *testing.T) {
	result := ToolError("file not found",
		WithHint("Try list_dir to find the correct path."),
		WithRelatedTools("list_dir"),
	)

	if result.Success {
		t.Fatal("expected success=false for error")
	}
	if result.Hint != "Try list_dir to find the correct path." {
		t.Errorf("expected error hint, got %q", result.Hint)
	}
	if len(result.RelatedTools) != 1 {
		t.Fatalf("expected 1 related tool, got %d", len(result.RelatedTools))
	}
}
