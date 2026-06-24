# SubagentChannel v2 & v3: Bidirectional Execution and Multi-Adapter Transport

**Depends on**: [SubagentChannel v1](2026-06-11-subagent-channel-design.md) (implemented)

---

## v1 Recap (Implemented)

v1 delivered a unidirectional event channel:
- `SubagentChannel` interface with `EmitEvent(event ExecutionEvent) error` and `Close()`
- `MCPSubagentChannel` adapter with `NotifyProgress` (progress token) / `ResourceUpdated` (fallback) dual-mode
- `Bridge()` goroutine subscribing to `stream.GlobalBus` filtered by taskID
- `ChunkToEvent()` mapper decomposing `StreamChunk` events into typed `ExecutionEvent`s
- 11 event types: `task_started`, `task_completed`, `task_failed`, `task_paused`, `node_started`, `node_completed`, `node_failed`, `node_skipped`, `edge_thought`, `confidence_escalation`, `mutation_spawned`

v1 is read-only: the harness observes execution but cannot interact with it.

---

## v2: Bidirectional Tool Execution via MCP Sampling

### Problem

When the executor encounters a client-side tool (e.g., `send_slack`, `deploy_k8s`), the current flow is:

1. `ClientToolHook.BeforeNode` detects the client tool, writes a `durable_notifications` row, returns `ActionPause`
2. The executor pauses the entire task
3. The harness polls `tzro_client_tool_list` to discover pending requests
4. The harness executes the tool locally and calls `tzro_client_tool_submit` with the output
5. The harness calls `tzro_resume` to restart execution

This is a **4-step polling loop** requiring 3 cloud LLM turns in the harness (discover → execute → resume). Each turn costs tokens and adds latency.

### Goal

Replace the polling client-tool pattern with a **push model** where the SubagentChannel asks the harness to execute a tool directly, receives the result, and resumes execution without pausing the task.

### MCP Primitive: `session.CreateMessage()`

The MCP SDK (v0.8.0) provides `ServerSession.CreateMessage()` — the **sampling** primitive where the server asks the client to generate a response. The client advertises sampling support via `ClientCapabilities.Sampling`.

```go
// Server-side: ask the client to execute a tool
result, err := session.CreateMessage(ctx, &mcp.CreateMessageParams{
    Messages: []*mcp.SamplingMessage{{
        Role:    mcp.RoleUser,
        Content: &mcp.TextContent{Text: toolRequestJSON},
    }},
    MaxTokens:    1,  // We want a structured response, not generation
    SystemPrompt: "Execute the tool call and return the result as JSON.",
})
```

The client (Antigravity, Claude Code, etc.) receives this as a `CreateMessageRequest`, executes the tool locally, and returns the result as a `CreateMessageResult`.

### Interface Extension

```go
// internal/channel/channel.go

type ToolRequest struct {
    TaskID    string                 `json:"taskId"`
    NodeID    string                 `json:"nodeId"`
    ToolName  string                 `json:"toolName"`
    Arguments map[string]interface{} `json:"arguments"`
    RequestID string                 `json:"requestId"` // correlation ID
}

type ToolResponse struct {
    RequestID string `json:"requestId"`
    Output    string `json:"output"`
    IsError   bool   `json:"isError"`
}

type SubagentChannel interface {
    EmitEvent(event ExecutionEvent) error

    // v2: Push tool execution requests to the harness and block for the result.
    // Returns ErrToolExecutionUnsupported if the adapter doesn't support bidirectional dispatch.
    RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error)

    Close()
}

var ErrToolExecutionUnsupported = fmt.Errorf("channel does not support tool execution")
```

### MCP Adapter Changes

```go
// internal/channel/mcp_adapter.go

type MCPSubagentChannel struct {
    progressNotifier ProgressNotifier
    resourceUpdater  ResourceUpdater
    toolDispatcher   ToolDispatcher   // v2: new seam for sampling
    progressToken    any
    taskID           string
    nodeCount        float64
    completed        float64
    mu               sync.Mutex       // v3: concurrency safety
    closed           bool
}

// ToolDispatcher is the seam interface for MCP sampling, avoiding direct SDK dependency.
type ToolDispatcher interface {
    // CreateMessage sends a sampling request to the client and blocks for the response.
    CreateMessage(ctx context.Context, systemPrompt string, userMessage string, maxTokens int64) (string, error)
}

func (ch *MCPSubagentChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
    if ch.toolDispatcher == nil {
        return ToolResponse{}, ErrToolExecutionUnsupported
    }

    reqJSON, _ := json.Marshal(req)
    result, err := ch.toolDispatcher.CreateMessage(ctx,
        "Execute the requested tool and return a JSON response with fields: requestId, output, isError.",
        string(reqJSON),
        4096,
    )
    if err != nil {
        return ToolResponse{}, fmt.Errorf("sampling request failed: %w", err)
    }

    var resp ToolResponse
    if err := json.Unmarshal([]byte(result), &resp); err != nil {
        // Treat raw text as successful output
        return ToolResponse{RequestID: req.RequestID, Output: result}, nil
    }
    return resp, nil
}
```

