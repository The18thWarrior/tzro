# Use Case: MCP Server Mode Integration

**Actor**: Developer or AI Agent Host (e.g., Claude Desktop, Hermes Agent) consuming tzro tasks.
**Route**: MCP stdio protocol (`mcp://stdio`)
**Backend**: http://localhost:36888 (stdio JSON-RPC bridge)
**Priority**: P1

---

## Intent

A developer or AI agent host wants to run the `tzro-mcp` server to expose tzro's durable task execution capabilities as standard MCP tools over a stdio pipe. This enables the client host to trigger durable tasks, inspect task status, list historical tasks, and dynamically configure the tool registry.

## Preconditions

- The `tzro-mcp` binary is compiled and accessible.
- SQLite database is initialized (in WAL mode to support concurrent connections).
- Pluggable inference backend is configured and reachable.

## Success Criteria

- [ ] MCP client connects successfully and lists all tools, including: `tzro_run`, `tzro_status`, `tzro_list_tasks`, `tzro_configure_tools`, `tzro_web_search`, `tzro_memory_query`, `tzro_memory_ingest`, `tzro_skills_add`, `tzro_hook_approve`, `tzro_model_list`, `tzro_model_set`, `tzro_completion`, `tzro_classification`, `tzro_code`, `tzro_rag_context`, `tzro_kg_add_entity`, `tzro_kg_neighborhood`.
- [ ] Calling `tzro_run` triggers durable graph planning and execution.
- [ ] If execution completes within the timeout, `tzro_run` returns the final task status and outputs.
- [ ] If execution exceeds the timeout, `tzro_run` returns a taskId and continues execution asynchronously.
- [ ] Calling `tzro_status` with a valid taskId returns the task's state, nodes, and step logs.
- [ ] Calling `tzro_list_tasks` returns a list of all current and historical task execution records.
- [ ] Calling `tzro_configure_tools` registers new tools dynamically or updates existing tool configurations.
- [ ] Tool configurations registered via `tzro_configure_tools` persist in the SQLite database across server restarts.
- [ ] Calling `tzro_web_search` returns ranked results from multiple search engines using tiered fallback logic.
- [ ] Calling `tzro_completion` correctly runs a prompt against the local GGUF sidecar with optional GBNF JSON schema rules.
- [ ] Calling `tzro_classification` uses the local model and GBNF schema to output exactly one of the requested class labels.
- [ ] Calling `tzro_model_list` and `tzro_model_set` successfully queries and switches the active local GGUF models.
- [ ] Standalone server stdout logs are successfully redirected to stderr or a log file, keeping the JSON-RPC pipe clean.
- [ ] Calling `tzro_code` generates or updates a single file using the local model with full or diff mode.
- [ ] The MCP UI frontend serves an HTML application at the dashboard endpoint with real-time task visualization.
- [ ] Task progress is tracked in real-time via injected metadata and event buffering.
- [ ] The event buffer captures task lifecycle events (start, node progress, completion) and streams them to the UI.
- [ ] The UI displays node-level detail views for probe, analyze, code, synthesis, and generic node types.
- [ ] The UI includes a DAG graph visualization showing node dependencies and execution status.
- [ ] Resource subscriptions (`tzro://tasks/{taskId}/output`) push notifications when task states update.

## Edge Cases to Probe

- Calling `tzro_status` with a non-existent or malformed taskId.
- Calling `tzro_run` with an empty prompt or invalid tool schema arguments.
- Running concurrent tool calls to verify SQLite WAL multi-connection stability.
- Starting the server when the configured inference backend is unreachable.
- Requesting a model set to an invalid download URL or a non-existent model ID.
- Calling classification with empty categories list or malformed prompts.
- Opening the MCP UI dashboard while no tasks are running — verify the page renders cleanly.
- Multiple MCP clients subscribing to the same task resource URI — verify all receive updates.
- `tzro_code` called with a filepath that doesn't exist yet (new file) — verify `mode: "full"` creates it.
- Event buffer receives 1000+ events in 1 second — verify no dropped events or memory overflow.


## Anti-Patterns to Watch For

- [ ] Server prints debug logs, warning messages, or error traces to `stdout`, corrupting the JSON-RPC stream.
- [ ] The MCP connection hangs indefinitely when a long-running graph execution is active.
- [ ] Tool configuration changes are only stored in-memory and lost when the MCP process is restarted.
- [ ] Standard library logs or panics bypass redirection and crash the JSON-RPC channel.
- [ ] The MCP UI frontend serves stale state after a task completes — event buffer not flushed.
- [ ] Resource subscription notifications are sent for tasks the client is not subscribed to.
- [ ] The event buffer leaks goroutines for each connected UI client.
- [ ] `tzro_code` overwrites a file without reading the existing content when `mode: "diff"` is specified.

