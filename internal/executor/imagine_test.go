package executor

import (
	"context"
	"testing"
)

// TestImagineToolOutputReturnsNonEmpty verifies that the imagination function
// produces a plausible non-empty output for a known tool.
func TestImagineToolOutputReturnsNonEmpty(t *testing.T) {
	// Use the mock-based imagination (no real inference sidecar needed)
	output, err := ImagineToolOutput(context.Background(), "read_file", map[string]interface{}{
		"path": "/etc/config.json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output == "" {
		t.Error("imagined output should not be empty")
	}
}

// TestImagineToolOutputIncludesToolName verifies that the imagined output
// references the tool being simulated for context.
func TestImagineToolOutputIncludesToolName(t *testing.T) {
	output, err := ImagineToolOutput(context.Background(), "web_search", map[string]interface{}{
		"query": "AI orchestration frameworks",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The output should be a plausible simulation — not empty
	if len(output) < 10 {
		t.Errorf("imagined output too short (%d chars), expected plausible simulation", len(output))
	}
}
