package channel

import (
	"context"
	"fmt"
	"sync"
)

// EventCallback is called for each execution event in the plugin adapter.
type EventCallback func(event ExecutionEvent)

// ToolCallback is called when the engine requests tool execution in the plugin adapter.
// Return the tool response or an error.
type ToolCallback func(req ToolRequest) (ToolResponse, error)

// PluginSubagentChannel implements SubagentChannel for in-process Go integrations.
// Events are delivered as direct function calls with zero serialization overhead.
// Tool dispatch is handled by an optional callback — nil means unsupported.
type PluginSubagentChannel struct {
	onEvent EventCallback
	onTool  ToolCallback // nil = tool execution unsupported
	mu      sync.Mutex
	closed  bool
}

// NewPluginSubagentChannel creates a new PluginSubagentChannel.
//   - onEvent: called for each execution event (required)
//   - onTool: called for tool execution requests (nil = unsupported)
func NewPluginSubagentChannel(onEvent EventCallback, onTool ToolCallback) *PluginSubagentChannel {
	return &PluginSubagentChannel{onEvent: onEvent, onTool: onTool}
}

func (ch *PluginSubagentChannel) EmitEvent(event ExecutionEvent) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return fmt.Errorf("channel closed")
	}
	ch.onEvent(event)
	return nil
}

func (ch *PluginSubagentChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	if ch.onTool == nil {
		return ToolResponse{}, ErrToolExecutionUnsupported
	}
	return ch.onTool(req)
}

func (ch *PluginSubagentChannel) UpdateTotal(total float64) {
	// No-op — plugin adapter doesn't track progress totals.
}

func (ch *PluginSubagentChannel) Close() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.closed = true
}
