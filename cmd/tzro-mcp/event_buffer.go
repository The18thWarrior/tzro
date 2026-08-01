package main

import (
	"sync"
	"time"
)

// TaskEvent represents a structured event captured from the StreamBus for
// enriching MCP resource responses. These events give the subscribing agent
// a timeline of what happened without requiring it to poll tzro_status.
type TaskEvent struct {
	Type      string `json:"type"`                // "node_started", "node_completed", "node_failed", "tool_dispatch", "thought_step"
	NodeID    string `json:"nodeId,omitempty"`     // which execution node emitted this event
	Timestamp int64  `json:"timestamp"`            // unix milliseconds
	Detail    string `json:"detail,omitempty"`     // tool name, error message, synthesis excerpt, etc.
}

// EventBuffer is a bounded, thread-safe ring buffer that captures the last N
// events per task from the StreamBus. It provides the "recentEvents" field
// in enriched resources/read responses.
//
// Deep module: two-method interface, internal per-task ring management.
type EventBuffer struct {
	mu       sync.RWMutex
	capacity int                    // max events per task
	buffers  map[string][]TaskEvent // taskID → circular buffer
	cursors  map[string]int         // taskID → next write index
	counts   map[string]int         // taskID → total events recorded (for ring logic)
}

// NewEventBuffer creates a buffer that retains up to capacity events per task.
func NewEventBuffer(capacity int) *EventBuffer {
	if capacity <= 0 {
		capacity = 50
	}
	return &EventBuffer{
		capacity: capacity,
		buffers:  make(map[string][]TaskEvent),
		cursors:  make(map[string]int),
		counts:   make(map[string]int),
	}
}

// Record appends an event to the per-task ring buffer.
func (b *EventBuffer) Record(taskID string, event TaskEvent) {
	if taskID == "" {
		return
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	buf, exists := b.buffers[taskID]
	if !exists {
		buf = make([]TaskEvent, b.capacity)
		b.buffers[taskID] = buf
		b.cursors[taskID] = 0
		b.counts[taskID] = 0
	}

	idx := b.cursors[taskID]
	buf[idx] = event
	b.cursors[taskID] = (idx + 1) % b.capacity
	b.counts[taskID]++
}

// Recent returns the last `limit` events for a task in chronological order.
// If fewer than `limit` events exist, all events are returned.
func (b *EventBuffer) Recent(taskID string, limit int) []TaskEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()

	buf, exists := b.buffers[taskID]
	if !exists {
		return nil
	}

	total := b.counts[taskID]
	available := total
	if available > b.capacity {
		available = b.capacity
	}
	if limit <= 0 || limit > available {
		limit = available
	}
	if limit == 0 {
		return nil
	}

	// Read from the ring buffer in chronological order
	cursor := b.cursors[taskID]
	result := make([]TaskEvent, 0, limit)
	start := (cursor - limit + b.capacity) % b.capacity

	for i := 0; i < limit; i++ {
		idx := (start + i) % b.capacity
		result = append(result, buf[idx])
	}

	return result
}

// taskEventBuffer is the global event buffer used by the event bridge
// to capture recent events for enriched resources/read responses.
var taskEventBuffer = NewEventBuffer(100)
