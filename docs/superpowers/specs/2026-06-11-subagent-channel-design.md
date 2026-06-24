# SubagentChannel: Interactive Task Execution Events for MCP Harnesses

## Problem

When an external harness (Antigravity, Claude Code, etc.) calls `tzro_run` or `tzro_workflow`, it receives `{ taskId, status: "running" }` and is immediately blind. The harness must poll `tzro_status` on a timer to discover what happened — wasting cloud tokens, adding latency, and missing time-sensitive events (failures, approval gates, confidence escalations).

The tzro executor already produces rich `StreamChunk` events on `stream.GlobalBus` for every lifecycle transition (node started, completed, failed, edge traversals, mutations). These events never reach the harness. The existing `ResourceUpdated` notification bridge in `resources.go` fires resource-change signals, but most MCP clients don't subscribe to resources.

## Goal

Deliver real-time execution events from the tzro engine to the external harness during task execution, using the MCP protocol's `NotifyProgress` mechanism as the primary transport, with `ResourceUpdated` as a graceful fallback for clients that don't support progress notifications.

The design must be transport-agnostic at the interface level so that future adapters (SSE, native plugin, Antigravity SDK subagent) can be added without modifying the core.

## Non-Goals

- Bidirectional tool execution (tzro pushing work back to the harness) — interface is designed for it, but implementation is deferred.
- Modifying the executor or event bus — the design is purely additive, consuming existing events.
- Replacing the existing `startLocalBridge` / `startSSEBridge` in `resources.go` — those continue operating in parallel.

---

## Architecture

### Layer 1: Existing Event Bus (unchanged)

```
Executor → TelemetryManager → stream.GlobalBus
```

The executor publishes `StreamChunk` events for every lifecycle transition:
- `node_state` — node started/completed/failed/skipped (with JSON payload)
- `task_started` / `task_completed` / `task_failed` / `task_paused`
- `confidence_insufficient` — local→cloud escalation
- `cache_envelope_created` — compaction events

These events are already produced and consumed by the dashboard, observer, and resource bridge. No changes to this layer.

### Layer 2: SubagentChannel Interface (new)

A transport-agnostic contract for delivering execution events from the engine to an external harness.

```go
// internal/channel/channel.go
package channel

import (
    "encoding/json"
    "time"
)

type ExecutionEvent struct {
    TaskID    string          `json:"taskId"`
    NodeID    string          `json:"nodeId,omitempty"`
    Type      string          `json:"type"`
    Message   string          `json:"message"`
    Payload   json.RawMessage `json:"payload,omitempty"`
    Progress  float64         `json:"progress"`
    Total     float64         `json:"total"`
    Timestamp int64           `json:"timestamp"`
}

type SubagentChannel interface {
    EmitEvent(event ExecutionEvent) error
    Close()
}
```

#### Event Vocabulary

| Type | Trigger | Payload |
|------|---------|---------|
| `task_started` | Task execution begins | `{nodeCount, levelCount}` |
| `task_completed` | All levels done | `{synthesisSnippet}` |
| `task_failed` | Fatal error | `{error}` |
| `task_paused` | Hook paused execution | `{reason}` |
| `node_started` | Node begins execution | `{nodeType, action}` |
| `node_completed` | Node finished | `{nodeType, outputSnippet}` |
| `node_failed` | Node errored | `{error}` |
| `node_skipped` | Node skipped by hook/branch | `{reason}` |
| `edge_thought` | Edge thought generated | `{confidence, goalAchieved}` |
| `confidence_escalation` | Local→Cloud escalation | `{nodeId, reason}` |
| `mutation_spawned` | Dynamic node spawned | `{spawnedNodeId, remainingBudget}` |

The `Progress` field increments per node completion. `Total` is set to the total node count when known (0 = unknown). This maps directly to MCP's `ProgressNotificationParams`.

### Layer 3: MCP Adapter (new)

The first adapter implementation. Uses `NotifyProgress` when the client provides a `progressToken` in `_meta`, falls back to `ResourceUpdated` otherwise.

```go
// internal/channel/mcp_adapter.go
package channel

type MCPSubagentChannel struct {
    server        *mcp.Server
    session       *mcp.ServerSession
    progressToken any                 // nil → fallback mode
    taskID        string
    sub           *stream.Subscription
    nodeCount     float64
    completed     float64
}
```

#### Fallback Detection

The MCP spec defines the detection mechanism: the **client sends a `progressToken`** in the tool call's `_meta` field when it wants progress notifications. If it doesn't send one, it doesn't support (or doesn't want) progress.

```
progressToken present in _meta → NotifyProgress (structured events pushed inline)
progressToken absent           → ResourceUpdated (existing behavior, client re-reads resource)
```

This is per-request, not per-session. No capability negotiation needed.

#### NotifyProgress Path

When `progressToken` is present, each execution event maps to:

```go
session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
    ProgressToken: progressToken,
    Message:       fmt.Sprintf("[%s] %s", event.Type, event.Message),
    Progress:      completed,     // monotonically increasing count of completed nodes
    Total:         nodeCount,     // total nodes in graph
})
```

