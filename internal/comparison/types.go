package comparison

import "tzro/internal/inference"

// Condition IDs for the 2×2 execution matrix.
const (
	ConditionCloudReAct  = "cloud_react"
	ConditionLocalReAct  = "local_react" // ReAct loop on local model (DAG-free baseline)
	ConditionCloudDAGRaw = "cloud_dag_raw"
	ConditionCloudDAG    = "cloud_dag"
	ConditionLocalOnly   = "local_only"
	ConditionCooperative = "cooperative"
	ConditionTzroCode    = "tzro_code"  // Unified codegen: direct (simple) or draft+fix (complex)
	ConditionCloudCode   = "cloud_code" // Static 3-node DAG via codegen package (cloud mode)
)

// Task category constants.
const (
	CategoryAll      = "" // Run all categories
	CategoryDocgen   = "docgen"
	CategoryCodegen  = "codegen"
	CategoryDatanal  = "datanal"
	CategoryResearch = "research"
)

// AllConditions returns the canonical ordered list of condition IDs
// for documentation generation benchmarks.
func AllConditions() []string {
	return []string{ConditionCloudReAct, ConditionLocalReAct, ConditionCloudDAGRaw, ConditionCloudDAG, ConditionLocalOnly, ConditionCooperative}
}

// CodegenConditions returns all conditions applicable to code generation benchmarks.
func CodegenConditions() []string {
	return []string{ConditionCloudCode, ConditionTzroCode}
}

// DatanalConditions returns all conditions applicable to data analysis benchmarks.
func DatanalConditions() []string {
	return []string{ConditionCloudReAct, ConditionCloudDAGRaw, ConditionCloudDAG, ConditionLocalOnly, ConditionCooperative}
}

// ResearchConditions returns all conditions applicable to web research benchmarks.
func ResearchConditions() []string {
	return []string{ConditionCloudReAct, ConditionCloudDAGRaw, ConditionCloudDAG, ConditionLocalOnly, ConditionCooperative}
}

// CodegenConditionsForTier returns the conditions to run for a given task tier.
// All tiers run the same two conditions. The tzro_code condition internally
// routes via the complexity gate: simple tasks use direct local codegen,
// complex tasks use the draft+cloud-fix pipeline (formerly tzro_draft).
func CodegenConditionsForTier(tier int) []string {
	return []string{ConditionCloudCode, ConditionTzroCode}
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
	Tags        []string `json:"tags,omitempty"` // Metadata for report slicing (e.g. "novel", "holdout")
	// Code-generation specific fields (category=codegen)
	Spec           string        `json:"spec,omitempty"`     // Specification for tzro_code
	Filepath       string        `json:"filepath,omitempty"` // Target file path for code generation
	Language       string        `json:"language,omitempty"` // Language hint (e.g. "go", "typescript")
	Action         string        `json:"action,omitempty"`   // "create" or "update"
	SeedFile       string        `json:"seedFile,omitempty"` // Relative path in testdata/codegen_seeds/
	QualityRubric  QualityRubric `json:"qualityRubric"`
	ExpectedAnswer string        `json:"expectedAnswer,omitempty"` // Pre-computed ground truth for data analysis tasks
}

// JudgeOptions configures the LLM-as-judge evaluation.
type JudgeOptions struct {
	Endpoint string // Override judge API endpoint (for testing)
	Category string // Task category for prompt selection
	Model    string // OpenRouter model ID (e.g. "anthropic/claude-sonnet-4"); empty = default Gemini
}

// SuiteOptions configures a comparison benchmark run.
type SuiteOptions struct {
	Category      string       // "" = both docgen and codegen; or "docgen" / "codegen" for single category
	Tier          int          // 0 = all tiers, 1-5 = specific tier
	Condition     string       // "" = all conditions, or specific condition ID
	TaskID        string       // "" = all tasks, or specific task ID
	OutputDir     string       // Directory to write results
	Pricing       PricingTable // Cloud model pricing
	JudgeEndpoint string       // Override judge API endpoint (for testing)
	ReactEndpoint string       // Override ReAct API endpoint (for testing)
	JudgeModel    string       // OpenRouter model ID for judging; empty = default Gemini
	Holdout       bool         // Load holdout tasks instead of development set
}

// ComparisonResult captures metrics for one condition × task execution run.
type ComparisonResult struct {
	TaskID        string               `json:"taskId"`
	TaskTier      int                  `json:"taskTier"`
	Holdout       bool                 `json:"holdout,omitempty"`
	Condition     string               `json:"condition"`
	CloudTokens   inference.TokenUsage `json:"cloudTokens"`
	LocalTokens   inference.TokenUsage `json:"localTokens"`
	WallClockMs   int64                `json:"wallClockMs"`
	EstCostUSD    float64              `json:"estCostUSD"`
	ToolCallCount int                  `json:"toolCallCount"`
	OutputText    string               `json:"outputText"`
	DraftText     string               `json:"draftText,omitempty"` // Raw local draft before cloud fix (populated when draft mode activates)
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
