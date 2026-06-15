# SubagentChannel v3

> Completed: 2026-06-14

## Overview

v3 of the SubagentChannel subsystem (`internal/channel/`) adds concurrency safety, structured payloads, error backpressure, and two new channel adapters (SSE + Plugin) to the existing MCP adapter from v2.

## Components

### Concurrency Safety
- `MCPSubagentChannel` now uses `sync.Mutex` to guard `EmitEvent`, `Close`, and `UpdateTotal`
- All mutable fields (`completed`, `closed`, `nodeCount`) are protected
- Verified with `go test -race`

### UpdateTotal (Dynamic Progress)
- Added `UpdateTotal(total float64)` to the `SubagentChannel` interface
- Solves the 0/0 progress bar bug where the DAG node count isn't known at channel creation
- `Bridge` automatically calls `UpdateTotal` when a `task_started` chunk arrives with `nodeCount` in its JSON content

### Structured Payloads (`internal/channel/payloads.go`)
11 typed payload structs, one per event type:
- `TaskStartedPayload`, `TaskCompletedPayload`, `TaskFailedPayload`, `TaskPausedPayload`
- `NodeStartedPayload`, `NodeCompletedPayload`, `NodeFailedPayload`, `NodeSkippedPayload`
- `EdgeThoughtPayload`, `ConfidenceEscalationPayload`, `MutationSpawnedPayload`

`ChunkToEvent` now parses `chunk.Content` as JSON and populates `event.Payload` with the typed payload. Graceful degradation: if Content isn't JSON, Payload stays nil.

`NodeCompletedPayload.OutputSnippet` is truncated to 500 chars with `...` suffix.

### Error Backpressure (`BridgeWithOptions`)
- New `BridgeOptions` struct with `OnEmitError` callback and `StopOnError` flag
- `BridgeWithOptions(ch, taskID, opts)` replaces the internal logic
- `Bridge(ch, taskID, bus)` remains as a backward-compatible thin wrapper

### SSE Adapter (`internal/channel/sse_adapter.go`)
- `SSESubagentChannel` streams events via Server-Sent Events protocol
- Format: `event: {type}\ndata: {json}\n\n`
- Sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`
- `RequestToolExecution` returns `ErrToolExecutionUnsupported` (SSE is unidirectional)

### Plugin Adapter (`internal/channel/plugin_adapter.go`)
- `PluginSubagentChannel` for in-process Go integrations
- Events delivered as direct function calls — zero serialization overhead
- Optional `ToolCallback` for bidirectional dispatch; nil = unsupported

### SSE Dashboard Endpoint
- `GET /api/tasks/events?taskId=X` in `internal/server/server.go`
- Creates an `SSESubagentChannel` and bridges it to `stream.GlobalBus`
- Uses `StopOnError: true` to cleanly exit on client disconnect

## Key Files
- [`channel.go`](file:///Users/jp/Desktop/Repos/tzro/internal/channel/channel.go) — Interface + RecordingChannel
- [`mcp_adapter.go`](file:///Users/jp/Desktop/Repos/tzro/internal/channel/mcp_adapter.go) — MCP adapter with mutex
- [`bridge.go`](file:///Users/jp/Desktop/Repos/tzro/internal/channel/bridge.go) — Event bridging + BridgeWithOptions
- [`payloads.go`](file:///Users/jp/Desktop/Repos/tzro/internal/channel/payloads.go) — 11 typed payload structs
- [`sse_adapter.go`](file:///Users/jp/Desktop/Repos/tzro/internal/channel/sse_adapter.go) — SSE adapter
- [`plugin_adapter.go`](file:///Users/jp/Desktop/Repos/tzro/internal/channel/plugin_adapter.go) — Plugin adapter
- [`server.go`](file:///Users/jp/Desktop/Repos/tzro/internal/server/server.go) — SSE endpoint

## Cross-references
- [Agentic Harness Integration](file:///Users/jp/Desktop/Repos/tzro/docs/wiki/architecture/agentic-harness-integration.md)
- [v2-v3 spec](file:///Users/jp/Desktop/Repos/tzro/docs/superpowers/specs/2026-06-12-subagent-channel-v2-v3.md)
