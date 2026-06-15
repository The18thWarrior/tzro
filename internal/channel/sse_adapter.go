package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// SSESubagentChannel implements SubagentChannel using Server-Sent Events.
// It streams execution events to an HTTP client via the SSE protocol.
// Bidirectional tool dispatch is not supported (SSE is unidirectional).
type SSESubagentChannel struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
	closed  bool
}

// NewSSESubagentChannel creates a new SSESubagentChannel from an http.ResponseWriter.
// Returns an error if the writer does not support flushing (required for SSE).
func NewSSESubagentChannel(w http.ResponseWriter) (*SSESubagentChannel, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	return &SSESubagentChannel{writer: w, flusher: flusher}, nil
}

func (ch *SSESubagentChannel) EmitEvent(event ExecutionEvent) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return fmt.Errorf("channel closed")
	}

	data, _ := json.Marshal(event)
	_, err := fmt.Fprintf(ch.writer, "event: %s\ndata: %s\n\n", event.Type, data)
	if err != nil {
		return err
	}
	ch.flusher.Flush()
	return nil
}

func (ch *SSESubagentChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	// SSE is unidirectional; bidirectional would require a paired REST endpoint.
	return ToolResponse{}, ErrToolExecutionUnsupported
}

func (ch *SSESubagentChannel) UpdateTotal(total float64) {
	// No-op — SSE adapter doesn't track progress totals.
}

func (ch *SSESubagentChannel) Close() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.closed = true
}
