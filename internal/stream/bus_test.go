package stream

import (
	"sync"
	"testing"
	"time"
)

func TestStreamBusPubSub(t *testing.T) {
	bus := NewBus()

	// Subscribe to all chunks
	sub1 := bus.Subscribe(nil)
	defer sub1.Unsubscribe()

	// Subscribe with a filter
	sub2 := bus.Subscribe(func(chunk StreamChunk) bool {
		return chunk.Source == "chat"
	})
	defer sub2.Unsubscribe()

	chunk1 := StreamChunk{
		StreamID: "stream-1",
		Source:   "chat",
		Type:     "token",
		Content:  "hello",
	}

	chunk2 := StreamChunk{
		StreamID: "stream-2",
		Source:   "executor",
		Type:     "node_state",
		Content:  "running",
	}

	// Publish first chunk
	bus.Publish(chunk1)

	// Verify both sub1 and sub2 get chunk1
	select {
	case c := <-sub1.Ch:
		if c.Content != "hello" {
			t.Errorf("sub1: expected hello, got %s", c.Content)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("sub1 timed out waiting for chunk1")
	}

	select {
	case c := <-sub2.Ch:
		if c.Content != "hello" {
			t.Errorf("sub2: expected hello, got %s", c.Content)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("sub2 timed out waiting for chunk1")
	}

	// Publish second chunk
	bus.Publish(chunk2)

	// Verify sub1 gets chunk2, but sub2 does not (filtered out)
	select {
	case c := <-sub1.Ch:
		if c.Content != "running" {
			t.Errorf("sub1: expected running, got %s", c.Content)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("sub1 timed out waiting for chunk2")
	}

	select {
	case c := <-sub2.Ch:
		t.Errorf("sub2 received unexpected filtered out chunk: %+v", c)
	default:
		// Passed, sub2 did not receive chunk2
	}
}

func TestStreamBusUnsubscribe(t *testing.T) {
	bus := NewBus()

	sub := bus.Subscribe(nil)

	chunk := StreamChunk{Content: "test"}
	bus.Publish(chunk)

	// Consume it
	<-sub.Ch

	// Unsubscribe
	sub.Unsubscribe()

	// Publish again
	bus.Publish(chunk)

	// Verify no more receive and channel is closed (or just doesn't receive)
	// We will design Unsubscribe to close the channel so we can detect it.
	select {
	case _, ok := <-sub.Ch:
		if ok {
			t.Error("sub.Ch should be closed or empty")
		}
	case <-time.After(50 * time.Millisecond):
		// Unsubscribe might close or just remove subscription. If we close, it will read immediately.
		// If we close the channel in Unsubscribe, we should verify it is indeed closed.
	}
}

func TestStreamBusNonBlocking(t *testing.T) {
	bus := NewBus()

	// Create a subscriber but do not read from it (making it slow/blocked)
	sub := bus.Subscribe(nil)
	defer sub.Unsubscribe()

	// Publish a chunk. It should not block the caller!
	start := time.Now()
	done := make(chan bool, 1)
	go func() {
		bus.Publish(StreamChunk{Content: "1"})
		bus.Publish(StreamChunk{Content: "2"})
		bus.Publish(StreamChunk{Content: "3"})
		done <- true
	}()

	select {
	case <-done:
		// Published without blocking!
	case <-time.After(100 * time.Millisecond):
		t.Error("Publish blocked on slow subscriber")
	}

	duration := time.Since(start)
	if duration > 50*time.Millisecond {
		t.Errorf("Publish took too long: %v", duration)
	}
}

func TestStreamBusConcurrency(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	// Concurrently subscribe and unsubscribe
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sub := bus.Subscribe(nil)
				bus.Publish(StreamChunk{Content: "concurrency"})
				sub.Unsubscribe()
			}
		}()
	}

	// Concurrently publish
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				bus.Publish(StreamChunk{Content: "pub"})
			}
		}()
	}

	wg.Wait()
}
