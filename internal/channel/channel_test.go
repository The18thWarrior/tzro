package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tzro/internal/stream"
)

func TestExecutionEventJSONRoundTrip(t *testing.T) {
	now := time.Now().Unix()
	event := ExecutionEvent{
		TaskID:    "task-123",
		NodeID:    "node-abc",
		Type:      EventNodeStarted,
		Message:   "Executing web_search",
		Payload:   json.RawMessage(`{"nodeType":"action","action":"web_search"}`),
		Progress:  2,
		Total:     5,
		Timestamp: now,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal ExecutionEvent: %v", err)
	}

	var decoded ExecutionEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ExecutionEvent: %v", err)
	}

	if decoded.TaskID != "task-123" {
		t.Errorf("TaskID: got %q, want %q", decoded.TaskID, "task-123")
	}
	if decoded.NodeID != "node-abc" {
		t.Errorf("NodeID: got %q, want %q", decoded.NodeID, "node-abc")
	}
	if decoded.Type != EventNodeStarted {
		t.Errorf("Type: got %q, want %q", decoded.Type, EventNodeStarted)
	}
	if decoded.Message != "Executing web_search" {
		t.Errorf("Message: got %q, want %q", decoded.Message, "Executing web_search")
	}
	if decoded.Progress != 2 {
		t.Errorf("Progress: got %f, want 2", decoded.Progress)
	}
	if decoded.Total != 5 {
		t.Errorf("Total: got %f, want 5", decoded.Total)
	}
	if decoded.Timestamp != now {
		t.Errorf("Timestamp: got %d, want %d", decoded.Timestamp, now)
	}

	// Verify JSON field names match spec (camelCase)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	for _, key := range []string{"taskId", "nodeId", "type", "message", "payload", "progress", "total", "timestamp"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected JSON key %q not found in marshaled output", key)
		}
	}
}

func TestRecordingChannelCapturesEvents(t *testing.T) {
	ch := NewRecordingChannel()

	// Verify it satisfies the interface
	var _ SubagentChannel = ch

	event1 := NewExecutionEvent("task-1", "node-a", EventNodeStarted, "Starting web_search")
	event2 := NewExecutionEvent("task-1", "node-a", EventNodeCompleted, "Completed web_search")

	if err := ch.EmitEvent(event1); err != nil {
		t.Fatalf("EmitEvent(event1): %v", err)
	}
	if err := ch.EmitEvent(event2); err != nil {
		t.Fatalf("EmitEvent(event2): %v", err)
	}

	events := ch.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != EventNodeStarted {
		t.Errorf("event[0].Type: got %q, want %q", events[0].Type, EventNodeStarted)
	}
	if events[1].Type != EventNodeCompleted {
		t.Errorf("event[1].Type: got %q, want %q", events[1].Type, EventNodeCompleted)
	}
}

func TestRecordingChannelClose(t *testing.T) {
	ch := NewRecordingChannel()
	ch.Close()

	// After close, EmitEvent should return an error
	err := ch.EmitEvent(NewExecutionEvent("task-1", "", EventTaskStarted, "test"))
	if err == nil {
		t.Error("expected error after Close(), got nil")
	}
}

func TestEventTypeConstants(t *testing.T) {
	// Verify all event types from the spec vocabulary exist
	expectedTypes := []string{
		"task_started",
		"task_completed",
		"task_failed",
		"task_paused",
		"node_started",
		"node_completed",
		"node_failed",
		"node_skipped",
		"edge_thought",
		"confidence_escalation",
		"mutation_spawned",
	}

	actualTypes := []string{
		EventTaskStarted,
		EventTaskCompleted,
		EventTaskFailed,
		EventTaskPaused,
		EventNodeStarted,
		EventNodeCompleted,
		EventNodeFailed,
		EventNodeSkipped,
		EventEdgeThought,
		EventConfidenceEscalation,
		EventMutationSpawned,
	}

	for i, expected := range expectedTypes {
		if actualTypes[i] != expected {
			t.Errorf("event type constant %d: got %q, want %q", i, actualTypes[i], expected)
		}
	}
}

func TestChunkToEventMapsNodeStateEvents(t *testing.T) {
	// The executor publishes "node_started", "node_completed", "node_failed", "node_skipped"
	// as separate event types through PublishEvent → telemetry → GlobalBus.
	tests := []struct {
		chunkType string
		wantType  string
	}{
		{"node_started", EventNodeStarted},
		{"node_completed", EventNodeCompleted},
		{"node_failed", EventNodeFailed},
		{"node_skipped", EventNodeSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.chunkType, func(t *testing.T) {
			chunk := stream.StreamChunk{
				TaskID:  "task-1",
				NodeID:  "node-a",
				Source:  "telemetry",
				Type:    tt.chunkType,
				Content: "some payload",
			}
			event := ChunkToEvent(chunk)
			if event == nil {
				t.Fatalf("ChunkToEvent returned nil for %q", tt.chunkType)
			}
			if event.Type != tt.wantType {
				t.Errorf("Type: got %q, want %q", event.Type, tt.wantType)
			}
			if event.TaskID != "task-1" {
				t.Errorf("TaskID: got %q, want %q", event.TaskID, "task-1")
			}
			if event.NodeID != "node-a" {
				t.Errorf("NodeID: got %q, want %q", event.NodeID, "node-a")
			}
		})
	}
}

