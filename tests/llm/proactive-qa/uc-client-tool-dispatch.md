# Use Case: Dynamic Client Tool Dispatch

**Actor**: Client host or developer managing local tools that cannot run directly on the tzro server (e.g., frontend-native browser operations, locally authenticated client APIs).
**Route**: MCP stdio protocol / client integration loop
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer or client host wants to register client-side tools that the tzro server cannot execute directly. When a task requires executing these tools, tzro pauses durably at the step, registers the tool invocation request, and waits for the client to execute the tool locally and submit the results back, resuming execution seamlessly.

## Preconditions

- The `tzro-mcp` server is running and connected.
- Task execution graph contains one or more client-registered tools.

## Success Criteria

- [ ] Client successfully registers client-side tools via `tzro_register_client_tools`.
- [ ] Staged client-side tools appear in the registered tool catalog with correct schemas.
- [ ] When execution hits a client tool step, the `ClientToolHook` intercepts it and pauses execution.
- [ ] The task transitions to a paused state and is durably checkpointed to the database.
- [ ] Client retrieves pending client-side tool execution requests via `tzro_client_tool_list`.
- [ ] Client executes the tool locally and submits the result back using `tzro_client_tool_submit`.
- [ ] Submitting the result successfully resumes the task from the exact checkpoint.
- [ ] Sequential client tools pause and resume correctly, updating task state progressively.
- [ ] Parallel client tools in the same Kahn level pause concurrently and resume in any order.

## Edge Cases to Probe

- Submitting tool results for a task that is not paused or does not exist.
- Submitting malformed JSON or empty output for a client tool.
- Restarting the daemon while client tool executions are pending, verifying state remains durably paused.
- Submitting a failure/error output from the client tool and verifying the executor handles it according to the graph policy.

## Anti-Patterns to Watch For

- [ ] The engine attempts to execute client tools locally, leading to immediate execution errors.
- [ ] Staged task state is corrupted or lost during the pause/resume cycle.
- [ ] Task execution remains locked or stuck in a paused state after client submits valid results.
- [ ] Concurrent client tool submissions result in database lock or write serialization issues.
