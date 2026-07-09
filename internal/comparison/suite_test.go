package comparison

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunComparisonSuite_RunsSingleConditionForFilteredTier(t *testing.T) {
	// Create a temp dir with a test file for the ReAct loop to read
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "results")

	testFile := filepath.Join(tmpDir, "hello.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Hello() string {\n\treturn \"world\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origDir) }()

	finalContent := "# Function Index\n\n- `Hello() string` — returns \"world\""

	// Mock ReAct server: first call = tool_call, second call = final text
	var reactCallIdx int64
	reactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt64(&reactCallIdx, 1) - 1)
		w.Header().Set("Content-Type", "application/json")

		if idx == 0 {
			// Return tool call
			resp := reactCompletionResponse{
				Choices: []struct {
					Message struct {
						Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
						ToolCalls []reactToolCall `json:"tool_calls,omitempty"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{
					{
						Message: struct {
							Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
							ToolCalls []reactToolCall `json:"tool_calls,omitempty"`
						}{
							ToolCalls: []reactToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: struct {
										Name      string `json:"name"`
										Arguments string `json:"arguments"`
									}{
										Name:      "read_file",
										Arguments: fmt.Sprintf(`{"path": "%s"}`, testFile),
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
				Usage: struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				}{PromptTokens: 100, CompletionTokens: 20},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			// Return final text
			resp := reactCompletionResponse{
				Choices: []struct {
					Message struct {
						Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
						ToolCalls []reactToolCall `json:"tool_calls,omitempty"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{
					{
						Message: struct {
							Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
							ToolCalls []reactToolCall `json:"tool_calls,omitempty"`
						}{
							Content: &finalContent,
						},
						FinishReason: "stop",
					},
				},
				Usage: struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				}{PromptTokens: 200, CompletionTokens: 50},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer reactServer.Close()

	// Mock judge server
	judgeResp := JudgeResponse{
		Criteria: []JudgeCriterionScore{
			{Name: "Completeness", Score: 4, Reasoning: "Good"},
			{Name: "Accuracy", Score: 5, Reasoning: "Excellent"},
			{Name: "Structure", Score: 4, Reasoning: "Clear"},
			{Name: "Usefulness", Score: 4, Reasoning: "Helpful"},
		},
		OverallScore: 4.25,
		Summary:      "Solid documentation",
	}
	judgeJSON, _ := json.Marshal(judgeResp)
	judgeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": string(judgeJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer judgeServer.Close()

	// Track callbacks
	var startedTasks []string
	var completedResults []ComparisonResult

	callbacks := &SuiteCallbacks{
		OnTaskStart: func(taskID, conditionID string) {
			startedTasks = append(startedTasks, fmt.Sprintf("%s/%s", taskID, conditionID))
		},
		OnTaskComplete: func(result ComparisonResult) {
			completedResults = append(completedResults, result)
		},
	}

	opts := SuiteOptions{
		Category:      CategoryDocgen,      // Pin to docgen (default is now both categories)
		Tier:          1,                   // Only T1
		Condition:     ConditionCloudReAct, // Only the ReAct condition (no daemon needed)
		OutputDir:     outputDir,
		Pricing:       DefaultPricing(),
		ReactEndpoint: reactServer.URL,
		JudgeEndpoint: judgeServer.URL,
	}

	results, err := RunComparisonSuite(t.Context(), opts, callbacks)
	if err != nil {
		t.Fatalf("RunComparisonSuite failed: %v", err)
	}

	// Should have exactly 1 result (T1 × cloud_react)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	r := results[0]
	if r.TaskID != "cache_function_index" {
		t.Errorf("TaskID = %q, want %q", r.TaskID, "cache_function_index")
	}
	if r.Condition != ConditionCloudReAct {
		t.Errorf("Condition = %q, want %q", r.Condition, ConditionCloudReAct)
	}
	if r.QualityScore != 4.25 {
		t.Errorf("QualityScore = %f, want 4.25", r.QualityScore)
	}
	if r.OutputText == "" {
		t.Error("OutputText should not be empty")
	}

	// Verify callbacks fired
	if len(startedTasks) != 1 {
		t.Errorf("OnTaskStart called %d times, want 1", len(startedTasks))
	}
	if len(completedResults) != 1 {
		t.Errorf("OnTaskComplete called %d times, want 1", len(completedResults))
	}

	// Verify report files generated
	datestamp := time.Now().Format("2006-01-02")
	jsonPath := filepath.Join(outputDir, "comparison_results_"+datestamp+".json")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Error("JSON results file not created")
	}
	mdPath := filepath.Join(outputDir, "comparison_report_"+datestamp+".md")
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		t.Error("Markdown report file not created")
	}
}