### Adapter Wiring (cmd/tzro-mcp)

```go
// cmd/tzro-mcp/channel_adapters.go

type sessionToolDispatcher struct {
    session *mcp.ServerSession
}

func (d *sessionToolDispatcher) CreateMessage(ctx context.Context, systemPrompt, userMessage string, maxTokens int64) (string, error) {
    result, err := d.session.CreateMessage(ctx, &mcp.CreateMessageParams{
        SystemPrompt: systemPrompt,
        Messages: []*mcp.SamplingMessage{{
            Role:    mcp.RoleUser,
            Content: &mcp.TextContent{Text: userMessage},
        }},
        MaxTokens: maxTokens,
    })
    if err != nil {
        return "", err
    }
    if tc, ok := result.Content.(*mcp.TextContent); ok {
        return tc.Text, nil
    }
    return "", fmt.Errorf("unexpected content type from sampling response")
}
```

### Capability Detection

The client advertises sampling support during `initialize`:

```json
{
    "capabilities": {
        "sampling": {}
    }
}
```

The `startSubagentChannel` factory checks this and wires the `ToolDispatcher` seam only when sampling is available. Clients without sampling fall through to the existing `ClientToolHook` polling pattern — **full backward compatibility**.

```go
func startSubagentChannel(req *mcp.CallToolRequest, session *mcp.ServerSession, taskID string, nodeCount int) *channel.MCPSubagentChannel {
    // ... existing progress/fallback detection ...

    // v2: Wire tool dispatcher if client supports sampling
    var dispatcher channel.ToolDispatcher
    if session != nil && clientSupportsSampling(session) {
        dispatcher = &sessionToolDispatcher{session: session}
    }

    return channel.NewMCPSubagentChannel(progressNotifier, resourceUpdater, dispatcher, progressToken, taskID, float64(nodeCount))
}
```

### Executor Integration: `ChannelToolHook`

A new `ExecutionHook` that replaces `ClientToolHook` when a SubagentChannel with tool dispatch capability is active.

```go
// internal/channel/hook.go

type ChannelToolHook struct {
    Channel SubagentChannel
}

func (h *ChannelToolHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (executor.HookAction, error) {
    t := tools.GetTool(node.Action)
    if t == nil {
        return executor.ActionContinue, nil
    }
    _, isClientTool := t.(*tools.ClientToolAdapter)
    if !isClientTool {
        return executor.ActionContinue, nil
    }

    // Extract and interpolate arguments (same as ClientToolHook)
    interpolatedPrompt := executor.InterpolateVariables(node.Instructions, taskID)
    toolArguments := executor.ExtractToolArguments(interpolatedPrompt)

    // Push execution to harness via channel
    resp, err := h.Channel.RequestToolExecution(ctx, ToolRequest{
        TaskID:    taskID,
        NodeID:    node.ID,
        ToolName:  node.Action,
        Arguments: toolArguments,
        RequestID: fmt.Sprintf("%s_%s_%d", taskID, node.ID, time.Now().UnixNano()),
    })

    if err == ErrToolExecutionUnsupported {
        // Fall through to ClientToolHook behavior (notification + pause)
        return executor.ActionContinue, nil
    }
    if err != nil {
        return executor.ActionAbort, fmt.Errorf("channel tool execution failed: %w", err)
    }

    if resp.IsError {
        return executor.ActionAbort, fmt.Errorf("client tool returned error: %s", resp.Output)
    }

    // Inject tool output directly — no pause/resume cycle needed
    _ = memory.DB.SetNodeRawOutput(taskID, node.ID, resp.Output)
    return executor.ActionContinue, nil
}
```

### What This Eliminates

| Before (v1 + ClientToolHook) | After (v2 + ChannelToolHook) |
|------|------|
| `ClientToolHook` writes notification | `ChannelToolHook` pushes request via channel |
| Task **pauses** | Task stays running |
| Harness polls `tzro_client_tool_list` | Harness receives `CreateMessage` push |
| Harness calls `tzro_client_tool_submit` | Harness returns `CreateMessageResult` |
| Harness calls `tzro_resume` | Execution continues automatically |
| **3 cloud LLM turns** | **0 cloud LLM turns** |