func TestChunkToEventMapsTaskEvents(t *testing.T) {
	tests := []struct {
		chunkType string
		wantType  string
	}{
		{"task_started", EventTaskStarted},
		{"task_completed", EventTaskCompleted},
		{"task_failed", EventTaskFailed},
		{"task_paused", EventTaskPaused},
	}

	for _, tt := range tests {
		t.Run(tt.chunkType, func(t *testing.T) {
			chunk := stream.StreamChunk{
				TaskID:  "task-1",
				Source:  "telemetry",
				Type:    tt.chunkType,
				Content: "test message",
			}
			event := ChunkToEvent(chunk)
			if event == nil {
				t.Fatalf("ChunkToEvent returned nil for %q", tt.chunkType)
			}
			if event.Type != tt.wantType {
				t.Errorf("Type: got %q, want %q", event.Type, tt.wantType)
			}
			if event.NodeID != "" {
				t.Errorf("NodeID should be empty for task events, got %q", event.NodeID)
			}
		})
	}
}

func TestChunkToEventMapsSpecialEvents(t *testing.T) {
	tests := []struct {
		chunkType string
		wantType  string
	}{
		{"confidence_insufficient", EventConfidenceEscalation},
		{"edge_thought_generated", EventEdgeThought},
		{"node_spawned", EventMutationSpawned},
	}

	for _, tt := range tests {
		t.Run(tt.chunkType, func(t *testing.T) {
			chunk := stream.StreamChunk{
				TaskID:  "task-1",
				NodeID:  "node-x",
				Source:  "telemetry",
				Type:    tt.chunkType,
				Content: "detail",
			}
			event := ChunkToEvent(chunk)
			if event == nil {
				t.Fatalf("ChunkToEvent returned nil for %q", tt.chunkType)
			}
			if event.Type != tt.wantType {
				t.Errorf("Type: got %q, want %q", event.Type, tt.wantType)
			}
		})
	}
}

func TestChunkToEventReturnsNilForUnknownTypes(t *testing.T) {
	chunk := stream.StreamChunk{
		TaskID: "task-1",
		Source: "chat",
		Type:   "token",
	}
	event := ChunkToEvent(chunk)
	if event != nil {
		t.Errorf("expected nil for unknown chunk type %q, got %+v", chunk.Type, event)
	}
}

func TestBridgeFiltersByTaskID(t *testing.T) {
	bus := stream.NewBus()
	ch := NewRecordingChannel()

	// Start bridge in background
	done := make(chan struct{})
	go func() {
		Bridge(ch, "target-task", bus)
		close(done)
	}()

	// Wait for bridge goroutine to subscribe
	time.Sleep(10 * time.Millisecond)

	// Publish chunks for different tasks
	bus.Publish(stream.StreamChunk{
		TaskID: "target-task", Type: "task_started", Source: "telemetry", Content: "start",
	})
	bus.Publish(stream.StreamChunk{
		TaskID: "other-task", Type: "task_started", Source: "telemetry", Content: "should not appear",
	})
	bus.Publish(stream.StreamChunk{
		TaskID: "target-task", Type: "node_started", Source: "telemetry", NodeID: "n1", Content: "node",
	})

	// Give bridge goroutine time to process
	time.Sleep(50 * time.Millisecond)

	// Close the channel's subscription by unsubscribing (Bridge exits when sub.Ch closes)
	// We do this indirectly by closing the bus subscription — which happens when Bridge's
	// underlying subscription is unsubscribed. Since Bridge blocks on range sub.Ch,
	// we need to stop it. Let's use a separate bus so we can control lifecycle.
	// Actually, we need to close the bus or unsubscribe to stop the bridge.
	// The cleanest way: create a separate test bus, publish events, then close it.
	// But Bus doesn't have a Close method. Instead, we can verify events collected so far.

	events := ch.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (filtered), got %d: %+v", len(events), events)
	}
	if events[0].Type != EventTaskStarted {
		t.Errorf("events[0].Type: got %q, want %q", events[0].Type, EventTaskStarted)
	}
	if events[1].Type != EventNodeStarted {
		t.Errorf("events[1].Type: got %q, want %q", events[1].Type, EventNodeStarted)
	}
}

func TestBridgeEndToEndEventMapping(t *testing.T) {
	bus := stream.NewBus()
	ch := NewRecordingChannel()

	done := make(chan struct{})
	go func() {
		Bridge(ch, "e2e-task", bus)
		close(done)
	}()

	// Wait for bridge goroutine to subscribe
	time.Sleep(10 * time.Millisecond)

	// Simulate a full task lifecycle
	bus.Publish(stream.StreamChunk{
		TaskID: "e2e-task", Type: "task_started", Source: "telemetry", Content: "Task execution initiated",
	})
	bus.Publish(stream.StreamChunk{
		TaskID: "e2e-task", NodeID: "search", Type: "node_started", Source: "telemetry", Content: "Executing web_search",
	})
	bus.Publish(stream.StreamChunk{
		TaskID: "e2e-task", NodeID: "search", Type: "node_completed", Source: "telemetry", Content: "completed",
	})
	bus.Publish(stream.StreamChunk{
		TaskID: "e2e-task", Type: "task_completed", Source: "telemetry", Content: "done",
	})
	// Unknown type should be ignored
	bus.Publish(stream.StreamChunk{
		TaskID: "e2e-task", Type: "token", Source: "chat", Content: "ignored",
	})

	time.Sleep(50 * time.Millisecond)

	events := ch.Events()
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}

	expectedTypes := []string{EventTaskStarted, EventNodeStarted, EventNodeCompleted, EventTaskCompleted}
	for i, want := range expectedTypes {
		if events[i].Type != want {
			t.Errorf("events[%d].Type: got %q, want %q", i, events[i].Type, want)
		}
	}

	// Verify node events carry NodeID
	if events[1].NodeID != "search" {
		t.Errorf("node_started event NodeID: got %q, want %q", events[1].NodeID, "search")
	}
}

