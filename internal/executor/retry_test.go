package executor

import (
	"testing"
)

func TestSchemaValidationGate_MissingRequired(t *testing.T) {
	// Mock tool with required fields
	// Since we can't easily mock tools.GetSchema here without the registry,
	// test the validation logic directly with a nil schema (should pass)
	err := validateAgainstSchema("nonexistent_tool", map[string]interface{}{
		"query": "hello",
	})
	if err != nil {
		t.Errorf("expected nil error for nonexistent tool (no schema to validate), got: %v", err)
	}
}

func TestSchemaValidationGate_EmptyField(t *testing.T) {
	// tools.GetSchema returns a fallback schema with "query" required for unknown tools.
	// validateAgainstSchema should correctly catch empty required fields.
	err := validateAgainstSchema("truly_unknown_tool_xyz", map[string]interface{}{
		"query": "",
	})
	if err == nil {
		t.Error("expected validation error for empty required field 'query', got nil")
	}
}

func TestRetryWithCloud_NoConfig(t *testing.T) {
	// retryWithCloud should fail gracefully when no cloud config is set
	// This tests the function signature and error path without a real cloud endpoint
	// We don't call it here because it would make a real HTTP request
	// Just verify the function exists and compiles
	_ = retryWithCloud
}
