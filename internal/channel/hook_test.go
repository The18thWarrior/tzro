package channel

import (
	"context"
	"os"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

func TestChannelToolHookBidirectional(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_channel_hook_bid.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_channel_hook_bid.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	// Register a client tool for this test
	tools.Register(&tools.ClientToolAdapter{
		NameVal:   "test_client_tool_bid",
		SchemaVal: `{"type": "object", "properties": {"channel": {"type": "string"}}}`,
	})
	defer tools.Unregister("test_client_tool_bid")

	// Create a mock channel that supports tool dispatch
	ch := &mockBidirectionalChannel{
		response: ToolResponse{
			RequestID: "req-1",
			Output:    `{"sent": true}`,
			IsError:   false,
		},
	}

	hook := &ChannelToolHook{channels: make(map[string]SubagentChannel)}
	hook.RegisterChannel("task-1", ch)

	node := &compiler.GraphNode{
		ID:           "node-1",
		Type:         "deterministic",
		Action:       "test_client_tool_bid",
		Instructions: "Send a message",
	}

	action, err := hook.BeforeNode(context.Background(), "task-1", node)
	if err != nil {
		t.Fatalf("BeforeNode: %v", err)
	}

	// Should return ActionSkip since the hook handled the tool execution
	if action != executor.ActionSkip {
		t.Errorf("expected ActionSkip, got %q", action)
	}

	// Verify the channel received the tool request
	if len(ch.requests) != 1 {
		t.Fatalf("expected 1 tool request, got %d", len(ch.requests))
	}
	req := ch.requests[0]
	if req.ToolName != "test_client_tool_bid" {
		t.Errorf("ToolName: got %q, want %q", req.ToolName, "test_client_tool_bid")
	}
	if req.TaskID != "task-1" {
		t.Errorf("TaskID: got %q, want %q", req.TaskID, "task-1")
	}
	if req.NodeID != "node-1" {
		t.Errorf("NodeID: got %q, want %q", req.NodeID, "node-1")
	}
}

func TestChannelToolHookFallback(t *testing.T) {
	// Register a client tool
	tools.Register(&tools.ClientToolAdapter{
		NameVal:   "test_client_tool_fallback",
		SchemaVal: `{"type": "object"}`,
	})
	defer tools.Unregister("test_client_tool_fallback")

	// Channel that does NOT support tool dispatch
	ch := NewRecordingChannel()

	hook := &ChannelToolHook{channels: make(map[string]SubagentChannel)}
	hook.RegisterChannel("task-1", ch)

	node := &compiler.GraphNode{
		ID:     "node-1",
		Type:   "deterministic",
		Action: "test_client_tool_fallback",
	}

	action, err := hook.BeforeNode(context.Background(), "task-1", node)
	if err != nil {
		t.Fatalf("BeforeNode: %v", err)
	}

	// Should return ActionContinue — falls through to ClientToolHook
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %q", action)
	}
}

func TestChannelToolHookNonClientTool(t *testing.T) {
	// A regular tool (not a ClientToolAdapter) — hook should pass through
	ch := &mockBidirectionalChannel{
		response: ToolResponse{Output: "should not be called"},
	}

	hook := &ChannelToolHook{channels: make(map[string]SubagentChannel)}
	hook.RegisterChannel("task-1", ch)

	// Node with a non-existent action — GetTool returns nil → ActionContinue
	node := &compiler.GraphNode{
		ID:     "node-1",
		Type:   "deterministic",
		Action: "nonexistent_tool",
	}

	action, err := hook.BeforeNode(context.Background(), "task-1", node)
	if err != nil {
		t.Fatalf("BeforeNode: %v", err)
	}

	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue for non-client tool, got %q", action)
	}

	// Verify no tool requests were made
	if len(ch.requests) != 0 {
		t.Errorf("expected 0 tool requests, got %d", len(ch.requests))
	}
}

func TestChannelToolHookNoChannelRegistered(t *testing.T) {
	// Register a client tool
	tools.Register(&tools.ClientToolAdapter{
		NameVal:   "test_client_tool_noreg",
		SchemaVal: `{"type": "object"}`,
	})
	defer tools.Unregister("test_client_tool_noreg")

	// No channel registered for this taskID → ActionContinue
	hook := &ChannelToolHook{channels: make(map[string]SubagentChannel)}

	node := &compiler.GraphNode{
		ID:     "node-1",
		Type:   "deterministic",
		Action: "test_client_tool_noreg",
	}

	action, err := hook.BeforeNode(context.Background(), "task-unknown", node)
	if err != nil {
		t.Fatalf("BeforeNode: %v", err)
	}

	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue when no channel registered, got %q", action)
	}
}

func TestChannelToolHookErrorResponse(t *testing.T) {
	// Register a client tool
	tools.Register(&tools.ClientToolAdapter{
		NameVal:   "test_client_tool_err",
		SchemaVal: `{"type": "object"}`,
	})
	defer tools.Unregister("test_client_tool_err")

	// Channel returns an error response
	ch := &mockBidirectionalChannel{
		response: ToolResponse{
			RequestID: "req-err",
			Output:    "connection refused",
			IsError:   true,
		},
	}

	hook := &ChannelToolHook{channels: make(map[string]SubagentChannel)}
	hook.RegisterChannel("task-1", ch)

	node := &compiler.GraphNode{
		ID:     "node-1",
		Type:   "deterministic",
		Action: "test_client_tool_err",
	}

	action, err := hook.BeforeNode(context.Background(), "task-1", node)

	// Error response should cause abort
	if action != executor.ActionAbort {
		t.Errorf("expected ActionAbort on error response, got %q", action)
	}
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// --- Mock types for hook tests ---

type mockBidirectionalChannel struct {
	response ToolResponse
	requests []ToolRequest
	events   []ExecutionEvent
	closed   bool
}

func (m *mockBidirectionalChannel) EmitEvent(event ExecutionEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockBidirectionalChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	m.requests = append(m.requests, req)
	return m.response, nil
}

func (m *mockBidirectionalChannel) Close() {
	m.closed = true
}

func (m *mockBidirectionalChannel) UpdateTotal(total float64) {}