func TestMCPChannelWithProgressToken(t *testing.T) {
	mock := &mockProgressNotifier{}
	ch := NewMCPSubagentChannel("task-42", "tok-abc", mock, nil, 5, nil)

	// Emit a node_started event
	err := ch.EmitEvent(ExecutionEvent{
		TaskID:  "task-42",
		NodeID:  "node-1",
		Type:    EventNodeStarted,
		Message: "Executing web_search",
	})
	if err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 NotifyProgress call, got %d", len(mock.calls))
	}

	call := mock.calls[0]
	if call.ProgressToken != "tok-abc" {
		t.Errorf("ProgressToken: got %v, want %q", call.ProgressToken, "tok-abc")
	}
	if call.Total != 5 {
		t.Errorf("Total: got %f, want 5", call.Total)
	}
	// Message should contain event type for structured parsing
	if call.Message == "" {
		t.Error("Message should not be empty")
	}
}

func TestMCPChannelFallback(t *testing.T) {
	mockRes := &mockResourceUpdater{}
	// progressToken is nil → fallback mode
	ch := NewMCPSubagentChannel("task-99", nil, nil, mockRes, 3, nil)

	err := ch.EmitEvent(ExecutionEvent{
		TaskID:  "task-99",
		NodeID:  "node-x",
		Type:    EventNodeCompleted,
		Message: "done",
	})
	if err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	if len(mockRes.calls) == 0 {
		t.Fatal("expected at least 1 ResourceUpdated call in fallback mode")
	}
	// Should fire task URI
	found := false
	for _, uri := range mockRes.calls {
		if uri == "tzro://tasks/task-99/output" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected task URI in ResourceUpdated calls, got: %v", mockRes.calls)
	}
}

func TestProgressCounterMonotonicity(t *testing.T) {
	mock := &mockProgressNotifier{}
	ch := NewMCPSubagentChannel("task-1", "tok-1", mock, nil, 4, nil)

	// Emit events — only node_completed should increment progress
	ch.EmitEvent(ExecutionEvent{Type: EventTaskStarted, TaskID: "task-1", Message: "start"})
	ch.EmitEvent(ExecutionEvent{Type: EventNodeStarted, TaskID: "task-1", NodeID: "n1", Message: "start"})
	ch.EmitEvent(ExecutionEvent{Type: EventNodeCompleted, TaskID: "task-1", NodeID: "n1", Message: "done"})
	ch.EmitEvent(ExecutionEvent{Type: EventNodeStarted, TaskID: "task-1", NodeID: "n2", Message: "start"})
	ch.EmitEvent(ExecutionEvent{Type: EventNodeCompleted, TaskID: "task-1", NodeID: "n2", Message: "done"})
	ch.EmitEvent(ExecutionEvent{Type: EventTaskCompleted, TaskID: "task-1", Message: "done"})

	if len(mock.calls) != 6 {
		t.Fatalf("expected 6 NotifyProgress calls, got %d", len(mock.calls))
	}

	// Check that Progress is monotonically increasing across node_completed events
	var lastProgress float64
	for _, call := range mock.calls {
		if call.Progress < lastProgress {
			t.Errorf("Progress decreased: %f -> %f", lastProgress, call.Progress)
		}
		lastProgress = call.Progress
	}

	// After 2 node_completed events, progress should be 2
	// Find the last call — should have progress=2
	lastCall := mock.calls[len(mock.calls)-1]
	if lastCall.Progress != 2 {
		t.Errorf("final Progress: got %f, want 2", lastCall.Progress)
	}
}

