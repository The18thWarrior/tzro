# Use Case: MCP Server for IDE Integration

**Actor**: An IDE extension (Claude Desktop, VS Code, Cursor) that connects to tzro as an MCP tool server over stdio for local graph execution.
**Route**: CLI `tzro mcp`
**Backend**: `cmd/tzro/mcp.go` — MCPServer, JSON-RPC 2.0 over stdio
**Priority**: P0

---

## Intent

A developer's IDE needs to execute System 1 Graph Calls through tzro without shelling out to the CLI. The IDE connects to `tzro mcp` over stdio and uses the Model Context Protocol (JSON-RPC 2.0) to discover available tools, submit graph execution requests, receive real-time progress notifications, and consume structured results — all within the IDE's native tool-use flow.

## Preconditions

- `tzro` binary is built and available on `PATH`
- IDE extension is configured to launch `tzro mcp` as an MCP server process
- SQLite store is accessible at the default database path
- Workspace directory exists and is writable

## Success Criteria

- [ ] `initialize` request returns server info with protocol version `2024-11-05` and tool capabilities
- [ ] `notifications/initialized` is acknowledged without error
- [ ] `tools/list` returns a tool declaration for `tzro_execute_graph` with a valid JSON schema
- [ ] `tools/call` with `tzro_execute_graph` and a valid graph returns a structured execution result
- [ ] Progress notifications are emitted: 0% at start and 100% at completion
- [ ] Yielded graph execution returns a yield envelope through the MCP response
- [ ] Multiple sequential tool calls on the same stdio connection work correctly
- [ ] `--concurrency` flag configures the underlying executor's max concurrency
- [ ] Invalid JSON-RPC requests return properly formatted error responses
- [ ] Server exits cleanly when stdin is closed (IDE disconnects)

## Edge Cases to Probe

- Malformed JSON on stdin — should return JSON-RPC parse error, not crash
- `tools/call` with an unknown tool name — should return method-not-found error
- Very large graph JSON (hundreds of nodes) — should not exceed memory or timeout
- Rapid sequential requests — should process in order without corruption
- Context cancellation mid-execution — should terminate gracefully

## Anti-Patterns to Watch For

- [ ] JSON-RPC responses interleave with progress notifications on stdout (race condition)
- [ ] Server panics on malformed input instead of returning structured errors
- [ ] Progress notifications contain raw stack traces or internal error details
- [ ] Server continues running after stdin EOF instead of exiting
- [ ] Tool schema in `tools/list` does not match the actual accepted input format
- [ ] Concurrent stdout writes corrupt JSON framing (missing newlines, partial objects)
