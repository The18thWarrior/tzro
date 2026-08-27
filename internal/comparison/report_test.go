package comparison

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/internal/inference"
)

func TestGenerateReport_ProducesJSONAndMarkdown(t *testing.T) {
	tmpDir := t.TempDir()

	results := []ComparisonResult{
		{
			TaskID:        "cache_function_index",
			TaskTier:      1,
			Condition:     ConditionCloudReAct,
			CloudTokens:   inference.TokenUsage{PromptTokens: 5000, CompletionTokens: 1000, TotalTokens: 6000},
			WallClockMs:   3000,
			EstCostUSD:    0.030,
			ToolCallCount: 5,
			OutputText:    "# Function Index...",
			QualityScore:  4.0,
		},
		{
			TaskID:        "cache_function_index",
			TaskTier:      1,
			Condition:     ConditionCloudDAGRaw,
			CloudTokens:   inference.TokenUsage{PromptTokens: 3500, CompletionTokens: 800, TotalTokens: 4300},
			LocalTokens:   inference.TokenUsage{TotalTokens: 500},
			WallClockMs:   2500,
			EstCostUSD:    0.020,
			ToolCallCount: 4,
			OutputText:    "# Function Index...",
			QualityScore:  3.9,
		},
		{
			TaskID:        "cache_function_index",
			TaskTier:      1,
			Condition:     ConditionCloudDAG,
			CloudTokens:   inference.TokenUsage{PromptTokens: 2000, CompletionTokens: 500, TotalTokens: 2500},
			LocalTokens:   inference.TokenUsage{TotalTokens: 1000},
			WallClockMs:   2000,
			EstCostUSD:    0.012,
			ToolCallCount: 3,
			OutputText:    "# Function Index...",
			QualityScore:  4.2,
		},
		{
			TaskID:        "cache_function_index",
			TaskTier:      1,
			Condition:     ConditionLocalOnly,
			CloudTokens:   inference.TokenUsage{TotalTokens: 0},
			LocalTokens:   inference.TokenUsage{TotalTokens: 8000},
			WallClockMs:   5000,
			EstCostUSD:    0.0,
			ToolCallCount: 5,
			OutputText:    "# Function Index...",
			QualityScore:  3.5,
		},
		{
			TaskID:        "cache_function_index",
			TaskTier:      1,
			Condition:     ConditionCooperative,
			CloudTokens:   inference.TokenUsage{PromptTokens: 500, CompletionTokens: 200, TotalTokens: 700},
			LocalTokens:   inference.TokenUsage{TotalTokens: 5000},
			WallClockMs:   2500,
			EstCostUSD:    0.004,
			ToolCallCount: 5,
			OutputText:    "# Function Index...",
			QualityScore:  4.1,
		},
	}

	if err := GenerateReport(results, tmpDir); err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}

	datestamp := time.Now().Format("2006-01-02")

	// Verify JSON file exists and deserializes
	jsonPath := filepath.Join(tmpDir, "comparison_results_"+datestamp+".json")
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("JSON file not found: %v", err)
	}

	var parsedResults []ComparisonResult
	if err := json.Unmarshal(jsonData, &parsedResults); err != nil {
		t.Fatalf("JSON deserialization failed: %v", err)
	}
	if len(parsedResults) != 5 {
		t.Errorf("JSON has %d results, want 5", len(parsedResults))
	}

	// Verify Markdown file exists and contains expected sections
	mdPath := filepath.Join(tmpDir, "comparison_report_"+datestamp+".md")
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("Markdown file not found: %v", err)
	}

	mdContent := string(mdData)
	expectedSections := []string{
		"Executive Summary",
		"Per-Task Comparison Table",
		"Scaling Analysis",
		"Savings Attribution",
		"Quality Comparison",
		"Deterministic Evaluation Summary",
		"Methodology",
		"Raw Data",
	}

	for _, section := range expectedSections {
		if !strings.Contains(mdContent, section) {
			t.Errorf("Markdown missing section: %q", section)
		}
	}

	// Verify three-bucket attribution table contains all bucket labels
	if !strings.Contains(mdContent, "DAG Structure") {
		t.Error("Markdown missing savings bucket: DAG Structure")
	}
	if !strings.Contains(mdContent, "Pipeline Compaction") {
		t.Error("Markdown missing savings bucket: Pipeline Compaction")
	}
	if !strings.Contains(mdContent, "Local Offloading") {
		t.Error("Markdown missing savings bucket: Local Offloading")
	}
}

func TestReportRendersERRForJudgeFailure(t *testing.T) {
	results := []ComparisonResult{
		{
			TaskID:       "test_task",
			TaskTier:     1,
			Condition:    ConditionCooperative,
			QualityScore: 4.5,
			OutputText:   "good output",
		},
		{
			TaskID:       "test_task",
			TaskTier:     1,
			Condition:    ConditionCloudReAct,
			QualityScore: -1,
			JudgeError:   "ERR_JUDGE_UNAVAILABLE",
			OutputText:   "output exists but judge failed",
		},
		{
			TaskID:       "test_task_2",
			TaskTier:     2,
			Condition:    ConditionCooperative,
			QualityScore: 3.5,
			OutputText:   "another output",
		},
	}

	datestamp := time.Now().Format("2006-01-02")
	md := generateMarkdown(results, datestamp, "results.json")

	// Verify ERR appears in the markdown for the judge-failed result
	if !strings.Contains(md, "ERR") {
		t.Error("expected ERR in markdown for judge-failed result")
	}

	// Verify the valid scores still appear
	if !strings.Contains(md, "4.50") {
		t.Error("expected 4.50 quality score for valid result")
	}
	if !strings.Contains(md, "3.50") {
		t.Error("expected 3.50 quality score for valid result")
	}
}

func TestAvgQualityScore_ExcludesJudgeErrors(t *testing.T) {
	results := []ComparisonResult{
		{QualityScore: 4.0},
		{QualityScore: -1, JudgeError: "ERR_JUDGE_UNAVAILABLE"},
		{QualityScore: 3.0},
		{QualityScore: 0, Error: "execution failed"},
	}

	avg := avgQualityScore(results)
	// Should only average 4.0 and 3.0 (the two valid results)
	expected := 3.5
	if avg != expected {
		t.Errorf("expected avg %.1f, got %.1f (should exclude judge errors and execution errors)", expected, avg)
	}
}

func TestAvgQualityScore_AllJudgeErrors(t *testing.T) {
	results := []ComparisonResult{
		{QualityScore: -1, JudgeError: "ERR_JUDGE_UNAVAILABLE"},
		{QualityScore: -1, JudgeError: "ERR_JUDGE_UNAVAILABLE"},
	}

	avg := avgQualityScore(results)
	if avg != 0 {
		t.Errorf("expected avg 0 when all results have judge errors, got %f", avg)
	}
}