func TestToolRequestResponseJSONRoundTrip(t *testing.T) {
	// ToolRequest round-trip
	req := ToolRequest{
		TaskID:    "task-abc",
		NodeID:    "node-xyz",
		ToolName:  "send_slack",
		Arguments: map[string]interface{}{"channel": "#general", "message": "hello"},
		RequestID: "req-001",
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal ToolRequest: %v", err)
	}

	var decodedReq ToolRequest
	if err := json.Unmarshal(reqData, &decodedReq); err != nil {
		t.Fatalf("failed to unmarshal ToolRequest: %v", err)
	}

	if decodedReq.TaskID != "task-abc" {
		t.Errorf("TaskID: got %q, want %q", decodedReq.TaskID, "task-abc")
	}
	if decodedReq.NodeID != "node-xyz" {
		t.Errorf("NodeID: got %q, want %q", decodedReq.NodeID, "node-xyz")
	}
	if decodedReq.ToolName != "send_slack" {
		t.Errorf("ToolName: got %q, want %q", decodedReq.ToolName, "send_slack")
	}
	if decodedReq.RequestID != "req-001" {
		t.Errorf("RequestID: got %q, want %q", decodedReq.RequestID, "req-001")
	}
	if decodedReq.Arguments["channel"] != "#general" {
		t.Errorf("Arguments[channel]: got %v, want %q", decodedReq.Arguments["channel"], "#general")
	}

	// Verify camelCase JSON keys
	var rawReq map[string]interface{}
	json.Unmarshal(reqData, &rawReq)
	for _, key := range []string{"taskId", "nodeId", "toolName", "arguments", "requestId"} {
		if _, ok := rawReq[key]; !ok {
			t.Errorf("expected JSON key %q not found in ToolRequest", key)
		}
	}

	// ToolResponse round-trip
	resp := ToolResponse{
		RequestID: "req-001",
		Output:    `{"ok": true}`,
		IsError:   false,
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal ToolResponse: %v", err)
	}

	var decodedResp ToolResponse
	if err := json.Unmarshal(respData, &decodedResp); err != nil {
		t.Fatalf("failed to unmarshal ToolResponse: %v", err)
	}

	if decodedResp.RequestID != "req-001" {
		t.Errorf("RequestID: got %q, want %q", decodedResp.RequestID, "req-001")
	}
	if decodedResp.Output != `{"ok": true}` {
		t.Errorf("Output: got %q, want %q", decodedResp.Output, `{"ok": true}`)
	}
	if decodedResp.IsError != false {
		t.Errorf("IsError: got %v, want false", decodedResp.IsError)
	}

	// Verify camelCase JSON keys
	var rawResp map[string]interface{}
	json.Unmarshal(respData, &rawResp)
	for _, key := range []string{"requestId", "output", "isError"} {
		if _, ok := rawResp[key]; !ok {
			t.Errorf("expected JSON key %q not found in ToolResponse", key)
		}
	}
}

func TestErrToolExecutionUnsupported(t *testing.T) {
	err := ErrToolExecutionUnsupported
	if err.Error() != "channel does not support tool execution" {
		t.Errorf("error message: got %q, want %q", err.Error(), "channel does not support tool execution")
	}
}

func TestRecordingChannelRequestToolReturnsUnsupported(t *testing.T) {
	ch := NewRecordingChannel()

	// Verify it satisfies the extended interface
	var _ SubagentChannel = ch

	req := ToolRequest{
		TaskID:    "task-1",
		NodeID:    "node-1",
		ToolName:  "send_slack",
		Arguments: map[string]interface{}{"channel": "#test"},
		RequestID: "req-1",
	}

	_, err := ch.RequestToolExecution(context.Background(), req)
	if err != ErrToolExecutionUnsupported {
		t.Errorf("expected ErrToolExecutionUnsupported, got %v", err)
	}
}

func TestMCPAdapterSamplingDispatch(t *testing.T) {
	mock := &mockToolDispatcher{
		response: `{"requestId":"req-42","output":"message sent","isError":false}`,
	}
	ch := NewMCPSubagentChannel("task-1", "tok-1", &mockProgressNotifier{}, nil, 3, mock)

	resp, err := ch.RequestToolExecution(context.Background(), ToolRequest{
		TaskID:    "task-1",
		NodeID:    "node-1",
		ToolName:  "send_slack",
		Arguments: map[string]interface{}{"channel": "#general"},
		RequestID: "req-42",
	})
	if err != nil {
		t.Fatalf("RequestToolExecution: %v", err)
	}

	if resp.RequestID != "req-42" {
		t.Errorf("RequestID: got %q, want %q", resp.RequestID, "req-42")
	}
	if resp.Output != "message sent" {
		t.Errorf("Output: got %q, want %q", resp.Output, "message sent")
	}
	if resp.IsError {
		t.Error("IsError: got true, want false")
	}

	// Verify the dispatcher was called with correct system prompt
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 CreateMessage call, got %d", len(mock.calls))
	}
	call := mock.calls[0]
	if call.maxTokens != 4096 {
		t.Errorf("maxTokens: got %d, want 4096", call.maxTokens)
	}
	// Verify the user message contains the tool request JSON
	if !contains(call.userMessage, "send_slack") {
		t.Errorf("user message should contain tool name, got: %s", call.userMessage)
	}
	if !contains(call.userMessage, "req-42") {
		t.Errorf("user message should contain request ID, got: %s", call.userMessage)
	}
}

func TestMCPAdapterToolDispatcherNil(t *testing.T) {
	// No dispatcher → returns ErrToolExecutionUnsupported
	ch := NewMCPSubagentChannel("task-1", "tok-1", &mockProgressNotifier{}, nil, 3, nil)

	_, err := ch.RequestToolExecution(context.Background(), ToolRequest{
		TaskID:    "task-1",
		NodeID:    "node-1",
		ToolName:  "send_slack",
		RequestID: "req-1",
	})
	if err != ErrToolExecutionUnsupported {
		t.Errorf("expected ErrToolExecutionUnsupported, got %v", err)
	}
}

