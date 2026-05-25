package telemetry

import (
	"sync"

	"tzro/internal/stream"
)

// EventPublisher defines the seam interface for telemetry clients.
type EventPublisher interface {
	PublishEvent(eventType, taskID, nodeID, payload string)
	PublishStream(chunk stream.StreamChunk)
}

// ObserverEvent mirrors the schema of observer events.
type ObserverEvent struct {
	Type      string `json:"type"` // "task_started" | "node_completed" | "node_failed" | "heartbeat_tick"
	TaskID    string `json:"taskId"`
	NodeID    string `json:"nodeId,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Payload   string `json:"payload,omitempty"`
}

// TelemetrySubscription is an isolated subscription to the telemetry stream.
type TelemetrySubscription struct {
	Ch       chan stream.StreamChunk
	filterFn func(stream.StreamChunk) bool
	manager  *TelemetryManager
}

func (s *TelemetrySubscription) Unsubscribe() {
	s.manager.unsubscribe(s)
}

// TelemetryManager implements EventPublisher in an isolated, context-scoped manner.
type TelemetryManager struct {
	mu          sync.RWMutex
	subscribers map[*TelemetrySubscription]bool
}

// NewTelemetryManager creates an isolated TelemetryManager instance.
func NewTelemetryManager() *TelemetryManager {
	return &TelemetryManager{
		subscribers: make(map[*TelemetrySubscription]bool),
	}
}

func (tm *TelemetryManager) PublishEvent(eventType, taskID, nodeID, payload string) {
	tm.PublishStream(stream.StreamChunk{
		Source:  "telemetry",
		Type:    eventType,
		TaskID:  taskID,
		NodeID:  nodeID,
		Content: payload,
	})
}

func (tm *TelemetryManager) PublishStream(chunk stream.StreamChunk) {
	tm.mu.RLock()
	for sub := range tm.subscribers {
		if sub.filterFn != nil && !sub.filterFn(chunk) {
			continue
		}
		select {
		case sub.Ch <- chunk:
		default:
		}
	}
	tm.mu.RUnlock()

	// Bridge Default manager chunks to legacy stream.GlobalBus for un-refactored consumers.
	if tm == Default {
		stream.GlobalBus.Publish(chunk)
	}
}

func (tm *TelemetryManager) Subscribe(filterFn func(stream.StreamChunk) bool) *TelemetrySubscription {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	sub := &TelemetrySubscription{
		Ch:       make(chan stream.StreamChunk, 100),
		filterFn: filterFn,
		manager:  tm,
	}
	tm.subscribers[sub] = true
	return sub
}

func (tm *TelemetryManager) unsubscribe(sub *TelemetrySubscription) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, exists := tm.subscribers[sub]; exists {
		delete(tm.subscribers, sub)
		close(sub.Ch)
	}
}

// Default is the package-level global singleton fallback telemetry manager.
var Default = NewTelemetryManager()
