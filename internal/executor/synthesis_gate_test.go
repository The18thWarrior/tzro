package executor

import (
	"encoding/json"
	"testing"
)

// TestSynthesisValidationSchema_Parses verifies the validation gate schema
// produces parseable JSON with ready, reason, and additionalSteps fields.
func TestSynthesisValidationSchema_Parses(t *testing.T) {
	// The schema should be valid JSON
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(SynthesisValidationSchema), &schema); err != nil {
		t.Fatalf("SynthesisValidationSchema is not valid JSON: %v", err)
	}

	// Check required fields
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing 'properties'")
	}
	for _, field := range []string{"ready", "reason", "additionalSteps"} {
		if _, exists := props[field]; !exists {
			t.Errorf("schema missing property %q", field)
		}
	}
}

// TestComputeUnusedTools_AllUsed verifies that when all tools have been
// used, the unused set is empty.
func TestComputeUnusedTools_AllUsed(t *testing.T) {
	allowed := []string{"read_file", "list_dir", "search_files"}
	used := map[string]bool{"read_file": true, "list_dir": true, "search_files": true}
	unused := computeUnusedTools(allowed, used)
	if len(unused) != 0 {
		t.Errorf("expected 0 unused tools, got %v", unused)
	}
}

// TestComputeUnusedTools_NoneUsed verifies that all tools are returned
// when none have been used.
func TestComputeUnusedTools_NoneUsed(t *testing.T) {
	allowed := []string{"read_file", "list_dir", "web_search"}
	used := map[string]bool{}
	unused := computeUnusedTools(allowed, used)
	if len(unused) != 3 {
		t.Errorf("expected 3 unused tools, got %v", unused)
	}
}

// TestComputeUnusedTools_PartiallyUsed verifies correct filtering.
func TestComputeUnusedTools_PartiallyUsed(t *testing.T) {
	allowed := []string{"read_file", "list_dir", "web_search", "web_browse"}
	used := map[string]bool{"read_file": true, "web_search": true}
	unused := computeUnusedTools(allowed, used)
	if len(unused) != 2 {
		t.Fatalf("expected 2 unused tools, got %v", unused)
	}
	// Check specific unused tools
	foundListDir := false
	foundWebBrowse := false
	for _, tool := range unused {
		if tool == "list_dir" {
			foundListDir = true
		}
		if tool == "web_browse" {
			foundWebBrowse = true
		}
	}
	if !foundListDir || !foundWebBrowse {
		t.Errorf("expected list_dir and web_browse in unused, got %v", unused)
	}
}