### Backward Compatibility

- If the client doesn't advertise `sampling` capability → `toolDispatcher` is nil → `RequestToolExecution` returns `ErrToolExecutionUnsupported` → `ChannelToolHook` falls through → `ClientToolHook` activates as before.
- `tzro_register_client_tools`, `tzro_client_tool_list`, `tzro_client_tool_submit` remain available for clients that prefer polling.

### Files NOT Changed

- `internal/executor/executor.go` — Zero changes. Hook interface is sufficient.
- `internal/executor/client_tool.go` — Retained as fallback. Not modified.
- `internal/stream/bus.go` — Zero changes.
- `cmd/tzro-mcp/resources.go` — Zero changes.

---

## v3: Multi-Adapter Transport, Structured Payloads, and Hardening

### 3.1 — Structured Event Payloads

**Problem**: `ChunkToEvent()` currently sets `Message` from `chunk.Content` but never populates `Payload` with typed JSON. The event vocabulary table (v1 spec) defines specific payload schemas per event type that go unused.

**Goal**: Parse `StreamChunk.Content` as JSON when available and populate `ExecutionEvent.Payload` with the typed payload.

#### Payload Schemas

```go
// internal/channel/payloads.go

type TaskStartedPayload struct {
    NodeCount  int `json:"nodeCount"`
    LevelCount int `json:"levelCount"`
}

type TaskCompletedPayload struct {
    SynthesisSnippet string `json:"synthesisSnippet"`
}

type TaskFailedPayload struct {
    Error string `json:"error"`
}

type TaskPausedPayload struct {
    Reason string `json:"reason"`
}

type NodeStartedPayload struct {
    NodeType string `json:"nodeType"`
    Action   string `json:"action"`
}

type NodeCompletedPayload struct {
    NodeType      string `json:"nodeType"`
    OutputSnippet string `json:"outputSnippet"` // truncated to 500 chars
}

type NodeFailedPayload struct {
    Error string `json:"error"`
}

type NodeSkippedPayload struct {
    Reason string `json:"reason"`
}

type EdgeThoughtPayload struct {
    Confidence   float64 `json:"confidence"`
    GoalAchieved bool    `json:"goalAchieved"`
}

type ConfidenceEscalationPayload struct {
    NodeID string `json:"nodeId"`
    Reason string `json:"reason"`
}

type MutationSpawnedPayload struct {
    SpawnedNodeID   string `json:"spawnedNodeId"`
    RemainingBudget int    `json:"remainingBudget"`
}
```

#### Mapping Logic

`ChunkToEvent()` currently inspects `chunk.Content` to parse `node_state` JSON for status fields. v3 extends this to also build typed payloads:

```go
func chunkToEvent(chunk stream.StreamChunk) *ExecutionEvent {
    // ... existing type mapping ...

    switch event.Type {
    case EventNodeStarted:
        payload, _ := json.Marshal(NodeStartedPayload{
            NodeType: parsed.NodeType,
            Action:   parsed.Action,
        })
        event.Payload = payload

    case EventNodeCompleted:
        snippet := parsed.Output
        if len(snippet) > 500 {
            snippet = snippet[:500] + "..."
        }
        payload, _ := json.Marshal(NodeCompletedPayload{
            NodeType:      parsed.NodeType,
            OutputSnippet: snippet,
        })
        event.Payload = payload

    // ... other cases ...
    }

    return &event
}
```

#### Dynamic Total Update

When Bridge sees a `task_started` chunk, it can extract `nodeCount` from the content and call `UpdateTotal()` on the channel. This solves the `nodeCount=0` problem for `tzro_run` where planning hasn't happened at channel creation time.

```go
// internal/channel/channel.go

type SubagentChannel interface {
    EmitEvent(event ExecutionEvent) error
    RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error)
    UpdateTotal(total float64)  // v3
    Close()
}
```

```go
// internal/channel/bridge.go — inside Bridge loop

if event.Type == EventTaskStarted {
    var payload TaskStartedPayload
    if json.Unmarshal(event.Payload, &payload) == nil && payload.NodeCount > 0 {
        ch.UpdateTotal(float64(payload.NodeCount))
    }
}
```

---

### 3.2 — Concurrency Safety

**Problem**: `MCPSubagentChannel` fields `completed`, `closed`, and `nodeCount` are accessed by the Bridge goroutine without synchronization. Safe in v1 (single writer), unsafe with v2 bidirectional dispatch and v3 multi-adapter scenarios.

