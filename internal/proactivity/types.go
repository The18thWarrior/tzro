package proactivity

import (
	"context"
	"time"
)

// ProactivityLevel defines the safety, cost, and visibility bounds of background proposed actions.
type ProactivityLevel int

const (
	// L0Observe allows daemons to inspect state and emit observations. No user approval required.
	L0Observe ProactivityLevel = iota
	// L1Prepare allows local deterministic preparation. No user approval required. Respects budgets.
	L1Prepare
	// L2Suggest surfaces recommendations/alert notifications to the user. User-visible, no side effects.
	L2Suggest
	// L3ReversibleAction performs local reversible actions under policy guidelines.
	L3ReversibleAction
	// L4ExternalSideEffect performs costly or externally visible actions. Always requires explicit approval.
	L4ExternalSideEffect
)

// String returns the string representation of a ProactivityLevel.
func (l ProactivityLevel) String() string {
	switch l {
	case L0Observe:
		return "L0-Observe"
	case L1Prepare:
		return "L1-Prepare"
	case L2Suggest:
		return "L2-Suggest"
	case L3ReversibleAction:
		return "L3-ReversibleAction"
	case L4ExternalSideEffect:
		return "L4-ExternalSideEffect"
	default:
		return "Unknown"
	}
}

// Event represents a typed scheduler input trigger.
type Event struct {
	ID              string
	Type            string // e.g. "file.changed", "workflow.failed", "user.idle"
	Source          string // e.g. "telemetry", "filesystem"
	Timestamp       int64
	Priority        string                 // "critical" | "suggestion" | "ambient"
	CorrelationID   string                 // e.g. taskId, workflowExecutionId
	PayloadMetadata map[string]interface{} // lightweight payload metadata
	Payload         interface{}            // optional payload pointer/envelope
}

// ProposedAction represents a recommended background mutation or observation.
type ProposedAction struct {
	ID                   string
	DaemonID             string
	TriggeringEventID    string
	Level                ProactivityLevel
	ActionType           string // e.g. "cache_warmup", "retry_parse", "notification"
	Description          string
	Confidence           float64
	EstimatedCost        float64 // e.g. cost in tokens or dollar estimate
	EstimatedLatency     time.Duration
	RequiredCapabilities []string // list of required permissions/tools
	RequiresLLM          bool
	IsReversible         bool
	ApprovalRequired     bool
	Payload              interface{}                                    // action parameters
	Execute              func(ctx context.Context) (string, error)      // action execution logic
}

// Budget defines the execution and interval limits for resource consumption.
type Budget struct {
	MaxCPUTime   time.Duration // Max time a single execution (or cumulative) can run
	MaxTokens    int           // Max LLM tokens allowed
	MaxToolCalls int           // Max tool invocations allowed
	Deadline     time.Time     // Absolute wall-clock deadline
}

// Daemon is the interface for background workers hosted by tzro.
type Daemon interface {
	Name() string
	Subscriptions() []string
	MaxLevel() ProactivityLevel
	ResourceRequirements() Budget
	RequiresLLM() bool
	Handler(ctx context.Context, event Event) (*ProposedAction, error)
}

// AttentionItem maps onto memory.DurableNotification for storage and UI dashboard delivery.
type AttentionItem struct {
	ID            string
	ProposedAction *ProposedAction
	Reason        string
	Severity      string // "critical" | "warning" | "info"
	CreatedTime   int64
	ExpireTime    int64
	Status        string // "pending" | "approved" | "rejected" | "expired" | "executed" | "dismissed"
}
