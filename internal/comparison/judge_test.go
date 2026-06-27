package comparison

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJudgeOutput_ParsesStructuredScores(t *testing.T) {
	judgeResp := JudgeResponse{
		Criteria: []JudgeCriterionScore{
			{Name: "Completeness", Score: 4, Reasoning: "Covers most functions"},
			{Name: "Accuracy", Score: 5, Reasoning: "All signatures match"},
			{Name: "Structure", Score: 4, Reasoning: "Well organized"},
			{Name: "Usefulness", Score: 4, Reasoning: "Helpful for developers"},
		},
		OverallScore: 4.25,
		Summary:      "Good documentation with minor gaps in completeness",
	}

	judgeJSON, _ := json.Marshal(judgeResp)

	// Mock server returns the judge response wrapped in OpenAI format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": string(judgeJSON),
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{
			{Name: "Completeness", Description: "Covers all exported functions"},
			{Name: "Accuracy", Description: "Signatures match source code"},
			{Name: "Structure", Description: "Well-organized, easy to scan"},
			{Name: "Usefulness", Description: "Developer would find this helpful"},
		},
		MaxScore: 5.0,
	}

	score, notes, err := JudgeOutputWithEndpoint(t.Context(), "# Function Index\n\n- Hello() string", rubric, server.URL)
	if err != nil {
		t.Fatalf("JudgeOutput failed: %v", err)
	}

	if math.Abs(score-4.25) > 1e-9 {
		t.Errorf("score = %f, want 4.25", score)
	}

	if notes != "Good documentation with minor gaps in completeness" {
		t.Errorf("notes = %q, want %q", notes, "Good documentation with minor gaps in completeness")
	}
}

func TestJudgeOutput_HandlesInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": "not valid json",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{
			{Name: "Completeness", Description: "test"},
		},
		MaxScore: 5.0,
	}

	_, _, err := JudgeOutputWithEndpoint(t.Context(), "test doc", rubric, server.URL)
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestJudgeOutput_HandlesCodeFencedJSON(t *testing.T) {
	// Simulate a model wrapping valid JSON in markdown code fences
	fencedJSON := "```json\n" + `{
  "criteria": [
    {"name": "Completeness", "score": 4, "reasoning": "Good"},
    {"name": "Accuracy", "score": 5, "reasoning": "Accurate"}
  ],
  "overallScore": 4.5,
  "summary": "Strong documentation"
}` + "\n```"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": fencedJSON,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{
			{Name: "Completeness", Description: "test"},
			{Name: "Accuracy", Description: "test"},
		},
		MaxScore: 5.0,
	}

	score, notes, err := JudgeOutputWithEndpoint(t.Context(), "# Test Doc", rubric, server.URL)
	if err != nil {
		t.Fatalf("JudgeOutput failed on code-fenced response: %v", err)
	}

	if math.Abs(score-4.5) > 1e-9 {
		t.Errorf("score = %f, want 4.5", score)
	}

	if notes != "Strong documentation" {
		t.Errorf("notes = %q, want %q", notes, "Strong documentation")
	}
}

func TestJudgeOutput_HandlesFlatCriterionMap(t *testing.T) {
	// Simulate a model returning a flat map instead of the structured format
	flatJSON := `{"completeness": 5, "accuracy": 4, "structure": 5, "usefulness": 4}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": flatJSON,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	rubric := QualityRubric{
		Criteria: []RubricCriterion{
			{Name: "completeness", Description: "Covers all exported functions"},
			{Name: "accuracy", Description: "Signatures match source code"},
			{Name: "structure", Description: "Well-organized"},
			{Name: "usefulness", Description: "Developer would find this helpful"},
		},
		MaxScore: 5.0,
	}

	score, notes, err := JudgeOutputWithEndpoint(t.Context(), "# Test Doc", rubric, server.URL)
	if err != nil {
		t.Fatalf("JudgeOutput failed on flat criterion map: %v", err)
	}

	// Mean of 5+4+5+4 = 4.5
	if math.Abs(score-4.5) > 1e-9 {
		t.Errorf("score = %f, want 4.5", score)
	}

	if notes == "" {
		t.Error("expected non-empty notes from flat response fallback")
	}
}
