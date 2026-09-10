package signaldensity

import (
	"time"
)

// Tier constants.
const (
	TierAll   = "all"
	TierMicro = "micro"
	TierMacro = "macro"
)

// Primitive constants.
const (
	PrimitiveAll       = "all"
	PrimitiveSkeleton  = "skeleton"
	PrimitiveCompactor = "compactor"
	PrimitiveJSON      = "json"
	PrimitiveTabular   = "tabular"
	PrimitiveMacro     = "macro"
)

// Battery names matching the design spec.
const (
	BatteryASTSkeleton   = "ast_skeleton"
	BatteryCompactorLogs = "compactor_logs"
	BatterySmartJSON     = "smart_json"
	BatteryTabularSQL    = "tabular_sql"
	BatteryMiniMacro     = "mini_macro"
)

// Matcher type constants.
const (
	MatcherExact    = "exact"
	MatcherContains = "contains"
	MatcherRegex    = "regex"
	MatcherJSON     = "json"
	MatcherGoTest   = "go_test"
)

// TaskCase defines a single evaluation task run across Baseline (R) and Tzro (T).
type TaskCase struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Tier        string            `json:"tier"`      // micro | macro
	Battery     string            `json:"battery"`   // ast_skeleton | compactor_logs | smart_json | tabular_sql | mini_macro
	Primitive   string            `json:"primitive"` // skeleton | compactor | json | tabular | macro
	Description string            `json:"description"`
	PromptRaw   string            `json:"prompt_raw"`
	PromptTzro  string            `json:"prompt_tzro"`
	MatcherType string            `json:"matcher_type"` // exact | contains | regex | json | go_test
	Expected    string            `json:"expected"`
	Scaffold    map[string]string `json:"scaffold,omitempty"`
}

// CaseResult holds the evaluation outcome of a single task.
type CaseResult struct {
	TaskID               string  `json:"task_id"`
	Name                 string  `json:"name"`
	Battery              string  `json:"battery"`
	Tier                 string  `json:"tier"`
	RawPromptTokens      int     `json:"raw_prompt_tokens"`
	TzroPromptTokens     int     `json:"tzro_prompt_tokens"`
	RawCompletionTokens  int     `json:"raw_completion_tokens"`
	TzroCompletionTokens int     `json:"tzro_completion_tokens"`
	RawPassed            bool    `json:"raw_passed"`
	TzroPassed           bool    `json:"tzro_passed"`
	RawCostUSD           float64 `json:"raw_cost_usd"`
	TzroCostUSD          float64 `json:"tzro_cost_usd"`
	RawResponse          string  `json:"raw_response,omitempty"`
	TzroResponse         string  `json:"tzro_response,omitempty"`
	Turns                int     `json:"turns,omitempty"`
	Error                string  `json:"error,omitempty"`
}

// BatteryResult aggregates metrics for an optimization primitive battery.
type BatteryResult struct {
	Name             string       `json:"name"`
	RawTokens        int          `json:"raw_tokens"`
	TzroTokens       int          `json:"tzro_tokens"`
	AccuracyRaw      float64      `json:"accuracy_raw"`
	AccuracyTzro     float64      `json:"accuracy_tzro"`
	SignalRetention  float64      `json:"signal_retention"`  // SR = sum(A_T) / sum(A_R)
	CompressionRatio float64      `json:"compression_ratio"` // CR = sum(K_R) / sum(K_T)
	SDM              float64      `json:"sdm"`               // SDM = SR * CR
	Cases            []CaseResult `json:"cases"`
}

// BenchmarkSummary represents the overall high-level metrics across all executed batteries.
type BenchmarkSummary struct {
	RawTokens           int     `json:"raw_tokens"`
	TzroTokens          int     `json:"tzro_tokens"`
	OverallAccuracyRaw  float64 `json:"overall_accuracy_raw"`
	OverallAccuracyTzro float64 `json:"overall_accuracy_tzro"`
	CompressionRatio    float64 `json:"compression_ratio"`
	SignalRetention     float64 `json:"signal_retention"`
	CompositeSDM        float64 `json:"composite_sdm"`
}

// BenchmarkMetadata captures run parameters and execution metadata.
type BenchmarkMetadata struct {
	Model           string  `json:"model"`
	Timestamp       string  `json:"timestamp"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	CompositeSDM    float64 `json:"composite_sdm"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// BenchmarkReport is the root schema output matching Section 5.2.
type BenchmarkReport struct {
	Schema    string            `json:"$schema"`
	Metadata  BenchmarkMetadata `json:"metadata"`
	Summary   BenchmarkSummary  `json:"summary"`
	Batteries []BatteryResult   `json:"batteries"`
}

// BenchmarkConfig specifies configuration options for running the benchmark suite.
type BenchmarkConfig struct {
	Model      string
	Tier       string
	Primitive  string
	MaxCost    float64
	Timeout    time.Duration
	OutputPath string
	NoCache    bool
	Samples    int
	BaseURL    string
	APIKey     string
	StorePath  string
}

// ModelPricing represents pricing per million tokens in USD.
type ModelPricing struct {
	PromptPerM     float64
	CompletionPerM float64
}

// DefaultPricingMap contains standard token pricing per 1M tokens.
var DefaultPricingMap = map[string]ModelPricing{
	"anthropic/claude-3.5-sonnet": {PromptPerM: 3.00, CompletionPerM: 15.00},
	"claude-3-5-sonnet-20241022":  {PromptPerM: 3.00, CompletionPerM: 15.00},
	"openai/gpt-4o":               {PromptPerM: 2.50, CompletionPerM: 10.00},
	"gpt-4o":                      {PromptPerM: 2.50, CompletionPerM: 10.00},
	"openai/gpt-4o-mini":          {PromptPerM: 0.15, CompletionPerM: 0.60},
	"gpt-4o-mini":                 {PromptPerM: 0.15, CompletionPerM: 0.60},
	"google/gemini-2.0-flash":     {PromptPerM: 0.10, CompletionPerM: 0.40},
}

// FallbackPricing is used when model rates are unspecified.
var FallbackPricing = ModelPricing{PromptPerM: 2.50, CompletionPerM: 10.00}
