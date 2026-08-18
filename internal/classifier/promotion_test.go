package classifier

import (
	"testing"
)

func TestShouldPromoteToWorkflow_Temporal(t *testing.T) {
	tests := []struct {
		prompt   string
		expected bool
	}{
		{"Run every Monday at 9am: generate lead report", true},
		{"Schedule every Friday to clean cache", true},
		{"Wait for confirmation before running migration", true},
		{"Ask me before executing the batch delete", true},
		{"Dry run and request approval", true},
		{"Check every detail carefully", false},
		{"Summarize daily standup notes from yesterday", false},
		{"Tell me a story about Monday mornings", false},
	}

	for _, tt := range tests {
		got := ShouldPromoteToWorkflow(tt.prompt)
		if got != tt.expected {
			t.Errorf("ShouldPromoteToWorkflow(%q) = %v, expected %v", tt.prompt, got, tt.expected)
		}
	}
}
