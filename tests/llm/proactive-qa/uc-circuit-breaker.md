# Use Case: Weighted Circuit Breaker for Task Execution

**Actor**: Developer running multi-node DAG tasks via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888/api/tasks
**Priority**: P1

---

## Intent

A developer wants the execution engine to automatically time-bound task execution based on the composition of the DAG — preventing runaway tasks from consuming resources indefinitely. The circuit breaker calculates a weighted time budget per node type, allows a configurable multiplier, and gracefully terminates remaining nodes while still producing a terminal synthesis from whatever has completed.

## Preconditions

- The `tzro` daemon is running and executing a multi-node task.
- Node types in the DAG have defined time budgets (probe: 10min, action: 5min, deterministic: 90s, synthesis: 90s).
- The `circuitBreakerMultiplier` config value may be set to adjust budget tolerance.

## Success Criteria

- [ ] The executor computes a time budget based on the sum of per-node-type budgets in the DAG.
- [ ] The budget is multiplied by the `circuitBreakerMultiplier` config value (default: 1.0).
- [ ] When the budget expires, remaining pending nodes are marked as `timed_out`.
- [ ] The `terminal_synthesis` node is NOT marked as timed_out — it always runs to produce a final output.
- [ ] A `task_circuit_breaker` telemetry event is published with the budget and number of remaining levels.
- [ ] Already-completed nodes retain their output and status even after the circuit breaker fires.
- [ ] The task still produces a coherent terminal synthesis from the nodes that did complete.
- [ ] Startup logs display the computed budget and multiplier value.

## Edge Cases to Probe

- A single-probe-node task with a 10-minute budget — verify the circuit breaker fires after budget expiry, not before.
- Setting `circuitBreakerMultiplier` to 0 — verify it defaults to 1.0 (not zero-budget instant timeout).
- Setting `circuitBreakerMultiplier` to 3.0 — verify the budget is tripled.
- All nodes complete before the budget — verify no circuit breaker event is published.
- Circuit breaker fires while a node is mid-execution — verify the mid-execution node's state is handled cleanly.

## Anti-Patterns to Watch For

- [ ] Circuit breaker kills the `terminal_synthesis` node, preventing any output from the task.
- [ ] Timed-out nodes are reported as "completed" or "failed" instead of "timed_out".
- [ ] Budget calculation returns 0 or negative for valid graphs, causing immediate termination.
- [ ] Circuit breaker event is missing from telemetry when the breaker fires.
- [ ] A task with only synthesis/deterministic nodes (short budget) times out before it can finish due to underestimated budgets.