func TestMCPAdapterSamplingRawTextFallback(t *testing.T) {
	// Dispatcher returns raw text (not JSON) — should be treated as successful output
	mock := &mockToolDispatcher{
		response: "Tool executed successfully, here is the output.",
	}
	ch := NewMCPSubagentChannel("task-1", "tok-1", &mockProgressNotifier{}, nil, 3, mock)

	resp, err := ch.RequestToolExecution(context.Background(), ToolRequest{
		TaskID:    "task-1",
		NodeID:    "node-1",
		ToolName:  "deploy_k8s",
		RequestID: "req-99",
	})
	if err != nil {
		t.Fatalf("RequestToolExecution: %v", err)
	}

	// Raw text should be wrapped as output with the original request ID
	if resp.RequestID != "req-99" {
		t.Errorf("RequestID: got %q, want %q", resp.RequestID, "req-99")
	}
	if resp.Output != "Tool executed successfully, here is the output." {
		t.Errorf("Output: got %q, want raw text", resp.Output)
	}
	if resp.IsError {
		t.Error("IsError: got true, want false")
	}
}

func TestConcurrencySafety(t *testing.T) {
	mock := &mockProgressNotifier{}
	ch := NewMCPSubagentChannel("task-race", "tok-race", mock, nil, 10, nil)

	// Hammer the channel from multiple goroutines — triggers race detector
	// if EmitEvent/Close/UpdateTotal lack synchronization.
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				_ = ch.EmitEvent(ExecutionEvent{
					TaskID:  "task-race",
					NodeID:  fmt.Sprintf("node-%d-%d", id, j),
					Type:    EventNodeCompleted,
					Message: "done",
				})
			}
		}(i)
	}

	// Concurrent Close while events are being emitted
	go func() {
		time.Sleep(1 * time.Millisecond)
		ch.Close()
		done <- struct{}{}
	}()

	// Wait for all goroutines
	for i := 0; i < 11; i++ {
		<-done
	}

	// If we get here without race detector complaints, concurrency is safe.
	// The channel should be closed.
	err := ch.EmitEvent(ExecutionEvent{Type: EventTaskStarted, TaskID: "task-race"})
	if err == nil {
		t.Error("expected error after concurrent Close, got nil")
	}
}

func TestDynamicTotalUpdate(t *testing.T) {
	mock := &mockProgressNotifier{}
	ch := NewMCPSubagentChannel("task-dyn", "tok-dyn", mock, nil, 0, nil)

	// Initially nodeCount is 0 — simulate the tzro_run case where planning hasn't happened yet
	_ = ch.EmitEvent(ExecutionEvent{Type: EventTaskStarted, TaskID: "task-dyn", Message: "started"})
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	if mock.calls[0].Total != 0 {
		t.Errorf("initial Total: got %f, want 0", mock.calls[0].Total)
	}

	// Now update the total — simulates Bridge receiving task_started with nodeCount
	ch.UpdateTotal(5)

	_ = ch.EmitEvent(ExecutionEvent{Type: EventNodeStarted, TaskID: "task-dyn", NodeID: "n1", Message: "go"})
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	if mock.calls[1].Total != 5 {
		t.Errorf("after UpdateTotal: Total got %f, want 5", mock.calls[1].Total)
	}
}

func TestStructuredPayloadNodeStarted(t *testing.T) {
	// Chunk Content carries JSON with nodeType and action
	chunk := stream.StreamChunk{
		TaskID:  "task-1",
		NodeID:  "node-a",
		Source:  "telemetry",
		Type:    "node_started",
		Content: `{"nodeType":"action","action":"web_search"}`,
	}

	event := ChunkToEvent(chunk)
	if event == nil {
		t.Fatal("ChunkToEvent returned nil")
	}
	if event.Type != EventNodeStarted {
		t.Fatalf("Type: got %q, want %q", event.Type, EventNodeStarted)
	}

	// Payload should be populated with NodeStartedPayload JSON
	if event.Payload == nil {
		t.Fatal("Payload is nil — ChunkToEvent should populate structured payload")
	}

	var payload NodeStartedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal NodeStartedPayload: %v", err)
	}
	if payload.NodeType != "action" {
		t.Errorf("NodeType: got %q, want %q", payload.NodeType, "action")
	}
	if payload.Action != "web_search" {
		t.Errorf("Action: got %q, want %q", payload.Action, "web_search")
	}
}

func TestStructuredPayloadNodeCompletedTruncation(t *testing.T) {
	// Build a long output string (600 chars)
	longOutput := strings.Repeat("x", 600)
	content := fmt.Sprintf(`{"nodeType":"action","output":"%s"}`, longOutput)

	chunk := stream.StreamChunk{
		TaskID:  "task-1",
		NodeID:  "node-b",
		Source:  "telemetry",
		Type:    "node_completed",
		Content: content,
	}

	event := ChunkToEvent(chunk)
	if event == nil {
		t.Fatal("ChunkToEvent returned nil")
	}
	if event.Payload == nil {
		t.Fatal("Payload is nil")
	}

	var payload NodeCompletedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Output should be truncated to 500 + "..."
	if len(payload.OutputSnippet) != 503 {
		t.Errorf("OutputSnippet length: got %d, want 503 (500 + '...')", len(payload.OutputSnippet))
	}
	if payload.OutputSnippet[500:] != "..." {
		t.Errorf("expected trailing '...' on truncated output")
	}
	if payload.NodeType != "action" {
		t.Errorf("NodeType: got %q, want %q", payload.NodeType, "action")
	}
}

