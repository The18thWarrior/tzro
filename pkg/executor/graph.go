// Package executor implements the System 1 Graph Call runtime engine.
// It executes declarative DAGs of tool, decision, extract, and group nodes
// using Kahn's topological sort with bounded concurrent dispatch.
package executor

import "context"

// NodeType discriminates the four node kinds in a System 1 Graph Call.
type NodeType string

const (
	NodeTypeTool     NodeType = "tool"
	NodeTypeDecision NodeType = "decision"
	NodeTypeExtract  NodeType = "extract"
	NodeTypeGroup    NodeType = "group"
)

// Node is a single vertex in a System 1 Graph Call DAG.
type Node struct {
	ID                      string                 `json:"id"`
	Type                    NodeType               `json:"type"`
	DependsOn               []string               `json:"depends_on,omitempty"`
	AllowFailedDependencies bool                   `json:"allow_failed_dependencies,omitempty"`
	Tool                    string                 `json:"tool,omitempty"`
	Args                    map[string]interface{} `json:"args,omitempty"`
	Input                   map[string]interface{} `json:"input,omitempty"`
	Question                *Question              `json:"question,omitempty"`
	Accept                  *AcceptCriteria        `json:"accept,omitempty"`
	Labels                  []string               `json:"labels,omitempty"`
	Strategy                string                 `json:"strategy,omitempty"`
	Items                   interface{}            `json:"items,omitempty"`
	MaxConcurrency          int                    `json:"max_concurrency,omitempty"`
	Template                *Node                  `json:"template,omitempty"`
}

// Question defines a typed question for the System 1 Decision Daemon.
type Question struct {
	Type    string   `json:"type"` // "noul", "choice", "score"
	Prompt  string   `json:"prompt"`
	Options []string `json:"options,omitempty"`
}

// AcceptCriteria defines the minimum confidence threshold for a decision node.
type AcceptCriteria struct {
	MinConfidence float64 `json:"min_confidence,omitempty"`
}

// Graph is an immutable JSON object representing a System 1 Graph Call.
type Graph struct {
	Version string   `json:"version"`
	TaskID  string   `json:"task_id"`
	Nodes   []Node   `json:"nodes"`
	Returns []string `json:"returns,omitempty"`
}

// NodeOutput captures the result of executing a single node.
type NodeOutput struct {
	NodeID   string                 `json:"node_id"`
	Status   string                 `json:"status"` // "completed", "failed", "blocked"
	ExitCode int                    `json:"exit_code"`
	Stdout   string                 `json:"stdout,omitempty"`
	Stderr   string                 `json:"stderr,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// ExecutionResult is the final output of a graph execution.
type ExecutionResult struct {
	TaskID  string                 `json:"task_id"`
	Status  string                 `json:"status"` // "completed", "yielded"
	Outputs map[string]NodeOutput  `json:"outputs"`
	Returns map[string]interface{} `json:"returns,omitempty"`
}

// DecisionInput is the request sent to a Decider for decision nodes.
type DecisionInput struct {
	QuestionType string                 `json:"question_type"`
	Prompt       string                 `json:"prompt"`
	Options      []string               `json:"options,omitempty"`
	State        map[string]interface{} `json:"state"`
}

// DecisionOutput is the response from a Decider.
type DecisionOutput struct {
	Answer     string             `json:"answer"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores,omitempty"`
}

// Decider is the system boundary interface for the System 1 Decision Daemon.
// In production this wraps the laya IPC client; in tests it is mocked.
type Decider interface {
	Decide(ctx context.Context, req *DecisionInput) (*DecisionOutput, error)
}

// Extractor is the system boundary interface for the zero-shot span extractor.
type Extractor interface {
	Extract(ctx context.Context, text string, labels []string) ([]ExtractedSpan, error)
}

// ExtractedSpan represents a single extracted parameter span.
type ExtractedSpan struct {
	Label      string  `json:"label"`
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}
