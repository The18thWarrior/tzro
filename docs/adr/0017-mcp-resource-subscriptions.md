# MCP Resource Subscriptions

We introduce dynamic Model Context Protocol (MCP) Resource Subscriptions to enable external agent hosts (such as Cursor or Claude Desktop) to subscribe to real-time execution streams and retrieve node-level task outputs without relying on blocking synchronous calls or polling interfaces.

## Context

When `tzro` operates in **MCP Server Mode**, it exposes tools to compile and execute tasks. Because task execution is designed to be durable and asynchronous (especially under complex planning, human approval gates, or remote sidecar loads), blocking standard tool requests (like `tzro_run`) for long durations causes client-side timeout failures.

While polling `tzro_status` is functional, it is inefficient. Implementing MCP resource templates enables clients to establish event-driven resource subscriptions over the standard stdio JSON-RPC channel.

## Proposed Decisions

### 1. Hierarchical URI Scheme
Expose dynamic task data using two hierarchical Resource URI templates:
*   `tzro://tasks/{taskId}/output` - Represents the consolidated Task state and final synthesis response.
*   `tzro://tasks/{taskId}/nodes/{nodeId}/output` - Represents intermediate outputs for individual execution steps (nodes).

### 2. In-Memory Session Management
Keep resource subscription entries purely in-memory in the `tzro-mcp` process namespace. Because stdio JSON-RPC connections are coupled directly to the life of the subprocess spawned by the parent host client, if the parent client kills the server, old subscription states are automatically discarded. This avoids database table bloat.

### 3. Dual-Path Event Sourcing
Wire the MCP notification bridge as a dual-path event bridge:
*   **Daemon SSE Connection (Primary)**: If `tzrod` is active on `localhost:8080`, connect to `/api/events` to stream execution updates triggered from any interface (web control dashboard, CLI, or MCP).
*   **In-Process GlobalBus (Fallback)**: If the daemon is offline, subscribe directly to the local in-process `stream.GlobalBus` to dispatch events from tasks executed directly by `tzro-mcp`.

### 4. Content-Aware Hybrid Compaction
By default, resource read requests return the lightweight compacted output summary. If the client wants the raw, un-compacted output (which may be large and cached on disk), they can query it by requesting the URI with the query parameter `format=raw` (e.g. `tzro://tasks/{taskId}/nodes/{nodeId}/output?format=raw`), which fetches the full envelope from the database/cache directly.

## Considered Options

*   **Durable SQLite Subscriptions**: Store subscriptions in `tzro.db` so they persist across process restarts. Rejected — stdio MCP sessions are fundamentally transient. If the parent agent restarts the process, the socket is destroyed, making old database subscriptions orphaned and useless.
*   **Local-Only Events**: Only support streaming tasks started inside `tzro-mcp`. Rejected — many tasks are triggered from the CLI or dashboard; the user expects their parent agent to be aware of all tasks regardless of source. Connecting to `/api/events` ensures complete visibility.