func TestStructuredPayloadRemainingTypes(t *testing.T) {
	tests := []struct {
		chunkType string
		content   string
		validate  func(t *testing.T, payload json.RawMessage)
	}{
		{
			chunkType: "task_started",
			content:   `{"nodeCount":5,"levelCount":3}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p TaskStartedPayload
				json.Unmarshal(payload, &p)
				if p.NodeCount != 5 {
					t.Errorf("NodeCount: got %d, want 5", p.NodeCount)
				}
				if p.LevelCount != 3 {
					t.Errorf("LevelCount: got %d, want 3", p.LevelCount)
				}
			},
		},
		{
			chunkType: "task_completed",
			content:   `{"synthesisSnippet":"Summary here"}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p TaskCompletedPayload
				json.Unmarshal(payload, &p)
				if p.SynthesisSnippet != "Summary here" {
					t.Errorf("SynthesisSnippet: got %q", p.SynthesisSnippet)
				}
			},
		},
		{
			chunkType: "task_failed",
			content:   `{"error":"timeout exceeded"}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p TaskFailedPayload
				json.Unmarshal(payload, &p)
				if p.Error != "timeout exceeded" {
					t.Errorf("Error: got %q", p.Error)
				}
			},
		},
		{
			chunkType: "task_paused",
			content:   `{"reason":"waiting for approval"}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p TaskPausedPayload
				json.Unmarshal(payload, &p)
				if p.Reason != "waiting for approval" {
					t.Errorf("Reason: got %q", p.Reason)
				}
			},
		},
		{
			chunkType: "node_failed",
			content:   `{"error":"tool not found"}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p NodeFailedPayload
				json.Unmarshal(payload, &p)
				if p.Error != "tool not found" {
					t.Errorf("Error: got %q", p.Error)
				}
			},
		},
		{
			chunkType: "node_skipped",
			content:   `{"reason":"goal already achieved"}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p NodeSkippedPayload
				json.Unmarshal(payload, &p)
				if p.Reason != "goal already achieved" {
					t.Errorf("Reason: got %q", p.Reason)
				}
			},
		},
		{
			chunkType: "edge_thought_generated",
			content:   `{"confidence":0.85,"goalAchieved":false}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p EdgeThoughtPayload
				json.Unmarshal(payload, &p)
				if p.Confidence != 0.85 {
					t.Errorf("Confidence: got %f, want 0.85", p.Confidence)
				}
				if p.GoalAchieved {
					t.Error("GoalAchieved should be false")
				}
			},
		},
		{
			chunkType: "confidence_insufficient",
			content:   `{"nodeId":"node-x","reason":"below threshold"}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p ConfidenceEscalationPayload
				json.Unmarshal(payload, &p)
				if p.NodeID != "node-x" {
					t.Errorf("NodeID: got %q", p.NodeID)
				}
				if p.Reason != "below threshold" {
					t.Errorf("Reason: got %q", p.Reason)
				}
			},
		},
		{
			chunkType: "node_spawned",
			content:   `{"spawnedNodeId":"spawned-1","remainingBudget":4}`,
			validate: func(t *testing.T, payload json.RawMessage) {
				var p MutationSpawnedPayload
				json.Unmarshal(payload, &p)
				if p.SpawnedNodeID != "spawned-1" {
					t.Errorf("SpawnedNodeID: got %q", p.SpawnedNodeID)
				}
				if p.RemainingBudget != 4 {
					t.Errorf("RemainingBudget: got %d, want 4", p.RemainingBudget)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.chunkType, func(t *testing.T) {
			chunk := stream.StreamChunk{
				TaskID:  "task-1",
				NodeID:  "node-a",
				Source:  "telemetry",
				Type:    tt.chunkType,
				Content: tt.content,
			}
			event := ChunkToEvent(chunk)
			if event == nil {
				t.Fatal("ChunkToEvent returned nil")
			}
			if event.Payload == nil {
				t.Fatal("Payload is nil")
			}
			tt.validate(t, event.Payload)
		})
	}
}

func TestStructuredPayloadNonJSONContent(t *testing.T) {
	// When Content is plain text (not JSON), Payload should be nil (graceful degradation)
	chunk := stream.StreamChunk{
		TaskID:  "task-1",
		NodeID:  "node-a",
		Source:  "telemetry",
		Type:    "node_started",
		Content: "Just a plain text message, not JSON",
	}
	event := ChunkToEvent(chunk)
	if event == nil {
		t.Fatal("ChunkToEvent returned nil")
	}
	if event.Payload != nil {
		t.Errorf("expected nil Payload for non-JSON content, got %s", string(event.Payload))
	}
	// Message should still be set
	if event.Message != "Just a plain text message, not JSON" {
		t.Errorf("Message should be preserved: got %q", event.Message)
	}
}