The `Message` field contains a structured `[event_type] description` format. Clients that parse it get typed events; clients that render it get human-readable progress.

#### ResourceUpdated Fallback Path

When `progressToken` is absent, the adapter fires `server.ResourceUpdated()` for the task and node URIs — identical to what `startLocalBridge` in `resources.go` already does. This path exists to ensure that even when the new channel is wired up, clients that don't send progress tokens see no behavioral change.

### Event Bridge (new)

A small function subscribes to `stream.GlobalBus` filtered by `taskID` and forwards matching events through the channel:

```go
// internal/channel/bridge.go
func Bridge(ch SubagentChannel, taskID string) {
    sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
        return chunk.TaskID == taskID
    })
    defer sub.Unsubscribe()

    for chunk := range sub.Ch {
        event := chunkToEvent(chunk)
        if event != nil {
            _ = ch.EmitEvent(*event)
        }
    }
}
```

The bridge runs as a goroutine, started alongside the execution goroutine. It exits when the subscription channel closes (which happens when `ch.Close()` unsubscribes, or when the bus shuts down).

**Chunk-to-Event mapping note:** The executor publishes `StreamChunk` events with type `"node_state"` containing a JSON payload with the actual status (`running`, `completed`, `failed`, `skipped`). The `chunkToEvent` mapper must decompose these into the typed event vocabulary (`node_started`, `node_completed`, `node_failed`, `node_skipped`). Similarly, telemetry-sourced chunks use types like `task_started`, `task_completed`, etc. which map 1:1 to the event vocabulary.

---

## File Changes

### New Files

| File | Lines | Purpose |
|------|-------|---------|
| `internal/channel/channel.go` | ~40 | `SubagentChannel` interface, `ExecutionEvent` type, event type constants |
| `internal/channel/mcp_adapter.go` | ~100 | `MCPSubagentChannel` with progress/resource fallback |
| `internal/channel/bridge.go` | ~50 | `Bridge()` function wiring GlobalBus → channel, `chunkToEvent()` mapper |
| `internal/channel/channel_test.go` | ~150 | Unit tests for interface, adapter, and bridge |

### Modified Files

#### `cmd/tzro-mcp/main.go`
- Store the `*mcp.Server` reference as a package-level variable so tool handlers can create channels.
- ~3 lines added.

#### `cmd/tzro-mcp/tools.go`
- `handleTzroRun`: Extract progress token from `req.Params.GetProgressToken()`, create `MCPSubagentChannel`, start bridge goroutine, close channel on completion.
- `handleTzroWorkflow`: Same pattern.
- ~15 lines added per handler (~30 total).

### Files NOT Changed

- `internal/executor/executor.go` — Zero changes. Events are already published.
- `internal/stream/bus.go` — Zero changes. Subscription mechanism is sufficient.
- `internal/telemetry/telemetry.go` — Zero changes.
- `cmd/tzro-mcp/resources.go` — Zero changes. The existing `startLocalBridge` / `startSSEBridge` continue operating in parallel. The new channel and the old bridge both consume from `GlobalBus` independently.

---

## Future: Bidirectional Tool Execution (Deferred)

The `SubagentChannel` interface is designed to accommodate a future `RequestToolExecution` method:

```go
type SubagentChannel interface {
    EmitEvent(event ExecutionEvent) error
    // Future: push tool execution requests back to the harness
    // RequestToolExecution(req ToolRequest) (ToolResponse, error)
    Close()
}
```

The MCP adapter would implement this via `session.CreateMessage()` (the MCP sampling primitive where the server asks the client to generate a response). This replaces the current `tzro_register_client_tools` / `tzro_client_tool_submit` polling pattern with a push model.

Additional adapters can be built for:
- **SSE**: `EmitEvent` writes `data: {event JSON}\n\n`; bidirectional via REST callback endpoint
- **Native Plugin**: `EmitEvent` calls an in-process Go callback; bidirectional via `hostToolDispatch()`
- **Antigravity SDK**: Maps to the SDK's subagent communication protocol

---

## Verification Plan

### Automated Tests

1. **Unit tests** (`internal/channel/channel_test.go`):
   - `TestMCPChannelWithProgressToken` — verifies `NotifyProgress` is called when token present
   - `TestMCPChannelFallback` — verifies `ResourceUpdated` is called when token absent
   - `TestBridgeFiltering` — verifies only matching taskID events are forwarded
   - `TestBridgeEventMapping` — verifies `StreamChunk` → `ExecutionEvent` conversion
   - `TestProgressCounter` — verifies monotonic progress increment on node completion

2. **Integration test** (`cmd/tzro-mcp/mcp_test.go`):
   - Submit a `tzro_workflow` call with `_meta.progressToken` via stdio
   - Verify progress notifications appear on stdout alongside the final result
   - Submit without progress token, verify no progress notifications but ResourceUpdated fires

3. **Build verification**:
   ```bash
   go build ./...
   go test ./internal/channel/... ./cmd/tzro-mcp/...
   ```

### Manual Verification

- Run `tzro_run` from an MCP client that supports progress (e.g., test harness with progressToken)
- Observe live progress notifications arriving during task execution
- Run the same task without progressToken, verify identical functional behavior (polling still works)
