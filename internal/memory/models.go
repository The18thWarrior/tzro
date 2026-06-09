package memory

import "time"

type FactMemory struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	Type       string    `json:"type"` // "fact" | "preference" | "insight" | "correction" | "anti_pattern" | "strategy"
	Content    string    `json:"content"`
	Context    string    `json:"context"`
	Confidence float64   `json:"confidence"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"createdAt"`
	Embedding  []float32 `json:"embedding,omitempty"`
}

type KGNode struct {
	ID        string                 `json:"id"`
	NodeType  string                 `json:"nodeType"` // "account" | "contact" | "ticket" | "document"
	Name      string                 `json:"name"`
	Metadata  map[string]interface{} `json:"metadata"`
	Source    string                 `json:"source"`
	Weight    float64                `json:"weight"`
	Embedding []float32              `json:"embedding,omitempty"`
}

type KGEdge struct {
	ID       string                 `json:"id"`
	EdgeType string                 `json:"edgeType"` // "belongs_to" | "assigned_to" | "references"
	SourceID string                 `json:"sourceId"`
	TargetID string                 `json:"targetId"`
	Metadata map[string]interface{} `json:"metadata"`
	Weight   float64                `json:"weight"`
}

type KGSubGraph struct {
	Nodes []KGNode `json:"nodes"`
	Edges []KGEdge `json:"edges"`
}

type NodeState struct {
	TaskID      string `json:"taskId"`
	NodeID      string `json:"nodeId"`
	Status      string `json:"status"` // "pending" | "running" | "completed" | "failed" | "skipped"
	Output      string `json:"output"`
	RawOutput   string `json:"rawOutput,omitempty"` // Clean tool output for interpolation (no tier prefix, no compaction)
	CompletedAt int64  `json:"completedAt"`
}

type Skill struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	TriggerDescription string `json:"triggerDescription"`
	SOPContent         string `json:"sopContent"`
	CreatedAt          int64  `json:"createdAt"`
}

type EntityType struct {
	ID      string `json:"id"`      // Machine key used in KGNode.NodeType (e.g. "contact")
	Label   string `json:"label"`   // Human-readable display name (e.g. "Contact")
	Color   string `json:"color"`   // CSS HSL color string for canvas rendering
	Icon    string `json:"icon"`    // Optional icon hint (e.g. "user", "building", "tag")
	BuiltIn bool   `json:"builtIn"` // true for default types that cannot be deleted
}

type WorkflowDefinition struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	TriggerType       string `json:"triggerType"`       // "cron" | "manual" | "background"
	TriggerConfig     string `json:"triggerConfig"`     // cron expression
	Status            string `json:"status"`            // "active" | "paused"
	NextRunAt         int64  `json:"nextRunAt"`         // unix timestamp
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
	OrchestrationMode string `json:"orchestrationMode"` // "static" | "dynamic"
	Goal              string `json:"goal"`              // natural language objective (dynamic mode)
	ApprovedLevel     int    `json:"approvedLevel"`     // Proactivity Ladder ceiling (0-4)
	MaxTokens         int    `json:"maxTokens"`         // token budget for dynamic workflows
	MaxToolCalls      int    `json:"maxToolCalls"`      // tool call budget for dynamic workflows
	SpawnedBy         string `json:"spawnedBy"`         // BackgroundAgent name, empty for user-spawned
}

type WorkflowTask struct {
	WorkflowID     string `json:"workflowId"`
	TaskTemplateID string `json:"taskTemplateId"`
	Name           string `json:"name"`
	Instructions   string `json:"instructions"`
	Dependencies   string `json:"dependencies"` // comma-separated taskTemplateIds
}

type WorkflowExecution struct {
	ID                string `json:"id"`
	WorkflowID        string `json:"workflowId"`
	Status            string `json:"status"` // "running" | "completed" | "failed" | "cancelled"
	StartedAt         int64  `json:"startedAt"`
	CompletedAt       int64  `json:"completedAt,omitempty"`
	TokensConsumed    int    `json:"tokensConsumed"`
	ToolCallsConsumed int    `json:"toolCallsConsumed"`
}

type WorkflowTaskExecution struct {
	WorkflowExecutionID string `json:"workflowExecutionId"`
	TaskTemplateID      string `json:"taskTemplateId"`
	TaskExecutionID     string `json:"taskExecutionId"` // tzro taskId
	Status              string `json:"status"`          // "pending" | "running" | "completed" | "failed" | "interrupted"
	StartedAt           int64  `json:"startedAt"`
	CompletedAt         int64  `json:"completedAt,omitempty"`
}

type OpenAPIIntegration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OpenAPISpec string `json:"openapiSpec"`
	AuthType    string `json:"authType"`
	AuthKey     string `json:"authKey,omitempty"`
	AuthValue   string `json:"authValue,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

type DurableNotification struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Type          string `json:"type"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	TaskID        string `json:"taskId,omitempty"`
	WorkflowID    string `json:"workflowId,omitempty"`
	TargetID      string `json:"targetId,omitempty"`
	Status        string `json:"status"`
	ActionPayload string `json:"actionPayload,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

type SessionTurnLog struct {
	TurnIdx       int      `json:"turnIdx"`
	UserMessage   string   `json:"userMessage"`
	ExecutedTools []string `json:"executedTools"`
}

// ThoughtStep represents a single step in a Probe Node's Thought Chain.
// Each step is a stateless Local Model inference call that produces either
// a tool call or a synthesis decision.
type ThoughtStep struct {
	ID         string `json:"id"`
	ProbeID    string `json:"probeId"`
	TaskID     string `json:"taskId"`
	StepIndex  int    `json:"stepIndex"`
	Thought    string `json:"thought"`
	ToolName   string `json:"toolName,omitempty"`
	ToolArgs   string `json:"toolArgs,omitempty"`
	ToolOutput string `json:"toolOutput,omitempty"`
	Embedding  []byte `json:"embedding,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// ThoughtSummary represents a rolling compaction summary of a Thought Chain.
// Every N steps, recent thoughts are compressed into a summary for context efficiency.
type ThoughtSummary struct {
	ID        string `json:"id"`
	ProbeID   string `json:"probeId"`
	TaskID    string `json:"taskId"`
	StepRange string `json:"stepRange"` // e.g., "1-3", "4-6"
	Summary   string `json:"summary"`
	Embedding []byte `json:"embedding,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// EdgeThought represents a reasoning state propagated across a DAG edge (ADR-0024).
// Generated by the Local Model when the executor traverses an edge whose target node
// has a non-zero Activation Threshold. Contains the current thought and a goal
// confidence score that determines whether dynamic node spawning is triggered.
type EdgeThought struct {
	ID             string  `json:"id"`
	TaskID         string  `json:"taskId"`
	SourceNode     string  `json:"sourceNode"`
	TargetNode     string  `json:"targetNode"`
	Thought        string  `json:"thought"`
	GoalConfidence float64 `json:"goalConfidence"` // 0.0-1.0: sufficiency gate signal
	GoalAchieved   bool    `json:"goalAchieved"`   // Halt flag: true = skip remaining nodes
	StepIndex      int     `json:"stepIndex"`
	CreatedAt      int64   `json:"createdAt"`
}