func TestBridgeDynamicTotalOnTaskStarted(t *testing.T) {
	bus := stream.NewBus()
	ch := &mockTrackingChannel{}

	done := make(chan struct{})
	go func() {
		Bridge(ch, "task-dyn-bridge", bus)
		close(done)
	}()

	// Wait for bridge goroutine to subscribe
	time.Sleep(10 * time.Millisecond)

	// Publish task_started with nodeCount in JSON content
	bus.Publish(stream.StreamChunk{
		TaskID:  "task-dyn-bridge",
		Type:    "task_started",
		Source:  "telemetry",
		Content: `{"nodeCount":7,"levelCount":3}`,
	})

	time.Sleep(50 * time.Millisecond)

	// Verify UpdateTotal was called with the node count from the payload
	if got := ch.getLastTotal(); got != 7 {
		t.Errorf("expected UpdateTotal(7), got UpdateTotal(%f)", got)
	}
}

func TestBridgeErrorBackpressure(t *testing.T) {
	bus := stream.NewBus()

	// A channel that always returns errors on EmitEvent
	errCh := &mockErrorChannel{emitErr: fmt.Errorf("transport dead")}

	var capturedErrors []string
	var errMu sync.Mutex

	done := make(chan struct{})
	go func() {
		BridgeWithOptions(errCh, "task-err", BridgeOptions{
			Bus: bus,
			OnEmitError: func(event ExecutionEvent, err error) {
				errMu.Lock()
				defer errMu.Unlock()
				capturedErrors = append(capturedErrors, err.Error())
			},
		})
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)

	bus.Publish(stream.StreamChunk{
		TaskID: "task-err", Type: "task_started", Source: "telemetry", Content: `{}`,
	})
	bus.Publish(stream.StreamChunk{
		TaskID: "task-err", Type: "node_started", Source: "telemetry", NodeID: "n1", Content: `{}`,
	})

	time.Sleep(50 * time.Millisecond)

	errMu.Lock()
	defer errMu.Unlock()
	if len(capturedErrors) != 2 {
		t.Fatalf("expected 2 error callbacks, got %d", len(capturedErrors))
	}
	if capturedErrors[0] != "transport dead" {
		t.Errorf("error[0]: got %q, want %q", capturedErrors[0], "transport dead")
	}
}

func TestBridgeStopOnError(t *testing.T) {
	bus := stream.NewBus()
	errCh := &mockErrorChannel{emitErr: fmt.Errorf("fatal")}

	done := make(chan struct{})
	go func() {
		BridgeWithOptions(errCh, "task-stop", BridgeOptions{
			Bus:         bus,
			StopOnError: true,
		})
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)

	// First event should trigger error → bridge exits
	bus.Publish(stream.StreamChunk{
		TaskID: "task-stop", Type: "task_started", Source: "telemetry", Content: `{}`,
	})

	// Bridge should exit quickly due to StopOnError
	select {
	case <-done:
		// Bridge exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not exit after StopOnError")
	}
}

func TestSSEAdapterFormat(t *testing.T) {
	recorder := httptest.NewRecorder()
	ch, err := NewSSESubagentChannel(recorder)
	if err != nil {
		t.Fatalf("NewSSESubagentChannel: %v", err)
	}

	event := NewExecutionEvent("task-sse", "node-1", EventNodeStarted, "Executing web_search")
	if err := ch.EmitEvent(event); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	body := recorder.Body.String()

	// SSE format: "event: {type}\ndata: {json}\n\n"
	if !strings.Contains(body, "event: node_started\n") {
		t.Errorf("expected 'event: node_started' in body, got:\n%s", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("expected 'data: ' in body, got:\n%s", body)
	}
	// Should end with double newline
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("expected body to end with double newline, got:\n%q", body)
	}

	// Verify the data line contains valid JSON with expected fields
	lines := strings.Split(body, "\n")
	var dataLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatal("no data line found in SSE output")
	}

	var parsed ExecutionEvent
	if err := json.Unmarshal([]byte(dataLine), &parsed); err != nil {
		t.Fatalf("failed to parse SSE data as JSON: %v", err)
	}
	if parsed.TaskID != "task-sse" {
		t.Errorf("TaskID: got %q, want %q", parsed.TaskID, "task-sse")
	}
	if parsed.Type != EventNodeStarted {
		t.Errorf("Type: got %q, want %q", parsed.Type, EventNodeStarted)
	}
}

func TestSSEAdapterClosed(t *testing.T) {
	recorder := httptest.NewRecorder()
	ch, err := NewSSESubagentChannel(recorder)
	if err != nil {
		t.Fatalf("NewSSESubagentChannel: %v", err)
	}

	ch.Close()

	err = ch.EmitEvent(NewExecutionEvent("task-1", "", EventTaskStarted, "test"))
	if err == nil {
		t.Error("expected error after Close, got nil")
	}
}

func TestSSEAdapterToolExecutionUnsupported(t *testing.T) {
	recorder := httptest.NewRecorder()
	ch, _ := NewSSESubagentChannel(recorder)

	_, err := ch.RequestToolExecution(context.Background(), ToolRequest{
		TaskID:    "task-1",
		ToolName:  "send_slack",
		RequestID: "req-1",
	})
	if err != ErrToolExecutionUnsupported {
		t.Errorf("expected ErrToolExecutionUnsupported, got %v", err)
	}
}