**Fix**: Add `sync.Mutex` to `MCPSubagentChannel`:

```go
type MCPSubagentChannel struct {
    // ... fields ...
    mu     sync.Mutex
    closed bool
}

func (ch *MCPSubagentChannel) EmitEvent(event ExecutionEvent) error {
    ch.mu.Lock()
    defer ch.mu.Unlock()

    if ch.closed {
        return fmt.Errorf("channel closed")
    }

    if event.Type == EventNodeCompleted {
        ch.completed++
    }
    // ... emit via notifier or updater ...
}

func (ch *MCPSubagentChannel) UpdateTotal(total float64) {
    ch.mu.Lock()
    defer ch.mu.Unlock()
    ch.nodeCount = total
}

func (ch *MCPSubagentChannel) Close() {
    ch.mu.Lock()
    defer ch.mu.Unlock()
    ch.closed = true
}
```

---

### 3.3 — Error Backpressure

**Problem**: `Bridge()` ignores `EmitEvent` errors (`_ = ch.EmitEvent(*event)`). Transport failures are silent.

**Fix**: Add a configurable error policy:

```go
// internal/channel/bridge.go

type BridgeOptions struct {
    Bus             *stream.Bus
    OnEmitError     func(event ExecutionEvent, err error)  // default: log to stderr
    StopOnError     bool                                    // default: false (keep streaming)
}

func BridgeWithOptions(ch SubagentChannel, taskID string, opts BridgeOptions) {
    bus := opts.Bus
    if bus == nil {
        bus = stream.GlobalBus
    }
    onErr := opts.OnEmitError
    if onErr == nil {
        onErr = func(e ExecutionEvent, err error) {
            fmt.Fprintf(os.Stderr, "[Channel Bridge] emit error for %s/%s: %v\n", e.TaskID, e.Type, err)
        }
    }

    sub := bus.Subscribe(func(chunk stream.StreamChunk) bool {
        return chunk.TaskID == taskID
    })
    defer sub.Unsubscribe()

    for chunk := range sub.Ch {
        event := ChunkToEvent(chunk)
        if event != nil {
            if err := ch.EmitEvent(*event); err != nil {
                onErr(*event, err)
                if opts.StopOnError {
                    return
                }
            }
        }
    }
}
```

The existing `Bridge()` function becomes a thin wrapper for backward compatibility:

```go
func Bridge(ch SubagentChannel, taskID string, bus *stream.Bus) {
    BridgeWithOptions(ch, taskID, BridgeOptions{Bus: bus})
}
```

---

### 3.4 — SSE Adapter

For the HTTP dashboard and non-MCP clients.

```go
// internal/channel/sse_adapter.go

type SSESubagentChannel struct {
    writer    http.ResponseWriter
    flusher   http.Flusher
    mu        sync.Mutex
    closed    bool
}

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
    // Deferred — return unsupported.
    return ToolResponse{}, ErrToolExecutionUnsupported
}

func (ch *SSESubagentChannel) UpdateTotal(total float64) {}
func (ch *SSESubagentChannel) Close() {
    ch.mu.Lock()
    defer ch.mu.Unlock()
    ch.closed = true
}
```

#### Dashboard Integration

```go
// internal/server/server.go — new endpoint

func handleTaskSSE(w http.ResponseWriter, r *http.Request) {
    taskID := r.URL.Query().Get("taskId")
    if taskID == "" {
        http.Error(w, "taskId required", 400)
        return
    }

    ch, err := channel.NewSSESubagentChannel(w)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    defer ch.Close()

    // Bridge blocks until bus closes or client disconnects
    channel.BridgeWithOptions(ch, taskID, channel.BridgeOptions{
        StopOnError: true, // Disconnect = stop
    })
}
```

Accessible at: `GET /api/tasks/{taskId}/events` — the dashboard JS connects via `new EventSource(url)`.

---

### 3.5 — Native Plugin Adapter

For in-process Go integrations (Antigravity SDK native plugin mode).

```go
// internal/channel/plugin_adapter.go

type EventCallback func(event ExecutionEvent)
type ToolCallback func(req ToolRequest) (ToolResponse, error)

type PluginSubagentChannel struct {
    onEvent EventCallback
    onTool  ToolCallback  // nil = unsupported
    mu      sync.Mutex
    closed  bool
}

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

func (ch *PluginSubagentChannel) UpdateTotal(total float64) {}
func (ch *PluginSubagentChannel) Close() {
    ch.mu.Lock()
    defer ch.mu.Unlock()
    ch.closed = true
}
```

