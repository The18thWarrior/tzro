# Use Case: DAG Execution Hooks and Gating

**Actor**: Developer registering custom lifecycle hooks on the execution engine to intercept, validate, mutate, or pause execution of topological steps.
**Route**: CLI / Web UI / Programmatic SDK
**Backend**: http://localhost:8080/api/tasks
**Priority**: P0

---

## Intent

A developer wants to intercept and control the execution flow of a compiled Directed Acyclic Graph (DAG) before or after level and step boundaries. This allows injecting safety gates (skip/abort), sanitizing sensitive output data (PII masking), or pausing execution durably to wait for Human-in-the-Loop (HITL) authorization.

## Preconditions

- The local Go application uses the `executor.GlobalEngine` module.
- An execution graph task has beenCompiled and topological levels established.
- The `tzro` relational database (SQLite) is initialized to hold durable checkpointed states.

## Success Criteria

- [ ] Developer successfully registers custom types implementing the `BeforeLevel`, `AfterLevel`, `BeforeNode`, or `AfterNode` hook interfaces.
- [ ] Registered hooks trigger deterministically in correct temporal order during `ExecuteGraph` execution.
- [ ] Returning `ActionSkip` from `BeforeNode` skips the targeted step, propagates the skip downstream to dependent nodes, and prunes edges correctly.
- [ ] Returning `ActionPause` yields the custom `ErrTaskPaused` sentinel, checkpoints completed steps, and gracefully yields execution slots.
- [ ] Retrying a paused task correctly resumes execution from the exact checkpointed step without repeating completed nodes.
- [ ] Mutating raw output inside `AfterNode` successfully alters the persisted database payload and compacted context before downstream usage.
- [ ] Returning `ActionAbort` immediately aborts the active level run and registers the failure description.

## Edge Cases to Probe

- Hook logic throws a panic or hangs, verifying that execution logs the failure or enforces a timeout safely.
- Multiple hooks are registered concurrently, ensuring they are executed in order of registration without blocking.
- A hook attempts to write to the active SQLite connection concurrently with the Kahn executor's write operations, verifying thread-safe locking and pool serialization.
- Dynamic skip propagation prunes all remaining steps in a multi-level graph, ensuring the graph completes cleanly in an empty state.

## Anti-Patterns to Watch For

- [ ] Hooks introduce mutex deadlocks during concurrent multi-level node runs.
- [ ] Upstream skipped nodes fail to propagate skip status to dependent downstream nodes, causing execution to run with uninitialized parameters.
- [ ] Sensitive PII data mutated in `AfterNode` is leaked or persisted in its raw form in SQLite tables.
- [ ] A paused task loses its state or re-executes previously completed nodes upon resumption.
- [ ] Returning an abort action leaves database transactions uncommitted or locks the connection pool.
