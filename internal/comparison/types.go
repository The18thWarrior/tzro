package comparison

import "tzro/internal/inference"

// Condition IDs for the 2×2 execution matrix.
const (
	ConditionCloudReAct  = "cloud_react"
	ConditionCloudDAGRaw = "cloud_dag_raw"
	ConditionCloudDAG    = "cloud_dag"
	ConditionLocalOnly   = "local_only"
	ConditionCooperative = "cooperative"
	ConditionTzroCode  = "tzro_code"  // Static 3-node DAG via codegen package (cooperative mode)
	ConditionCloudCode = "cloud_code" // Static 3-node DAG via codegen package (cloud mode)
	ConditionTzroDraft = "tzro_draft" // Local draft (no self-repair) + frontier fix (cooperative evaluation)
)

// Task category constants.
const (
	CategoryAll     = ""        // Run both docgen and codegen
	CategoryDocgen  = "docgen"
	CategoryCodegen = "codegen"
)

// AllConditions returns the canonical ordered list of condition IDs
// for documentation generation benchmarks.
func AllConditions() []string {
	return []string{ConditionCloudReAct, ConditionCloudDAGRaw, ConditionCloudDAG, ConditionLocalOnly, ConditionCooperative}
}

// CodegenConditions returns all conditions applicable to code generation benchmarks.
// Use CodegenConditionsForTier for tier-aware routing in production runs.
func CodegenConditions() []string {
	return []string{ConditionCloudCode, ConditionTzroCode, ConditionTzroDraft}
}

// CodegenConditionsForTier returns the conditions to run for a given task tier.
// T1 (simple) tasks run all three conditions for continued local-only evaluation.
// T2+ tasks drop tzro_code in favor of tzro_draft: benchmark #9 showed tzro_code
// compiles only 20% of T2+ tasks (avg Q=2.67) while tzro_draft achieves 90%
// compilation (avg Q=4.10) at 63% cost savings vs cloud_code.
func CodegenConditionsForTier(tier int) []string {
	if tier <= 1 {
		return []string{ConditionCloudCode, ConditionTzroCode, ConditionTzroDraft}
	}
	return []string{ConditionCloudCode, ConditionTzroDraft}
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

// ComparisonTask defines a single benchmark task for comparison evaluation.
// The Category field determines whether the task is a documentation generation
// task ("docgen") or a code generation task ("codegen").
type ComparisonTask struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"` // "docgen" or "codegen"
	Tier        int      `json:"tier"`
	Prompt      string   `json:"prompt"`
	TargetPaths []string `json:"targetPaths,omitempty"`
	// Code-generation specific fields (category=codegen)
	Spec          string        `json:"spec,omitempty"`     // Specification for tzro_code
	Filepath      string        `json:"filepath,omitempty"` // Target file path for code generation
	Language      string        `json:"language,omitempty"` // Language hint (e.g. "go", "typescript")
	Action        string        `json:"action,omitempty"`   // "create" or "update"
	SeedFile      string        `json:"seedFile,omitempty"` // Relative path in testdata/codegen_seeds/
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
	DraftText     string               `json:"draftText,omitempty"`  // Raw local draft before frontier fix (tzro_draft only)
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
