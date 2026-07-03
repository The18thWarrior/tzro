package comparison

import (
	"testing"
)

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Pure JSON",
			input:    `{"score": 5}`,
			expected: `{"score": 5}`,
		},
		{
			name:     "Markdown JSON block",
			input:    "```json\n{\"score\": 5}\n```",
			expected: `{"score": 5}`,
		},
		{
			name:     "Markdown block no language",
			input:    "```\n{\"score\": 5}\n```",
			expected: `{"score": 5}`,
		},
		{
			name:     "Text before and after",
			input:    "Sure! Here it is:\n```json\n{\"score\": 5}\n```\nHope this helps!",
			expected: `{"score": 5}`,
		},
		{
			name:     "Unterminated block",
			input:    "```json\n{\"score\": 5}",
			expected: `{"score": 5}`,
		},
		{
			name:     "Garbage prefix",
			input:    "{\"not\": \"json\"} text ```json\n{\"score\": 5}\n```",
			expected: `{"score": 5}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCodeFences(tt.input)
			if got != tt.expected {
				t.Errorf("stripCodeFences() = %v, want %v", got, tt.expected)
			}
		})
	}
}
