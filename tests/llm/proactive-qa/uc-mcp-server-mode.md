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

- [ ] MCP client connects successfully and lists the core tools: `tzro_run`, `tzro_status`, `tzro_list_tasks`, `tzro_configure_tools`.
- [ ] Calling `tzro_run` triggers durable graph planning and execution.
- [ ] If execution completes within the timeout, `tzro_run` returns the final task status and outputs.
- [ ] If execution exceeds the timeout, `tzro_run` returns a taskId and continues execution asynchronously.
- [ ] Calling `tzro_status` with a valid taskId returns the task's state, nodes, and step logs.
- [ ] Calling `tzro_list_tasks` returns a list of all current and historical task execution records.
- [ ] Calling `tzro_configure_tools` registers new tools dynamically or updates existing tool configurations.
- [ ] Tool configurations registered via `tzro_configure_tools` persist in the SQLite database across server restarts.
- [ ] Standalone server stdout logs are successfully redirected to stderr or a log file, keeping the JSON-RPC pipe clean.

## Edge Cases to Probe

- Calling `tzro_status` with a non-existent or malformed taskId.
- Calling `tzro_run` with an empty prompt or invalid tool schema arguments.
- Running concurrent tool calls to verify SQLite WAL multi-connection stability.
- Starting the server when the configured inference backend is unreachable.

## Anti-Patterns to Watch For

- [ ] Server prints debug logs, warning messages, or error traces to `stdout`, corrupting the JSON-RPC stream.
- [ ] The MCP connection hangs indefinitely when a long-running graph execution is active.
- [ ] Tool configuration changes are only stored in-memory and lost when the MCP process is restarted.
- [ ] Standard library logs or panics bypass redirection and crash the JSON-RPC channel.
