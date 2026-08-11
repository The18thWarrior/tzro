package executor

import "testing"

// --- Slice 17: classifyProbeGoal returns correct mode ---

func TestClassifyProbeGoal(t *testing.T) {
	tests := []struct {
		goal     string
		expected string
	}{
		// Overview
		{"Write a README for this project", "overview"},
		{"Give me an architecture overview", "overview"},
		{"Explain the codebase structure", "overview"},
		{"comprehensive documentation of all modules", "overview"},
		{"Describe the project for a new developer", "overview"},

		// Aggregate
		{"Summarize all ADRs in docs/adr/", "aggregate"},
		{"List all exported functions", "aggregate"},
		{"Compile all configuration options", "aggregate"},
		{"Summarize each module's purpose", "aggregate"},
		{"Catalog every endpoint", "aggregate"},

		// Focused
		{"Explain how the compiler works", "focused"},
		{"Trace the request lifecycle", "focused"},
		{"How does the KV cache work?", "focused"},
		{"Deep dive into the executor module", "focused"},
		{"Debug the probe execution failure", "focused"},

		// Unknown (Thought Chain fallback)
		{"Do something interesting", ""},
		{"Fix the build", ""},
		{"Implement the feature", ""},
	}

	for _, tt := range tests {
		t.Run(tt.goal, func(t *testing.T) {
			got := classifyProbeGoal(tt.goal)
			if got != tt.expected {
				t.Errorf("classifyProbeGoal(%q) = %q, want %q", tt.goal, got, tt.expected)
			}
		})
	}
}
