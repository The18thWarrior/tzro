# Use Case: Stdio-based MCP Host Integration

**Actor**: Developer configuring integrations to external services.
**Route**: CLI / Web UI config integrations page
**Backend**: http://localhost:36888/api/mcp
**Priority**: P1

---

## Intent

A developer wants to register and configure a third-party stdio-based tool server as an MCP Host, exposing its dynamic tool catalog to the local tactician model so that the agent can execute commands on the external system.

## Preconditions

- The external tool server executable (e.g., node command, python script) is installed on the host.
- The `tzro` daemon has sufficient system permissions to spawn child processes.
- The system configuration contains the MCP Host details.

## Success Criteria

- [ ] Developer sees the external MCP Host process spawn successfully as a child process via standard I/O (stdio).
- [ ] Developer sees the agent fetch the external server's tool catalog on startup.
- [ ] External tools are registered and displayed correctly in the TUI/Web tool lists.
- [ ] When the agent invokes an MCP tool, parameters are checked against the tool's JSON schema using GBNF grammar constraints.
- [ ] The child process handles secrets securely by resolving delegated secrets prefixed with `$` from environment variables.
- [ ] Shutting down the tzro daemon gracefully terminates all spawned MCP Host child processes.

## Edge Cases to Probe

- Spawning an MCP Host that doesn't exist or is not in the system path, verifying clean recovery.
- External MCP Host process crashing unexpectedly during a tool call execution.
- Sending a huge payload (over 10MB) to the MCP server to verify stdio buffering.
- Resolving a delegated secret when the required environment variable is completely missing.

## Anti-Patterns to Watch For

- [ ] MCP Host child processes remain as "zombie" processes after the main tzro daemon exits.
- [ ] Secrets (like API keys) are printed in plain-text logs or stored unencrypted in the DB configuration.
- [ ] A tool execution hangs forever if the MCP server fails to respond.
- [ ] GBNF logit constraint validation blocks standard non-tool agent generations.
