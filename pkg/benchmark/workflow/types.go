// Package workflow compares native agent installation experiences.
package workflow

import (
	"encoding/json"
	"time"
)

type Profile string

const (
	Baseline      Profile = "baseline"
	Standard      Profile = "standard"
	Full          Profile = "full"
	RecipeVersion         = "tzro.installation-profiles.v2"
)

type Task struct {
	ID            string
	Prompt        string
	Files         map[string]string
	GradeFiles    map[string]string
	ReadOnlyFiles []string
}

type Config struct {
	TzroBinary, Installer, PiBinary, WorkDir string
	Model, BaseURL, APIKey                   string
	Suite                                    string
	Profiles                                 []Profile
	Tasks                                    []Task
	SetupTimeout                             time.Duration
	Full                                     FullConfig
	Run                                      bool
	Timeout                                  time.Duration
	MaxTurns                                 int
	Repeats                                  int
	MaxCost                                  float64
	Prices                                   *Prices
}

type Prices struct {
	Input      float64 `json:"input_per_million"`
	Output     float64 `json:"output_per_million"`
	CacheRead  float64 `json:"cache_read_per_million"`
	CacheWrite float64 `json:"cache_write_per_million"`
}

type FullConfig struct {
	DecisionBin, DecisionModel, DecisionVersion    string
	ExtractorBin, ExtractorModel, ExtractorVersion string
	ExtractorArgs                                  []string
}

type Usage struct {
	Requests         int      `json:"requests"`
	Input            int      `json:"input"`
	Output           int      `json:"output"`
	CacheRead        int      `json:"cache_read"`
	CacheWrite       int      `json:"cache_write"`
	Complete         bool     `json:"complete"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`
}

type Turn struct {
	Number     int    `json:"number"`
	StopReason string `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
}

type ToolCall struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Completed   bool            `json:"completed"`
	IsError     bool            `json:"is_error"`
	DurationMS  int64           `json:"duration_ms"`
	OutputBytes int             `json:"output_bytes"`
}

type Result struct {
	Profile          Profile          `json:"profile"`
	Repeat           int              `json:"repeat"`
	Task             string           `json:"task"`
	Home             string           `json:"home"`
	Workspace        string           `json:"workspace"`
	Status           string           `json:"status"`
	Error            string           `json:"error,omitempty"`
	SkillLoaded      bool             `json:"skill_loaded"`
	Hooks            string           `json:"hooks"`
	MCP              string           `json:"mcp"`
	SetupMS          int64            `json:"setup_ms"`
	PreflightMS      int64            `json:"preflight_ms"`
	Usage            Usage            `json:"usage"`
	RuntimeReady     bool             `json:"runtime_ready"`
	TaskSuccess      bool             `json:"task_success"`
	AgentMS          int64            `json:"agent_ms"`
	GradeMS          int64            `json:"grade_ms"`
	FinalResponse    string           `json:"final_response,omitempty"`
	ToolCalls        int              `json:"tool_calls"`
	ToolErrors       int              `json:"tool_errors"`
	AgentEnded       bool             `json:"agent_ended"`
	Activity         []map[string]any `json:"activity"`
	PromptSHA256     string           `json:"prompt_sha256"`
	FixtureSHA256    string           `json:"fixture_sha256"`
	GradingSHA256    string           `json:"grading_sha256"`
	SkillSHA256      string           `json:"skill_sha256,omitempty"`
	HookSHA256       string           `json:"hook_sha256,omitempty"`
	ProxyRequests    int              `json:"proxy_requests"`
	SkillRead        bool             `json:"skill_read"`
	TracePath        string           `json:"trace_path,omitempty"`
	TraceSHA256      string           `json:"trace_sha256,omitempty"`
	EvidenceComplete bool             `json:"evidence_complete"`
	Turns            []Turn           `json:"turns,omitempty"`
	Tools            []ToolCall       `json:"tools,omitempty"`
}

type Report struct {
	Schema        string         `json:"schema"`
	ClientVersion string         `json:"client_version"`
	Ready         bool           `json:"ready"`
	Metadata      map[string]any `json:"metadata"`
	Results       []Result       `json:"results"`
}

type prepared struct {
	result Result
	env    []string
	binary string
}
