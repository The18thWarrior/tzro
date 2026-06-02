# Use Case: Kahn Task Compilation and Execution

**Actor**: Developer compiling a multi-step orchestration task.
**Route**: CLI / Web UI task compiler endpoint
**Backend**: http://localhost:36888/api/tasks
**Priority**: P0

---

## Intent

A developer wants to submit a complex prompt requiring multi-system tool execution and have the Kahn Compiler compile it into a topological dependency graph (Abstract Graph) that executes parallelizable steps deterministically.

## Preconditions

- The local LLM tactician is loaded and reachable via the Llama-server sidecar.
- Valid tool schemas are registered in the model catalog.
- The `tzro` daemon is online.

## Success Criteria

- [ ] Developer sees the agent formulate a structured plan from natural language instructions.
- [ ] Developer sees the Kahn Compiler run topological sort to organize steps into parallel execution layers.
- [ ] Developer can inspect the compiled Abstract Graph nodes and dependency edges in TUI or Web client.
- [ ] Steps with independent inputs are executed concurrently.
- [ ] Step outputs are forwarded correctly to downstream dependent steps.
- [ ] Developer receives a clear, comprehensive final task execution report with token counts and latency metrics.

## Edge Cases to Probe

- Submitting a prompt that creates a cyclic dependency in steps (e.g. step A depends on B, B depends on A) to verify cycle detection.
- Submitting a task where an intermediate step fails, ensuring graceful failure recovery or state preservation.
- Submitting a massive orchestration plan with over 20 concurrent steps to verify concurrency limit enforcement.
- Triggering step execution with simulated poor network latency to verify robust HTTP retries.

## Anti-Patterns to Watch For

- [ ] Kahn Compiler locks or loops infinitely during cyclic dependency evaluation.
- [ ] Downstream steps execute before their upstream dependencies have fully completed.
- [ ] Data payload leaks or variables are swapped incorrectly between parallel steps.
- [ ] Database locking/deadlock errors when writing concurrently executing step status updates.
- [ ] The task status hangs indefinitely in `running` state if a step process is terminated unexpectedly.