This adapter allows the Antigravity SDK Go plugin to receive events as in-process function calls with zero serialization overhead, and dispatch tool execution back to the host framework via a callback.

---

## File Changes Summary

### v2 Files

| File | Lines | Purpose |
|------|-------|---------|
| `internal/channel/channel.go` | ~+25 | `ToolRequest`, `ToolResponse`, `RequestToolExecution` on interface, `ErrToolExecutionUnsupported`, `UpdateTotal` |
| `internal/channel/mcp_adapter.go` | ~+40 | `ToolDispatcher` seam, `RequestToolExecution` impl, `sync.Mutex` |
| `internal/channel/hook.go` | ~80 [NEW] | `ChannelToolHook` implementing `ExecutionHook` |
| `internal/channel/channel_test.go` | ~+80 | Tests for bidirectional dispatch, fallback, capability detection |
| `cmd/tzro-mcp/channel_adapters.go` | ~+30 | `sessionToolDispatcher`, sampling capability check |
| `cmd/tzro-mcp/tools.go` | ~+10 | Wire `ChannelToolHook` when sampling available |

### v3 Files

| File | Lines | Purpose |
|------|-------|---------|
| `internal/channel/payloads.go` | ~60 [NEW] | Typed payload structs for all 11 event types |
| `internal/channel/bridge.go` | ~+30 | `BridgeWithOptions`, error policy, `UpdateTotal` on `task_started` |
| `internal/channel/sse_adapter.go` | ~50 [NEW] | SSE adapter for HTTP dashboard |
| `internal/channel/plugin_adapter.go` | ~50 [NEW] | In-process callback adapter for native plugins |
| `internal/channel/channel_test.go` | ~+60 | Tests for SSE, plugin, error backpressure, payload population |
| `internal/server/server.go` | ~+15 | `/api/tasks/{taskId}/events` SSE endpoint |

### Files NOT Changed

- `internal/executor/executor.go` — Zero changes (hook interface sufficient)
- `internal/executor/client_tool.go` — Retained as fallback, not modified
- `internal/stream/bus.go` — Zero changes
- `cmd/tzro-mcp/resources.go` — Existing bridge continues operating in parallel

---

## Migration Path

1. **v2 is additive**: No breaking changes. `SubagentChannel` gets a new method with a default `ErrToolExecutionUnsupported` return. Clients without sampling see identical behavior.
2. **v3 is additive**: `UpdateTotal` is a no-op on adapters that don't track progress. SSE and plugin adapters are opt-in.
3. **Future deprecation**: Once all known harnesses support sampling, `ClientToolHook` and `tzro_client_tool_*` MCP tools can be deprecated (but not removed — MCP Server Mode is a public API).

---

## Verification Plan

### v2 Automated Tests

1. **`TestChannelToolHookBidirectional`** — Mock channel with `RequestToolExecution` returning success → verify node output injected, no pause
2. **`TestChannelToolHookFallback`** — Mock channel returning `ErrToolExecutionUnsupported` → verify `ActionContinue` (falls through to ClientToolHook)
3. **`TestMCPAdapterSamplingDispatch`** — Mock `ToolDispatcher` → verify `CreateMessage` called with correct tool request JSON
4. **`TestSamplingCapabilityDetection`** — Session with/without sampling → verify dispatcher wired/nil
5. **`TestToolRequestSerialization`** — Round-trip `ToolRequest` ↔ JSON

### v3 Automated Tests

6. **`TestStructuredPayloads`** — Verify each event type produces correct typed payload
7. **`TestDynamicTotalUpdate`** — Verify `UpdateTotal` propagates to subsequent `NotifyProgress` calls
8. **`TestConcurrencySafety`** — Parallel goroutines calling `EmitEvent` and `Close` → no race (run with `-race`)
9. **`TestBridgeErrorBackpressure`** — Channel that returns errors → verify `OnEmitError` callback fires
10. **`TestBridgeStopOnError`** — Channel returns error with `StopOnError: true` → verify bridge exits
11. **`TestSSEAdapter`** — `httptest.ResponseRecorder` → verify SSE event format (`event: X\ndata: Y\n\n`)
12. **`TestPluginAdapter`** — Callback captures events → verify all events received in order

### Build Verification

```bash
go build ./...
go test -race ./internal/channel/... ./cmd/tzro-mcp/...
```

### Manual Verification

- Run `tzro_run` from Antigravity with a workflow containing a client tool
- Verify tool execution happens inline via sampling (no pause/resume cycle)
- Open dashboard → verify SSE events stream in real-time
- Run the same workflow without sampling support → verify fallback to polling pattern