func TestPluginAdapterEventsInOrder(t *testing.T) {
	var received []ExecutionEvent

	ch := NewPluginSubagentChannel(func(event ExecutionEvent) {
		received = append(received, event)
	}, nil)

	events := []ExecutionEvent{
		NewExecutionEvent("task-1", "", EventTaskStarted, "start"),
		NewExecutionEvent("task-1", "n1", EventNodeStarted, "node start"),
		NewExecutionEvent("task-1", "n1", EventNodeCompleted, "node done"),
		NewExecutionEvent("task-1", "", EventTaskCompleted, "done"),
	}

	for _, e := range events {
		if err := ch.EmitEvent(e); err != nil {
			t.Fatalf("EmitEvent: %v", err)
		}
	}

	if len(received) != 4 {
		t.Fatalf("expected 4 events, got %d", len(received))
	}

	expectedTypes := []string{EventTaskStarted, EventNodeStarted, EventNodeCompleted, EventTaskCompleted}
	for i, want := range expectedTypes {
		if received[i].Type != want {
			t.Errorf("event[%d]: got %q, want %q", i, received[i].Type, want)
		}
	}
}

func TestPluginAdapterToolCallback(t *testing.T) {
	ch := NewPluginSubagentChannel(
		func(event ExecutionEvent) {},
		func(req ToolRequest) (ToolResponse, error) {
			return ToolResponse{
				RequestID: req.RequestID,
				Output:    fmt.Sprintf("executed %s", req.ToolName),
				IsError:   false,
			}, nil
		},
	)

	resp, err := ch.RequestToolExecution(context.Background(), ToolRequest{
		TaskID:    "task-1",
		NodeID:    "node-1",
		ToolName:  "deploy_k8s",
		RequestID: "req-42",
	})
	if err != nil {
		t.Fatalf("RequestToolExecution: %v", err)
	}
	if resp.RequestID != "req-42" {
		t.Errorf("RequestID: got %q, want %q", resp.RequestID, "req-42")
	}
	if resp.Output != "executed deploy_k8s" {
		t.Errorf("Output: got %q, want %q", resp.Output, "executed deploy_k8s")
	}
}

func TestPluginAdapterToolCallbackNil(t *testing.T) {
	// No tool callback → returns ErrToolExecutionUnsupported
	ch := NewPluginSubagentChannel(func(event ExecutionEvent) {}, nil)

	_, err := ch.RequestToolExecution(context.Background(), ToolRequest{
		RequestID: "req-1",
	})
	if err != ErrToolExecutionUnsupported {
		t.Errorf("expected ErrToolExecutionUnsupported, got %v", err)
	}
}

func TestPluginAdapterClosed(t *testing.T) {
	ch := NewPluginSubagentChannel(func(event ExecutionEvent) {}, nil)
	ch.Close()

	err := ch.EmitEvent(NewExecutionEvent("task-1", "", EventTaskStarted, "test"))
	if err == nil {
		t.Error("expected error after Close, got nil")
	}
}

// --- Mock types ---

type progressCall struct {
	ProgressToken any
	Message       string
	Progress      float64
	Total         float64
}

type mockProgressNotifier struct {
	calls []progressCall
}

func (m *mockProgressNotifier) NotifyProgress(token any, message string, progress, total float64) error {
	m.calls = append(m.calls, progressCall{
		ProgressToken: token,
		Message:       message,
		Progress:      progress,
		Total:         total,
	})
	return nil
}

type mockResourceUpdater struct {
	calls []string // URIs
}

func (m *mockResourceUpdater) ResourceUpdated(uri string) error {
	m.calls = append(m.calls, uri)
	return nil
}

type dispatcherCall struct {
	systemPrompt string
	userMessage  string
	maxTokens    int64
}

type mockToolDispatcher struct {
	response string
	err      error
	calls    []dispatcherCall
}

func (m *mockToolDispatcher) CreateMessage(ctx context.Context, systemPrompt, userMessage string, maxTokens int64) (string, error) {
	m.calls = append(m.calls, dispatcherCall{
		systemPrompt: systemPrompt,
		userMessage:  userMessage,
		maxTokens:    maxTokens,
	})
	return m.response, m.err
}

// contains is a test helper for substring checks.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && strings.Contains(s, substr)))
}

// mockTrackingChannel tracks UpdateTotal calls and events for bridge tests.
type mockTrackingChannel struct {
	mu        sync.Mutex
	events    []ExecutionEvent
	lastTotal float64
}

func (m *mockTrackingChannel) EmitEvent(event ExecutionEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockTrackingChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	return ToolResponse{}, ErrToolExecutionUnsupported
}

func (m *mockTrackingChannel) UpdateTotal(total float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTotal = total
}

func (m *mockTrackingChannel) Close() {}

func (m *mockTrackingChannel) getLastTotal() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastTotal
}

// mockErrorChannel always returns emitErr from EmitEvent.
type mockErrorChannel struct {
	emitErr error
}

func (m *mockErrorChannel) EmitEvent(event ExecutionEvent) error {
	return m.emitErr
}

func (m *mockErrorChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	return ToolResponse{}, ErrToolExecutionUnsupported
}

func (m *mockErrorChannel) UpdateTotal(total float64) {}
func (m *mockErrorChannel) Close()                    {}
