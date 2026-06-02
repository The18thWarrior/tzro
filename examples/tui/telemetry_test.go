package tui

import (
	"context"
	"testing"
	"time"
	"tzro/internal/stream"
)

type mockSSEStream struct {
	chunks chan stream.StreamChunk
}

func TestTUI_SSEStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan stream.StreamChunk, 10)

	// Simulate background reader goroutine
	go func() {
		<-ctx.Done()
		close(ch)
	}()

	// 1. Immediately cancel the context
	cancel()

	// Wait briefly for goroutine shutdown
	time.Sleep(10 * time.Millisecond)

	// 2. Assert channel is closed and no panic occurs
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed upon context cancellation, but it was open")
	}

	// 3. Assert our tea.Cmd listener returns nil when channel is closed
	cmd := listenOnChannel(ch)
	msg := cmd()

	if msg != nil {
		t.Errorf("expected tea.Cmd to return nil Msg on closed channel, got: %v", msg)
	}
}
