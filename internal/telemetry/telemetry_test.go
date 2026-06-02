package telemetry

import (
	"sync"
	"testing"
	"time"

	"tzro/internal/stream"
)

func TestTelemetryManagerSubscriptionAndFanOut(t *testing.T) {
	mgr := NewTelemetryManager()

	// 1. Subscribe to mgr with a filter on "task-A"
	subA := mgr.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.TaskID == "task-A"
	})
	defer subA.Unsubscribe()

	// 2. Subscribe to mgr with a filter on "task-B"
	subB := mgr.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.TaskID == "task-B"
	})
	defer subB.Unsubscribe()

	// Publish event for task-A
	mgr.PublishEvent("node_completed", "task-A", "node-1", "Done node 1")

	// Publish event for task-B
	mgr.PublishEvent("node_completed", "task-B", "node-2", "Done node 2")

	// Verify subA got task-A event
	select {
	case chunk := <-subA.Ch:
		if chunk.TaskID != "task-A" {
			t.Errorf("subA: expected task-A, got %s", chunk.TaskID)
		}
		if chunk.Content != "Done node 1" {
			t.Errorf("subA: expected 'Done node 1', got '%s'", chunk.Content)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("subA timed out waiting for task-A event")
	}

	// Verify subA did NOT get task-B event (due to task-A filter)
	select {
	case chunk := <-subA.Ch:
		t.Errorf("subA received unexpected chunk: %+v", chunk)
	default:
		// Correct! Filter worked.
	}

	// Verify subB got task-B event
	select {
	case chunk := <-subB.Ch:
		if chunk.TaskID != "task-B" {
			t.Errorf("subB: expected task-B, got %s", chunk.TaskID)
		}
		if chunk.Content != "Done node 2" {
			t.Errorf("subB: expected 'Done node 2', got '%s'", chunk.Content)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("subB timed out waiting for task-B event")
	}
}

func TestTelemetryManagerMultiThreadingPublish(t *testing.T) {
	mgr := NewTelemetryManager()

	sub := mgr.Subscribe(nil) // nil filter accepts everything
	defer sub.Unsubscribe()

	var wg sync.WaitGroup
	const routines = 10
	const eventsPerRoutine = 5

	for i := 0; i < routines; i++ {
		wg.Add(1)
		go func(routineID int) {
			defer wg.Done()
			for j := 0; j < eventsPerRoutine; j++ {
				mgr.PublishEvent("heartbeat_tick", "system", "node-0", "tick")
			}
		}(i)
	}

	wg.Wait()

	// We should receive routines * eventsPerRoutine total events
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)

	for receivedCount < routines*eventsPerRoutine {
		select {
		case <-sub.Ch:
			receivedCount++
		case <-timeout:
			t.Fatalf("timed out; received %d/%d events", receivedCount, routines*eventsPerRoutine)
		}
	}
}
