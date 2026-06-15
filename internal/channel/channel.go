// Package channel provides a transport-agnostic contract for delivering
// real-time execution events from the tzro engine to external harnesses.
package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Event type constants matching the SubagentChannel spec vocabulary.
const (
	EventTaskStarted          = "task_started"
	EventTaskCompleted        = "task_completed"
	EventTaskFailed           = "task_failed"
	EventTaskPaused           = "task_paused"
	EventNodeStarted          = "node_started"
	EventNodeCompleted        = "node_completed"
	EventNodeFailed           = "node_failed"
	EventNodeSkipped          = "node_skipped"
	EventEdgeThought          = "edge_thought"
	EventConfidenceEscalation = "confidence_escalation"
	EventMutationSpawned      = "mutation_spawned"
)

// ExecutionEvent represents a single lifecycle event during task execution.
type ExecutionEvent struct {
	TaskID    string          `json:"taskId"`
	NodeID    string          `json:"nodeId,omitempty"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Progress  float64         `json:"progress"`
	Total     float64         `json:"total"`
	Timestamp int64           `json:"timestamp"`
}

// NewExecutionEvent creates an ExecutionEvent with the current timestamp.
func NewExecutionEvent(taskID, nodeID, eventType, message string) ExecutionEvent {
	return ExecutionEvent{
		TaskID:    taskID,
		NodeID:    nodeID,
		Type:      eventType,
		Message:   message,
		Timestamp: time.Now().Unix(),
	}
}

// ToolRequest represents a request from the engine to the harness to execute
// a client-side tool. Used by the v2 bidirectional dispatch flow.
type ToolRequest struct {
	TaskID    string                 `json:"taskId"`
	NodeID    string                 `json:"nodeId"`
	ToolName  string                 `json:"toolName"`
	Arguments map[string]interface{} `json:"arguments"`
	RequestID string                 `json:"requestId"` // correlation ID
}

// ToolResponse represents the harness's response after executing a tool request.
type ToolResponse struct {
	RequestID string `json:"requestId"`
	Output    string `json:"output"`
	IsError   bool   `json:"isError"`
}

// ErrToolExecutionUnsupported is returned when a channel adapter does not support
// bidirectional tool dispatch (e.g., no MCP sampling capability).
var ErrToolExecutionUnsupported = fmt.Errorf("channel does not support tool execution")

// SubagentChannel is the transport-agnostic contract for delivering
// execution events from the engine to an external harness.
type SubagentChannel interface {
	EmitEvent(event ExecutionEvent) error

	// v2: Push tool execution requests to the harness and block for the result.
	// Returns ErrToolExecutionUnsupported if the adapter doesn't support bidirectional dispatch.
	RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error)

	// v3: Dynamically update the total node count (e.g., after planning completes).
	UpdateTotal(total float64)

	Close()
}

// RecordingChannel is a test/debug SubagentChannel that captures all emitted events.
type RecordingChannel struct {
	mu     sync.RWMutex
	events []ExecutionEvent
	closed bool
}

// NewRecordingChannel creates a new RecordingChannel.
func NewRecordingChannel() *RecordingChannel {
	return &RecordingChannel{}
}

func (rc *RecordingChannel) EmitEvent(event ExecutionEvent) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.closed {
		return fmt.Errorf("channel closed")
	}
	rc.events = append(rc.events, event)
	return nil
}

func (rc *RecordingChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	return ToolResponse{}, ErrToolExecutionUnsupported
}

func (rc *RecordingChannel) Close() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.closed = true
}

func (rc *RecordingChannel) UpdateTotal(total float64) {
	// No-op for recording channel — it doesn't track progress.
}

// Events returns a copy of the recorded events.
func (rc *RecordingChannel) Events() []ExecutionEvent {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make([]ExecutionEvent, len(rc.events))
	copy(out, rc.events)
	return out
}
