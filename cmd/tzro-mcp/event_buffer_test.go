package main

import (
	"testing"
)

func TestEventBuffer_RecordsAndRetrieves(t *testing.T) {
	buf := NewEventBuffer(50)

	buf.Record("task1", TaskEvent{Type: "node_started", NodeID: "n1", Timestamp: 1000})
	buf.Record("task1", TaskEvent{Type: "node_completed", NodeID: "n1", Timestamp: 2000})
	buf.Record("task1", TaskEvent{Type: "node_started", NodeID: "n2", Timestamp: 3000})

	events := buf.Recent("task1", 10)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify chronological order
	if events[0].Type != "node_started" || events[0].NodeID != "n1" {
		t.Errorf("event[0] = %+v, want node_started/n1", events[0])
	}
	if events[1].Type != "node_completed" || events[1].NodeID != "n1" {
		t.Errorf("event[1] = %+v, want node_completed/n1", events[1])
	}
	if events[2].Type != "node_started" || events[2].NodeID != "n2" {
		t.Errorf("event[2] = %+v, want node_started/n2", events[2])
	}
}

func TestEventBuffer_RespectLimit(t *testing.T) {
	buf := NewEventBuffer(50)

	for i := 0; i < 10; i++ {
		buf.Record("task1", TaskEvent{Type: "event", Timestamp: int64(i)})
	}

	events := buf.Recent("task1", 3)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Should be the LAST 3 events (chronological order: 7, 8, 9)
	if events[0].Timestamp != 7 {
		t.Errorf("events[0].Timestamp = %d, want 7", events[0].Timestamp)
	}
	if events[2].Timestamp != 9 {
		t.Errorf("events[2].Timestamp = %d, want 9", events[2].Timestamp)
	}
}

func TestEventBuffer_RingOverflow(t *testing.T) {
	buf := NewEventBuffer(5) // small capacity

	for i := 0; i < 12; i++ {
		buf.Record("task1", TaskEvent{Type: "event", Timestamp: int64(i)})
	}

	events := buf.Recent("task1", 10) // ask for more than capacity
	if len(events) != 5 {
		t.Fatalf("expected 5 events (capacity), got %d", len(events))
	}

	// Should be the LAST 5 events: 7, 8, 9, 10, 11
	if events[0].Timestamp != 7 {
		t.Errorf("events[0].Timestamp = %d, want 7", events[0].Timestamp)
	}
	if events[4].Timestamp != 11 {
		t.Errorf("events[4].Timestamp = %d, want 11", events[4].Timestamp)
	}
}

func TestEventBuffer_IsolatesTasks(t *testing.T) {
	buf := NewEventBuffer(50)

	buf.Record("task1", TaskEvent{Type: "a", Timestamp: 1})
	buf.Record("task2", TaskEvent{Type: "b", Timestamp: 2})
	buf.Record("task1", TaskEvent{Type: "c", Timestamp: 3})

	events1 := buf.Recent("task1", 10)
	events2 := buf.Recent("task2", 10)

	if len(events1) != 2 {
		t.Fatalf("task1: expected 2 events, got %d", len(events1))
	}
	if len(events2) != 1 {
		t.Fatalf("task2: expected 1 event, got %d", len(events2))
	}
}

func TestEventBuffer_EmptyTask(t *testing.T) {
	buf := NewEventBuffer(50)
	events := buf.Recent("nonexistent", 10)
	if events != nil {
		t.Errorf("expected nil for unknown task, got %v", events)
	}
}

func TestEventBuffer_AutoTimestamp(t *testing.T) {
	buf := NewEventBuffer(50)
	buf.Record("task1", TaskEvent{Type: "auto"}) // no timestamp set

	events := buf.Recent("task1", 1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Timestamp == 0 {
		t.Error("expected auto-generated timestamp, got 0")
	}
}
