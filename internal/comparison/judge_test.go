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

func TestParseFlatJudgeResponseDetailed_WithRatings(t *testing.T) {
	rubric := QualityRubric{
		Criteria: []RubricCriterion{
			{Name: "Accuracy", Description: "Factual correctness"},
			{Name: "Completeness", Description: "All topics covered"},
		},
		MaxScore: 5.0,
	}

	rawJSON := `{
		"criteria": [
			{"name": "Accuracy", "score": 4.5, "reasoning": "Accurate"},
			{"name": "Completeness", "score": 4.0, "reasoning": "Complete"}
		],
		"ratings": {
			"goalAlignment": 4.8,
			"factualGrounding": 4.5,
			"coherence": 5.0,
			"completeness": 4.0
		},
		"overallScore": 4.4,
		"summary": "Great analysis"
	}`

	resp, err := parseFlatJudgeResponseDetailed(rawJSON, rubric)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GoalAlignment != 4.8 {
		t.Errorf("expected GoalAlignment 4.8, got %f", resp.GoalAlignment)
	}
	if resp.FactualGrounding != 4.5 {
		t.Errorf("expected FactualGrounding 4.5, got %f", resp.FactualGrounding)
	}
	if resp.Coherence != 5.0 {
		t.Errorf("expected Coherence 5.0, got %f", resp.Coherence)
	}
	if resp.Completeness != 4.0 {
		t.Errorf("expected Completeness 4.0, got %f", resp.Completeness)
	}
	if resp.OverallScore != 4.4 {
		t.Errorf("expected OverallScore 4.4, got %f", resp.OverallScore)
	}
}

