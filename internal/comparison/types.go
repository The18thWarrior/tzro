package comparison

import "tzro/internal/inference"

// Condition IDs for the 2×2 execution matrix.
const (
	ConditionCloudReAct  = "cloud_react"
	ConditionCloudDAGRaw = "cloud_dag_raw"
	ConditionCloudDAG    = "cloud_dag"
	ConditionLocalOnly   = "local_only"
	ConditionCooperative = "cooperative"
)

// AllConditions returns the canonical ordered list of condition IDs.
func AllConditions() []string {
	return []string{ConditionCloudReAct, ConditionCloudDAGRaw, ConditionCloudDAG, ConditionLocalOnly, ConditionCooperative}
}

// RubricCriterion defines a single quality evaluation dimension.
type RubricCriterion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// QualityRubric defines the set of criteria for LLM-as-judge scoring.
type QualityRubric struct {
	Criteria []RubricCriterion `json:"criteria"`
	MaxScore float64           `json:"maxScore"`
}

// ComparisonTask defines a single documentation generation task.
type ComparisonTask struct {
	ID            string        `json:"id"`
	Tier          int           `json:"tier"`
	Prompt        string        `json:"prompt"`
	TargetPaths   []string      `json:"targetPaths"`
	QualityRubric QualityRubric `json:"qualityRubric"`
}

// ComparisonResult captures metrics for one condition × task execution run.
type ComparisonResult struct {
	TaskID        string               `json:"taskId"`
	TaskTier      int                  `json:"taskTier"`
	Condition     string               `json:"condition"`
	CloudTokens   inference.TokenUsage `json:"cloudTokens"`
	LocalTokens   inference.TokenUsage `json:"localTokens"`
	WallClockMs   int64                `json:"wallClockMs"`
	EstCostUSD    float64              `json:"estCostUSD"`
	ToolCallCount int                  `json:"toolCallCount"`
	OutputText    string               `json:"outputText"`
	QualityScore  float64              `json:"qualityScore"`
	QualityNotes  string               `json:"qualityNotes"`
	Error         string               `json:"error,omitempty"`
	Logs          string               `json:"logs,omitempty"`
}

// PricingTable holds per-1K-token pricing for cloud model inference.
// Local tokens always cost $0.00.
type PricingTable struct {
	PromptPer1KTokens     float64
	CompletionPer1KTokens float64
}

// DefaultPricing returns pricing based on Claude 3.5 Sonnet rates.
func DefaultPricing() PricingTable {
	return PricingTable{
		PromptPer1KTokens:     0.005,
		CompletionPer1KTokens: 0.030,
	}
}
