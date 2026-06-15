package channel

// Typed payload schemas for ExecutionEvent.Payload (v3).
// Each event type has a corresponding payload struct that gets
// serialized as JSON into ExecutionEvent.Payload.

// TaskStartedPayload is emitted with EventTaskStarted.
type TaskStartedPayload struct {
	NodeCount  int `json:"nodeCount"`
	LevelCount int `json:"levelCount"`
}

// TaskCompletedPayload is emitted with EventTaskCompleted.
type TaskCompletedPayload struct {
	SynthesisSnippet string `json:"synthesisSnippet"`
}

// TaskFailedPayload is emitted with EventTaskFailed.
type TaskFailedPayload struct {
	Error string `json:"error"`
}

// TaskPausedPayload is emitted with EventTaskPaused.
type TaskPausedPayload struct {
	Reason string `json:"reason"`
}

// NodeStartedPayload is emitted with EventNodeStarted.
type NodeStartedPayload struct {
	NodeType string `json:"nodeType"`
	Action   string `json:"action"`
}

// NodeCompletedPayload is emitted with EventNodeCompleted.
type NodeCompletedPayload struct {
	NodeType      string `json:"nodeType"`
	OutputSnippet string `json:"outputSnippet"` // truncated to 500 chars
}

// NodeFailedPayload is emitted with EventNodeFailed.
type NodeFailedPayload struct {
	Error string `json:"error"`
}

// NodeSkippedPayload is emitted with EventNodeSkipped.
type NodeSkippedPayload struct {
	Reason string `json:"reason"`
}

// EdgeThoughtPayload is emitted with EventEdgeThought.
type EdgeThoughtPayload struct {
	Confidence   float64 `json:"confidence"`
	GoalAchieved bool    `json:"goalAchieved"`
}

// ConfidenceEscalationPayload is emitted with EventConfidenceEscalation.
type ConfidenceEscalationPayload struct {
	NodeID string `json:"nodeId"`
	Reason string `json:"reason"`
}

// MutationSpawnedPayload is emitted with EventMutationSpawned.
type MutationSpawnedPayload struct {
	SpawnedNodeID   string `json:"spawnedNodeId"`
	RemainingBudget int    `json:"remainingBudget"`
}
