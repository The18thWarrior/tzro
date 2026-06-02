# Deep Task Engine seam and domain separation

We separate the Workflow Orchestration domain (tracking running execution state, storing workflow memory, managing execution events) from the Task Planning and Compilation domain (LLM DAG planning, heuristic graph compilation, Kahn topological sorting, parallel step dispatch).

We establish a deep Task Engine seam exposing a single high-leverage entry point:
`task.Execute(ctx, prompt, opts)`

And a consolidated graph-planning utility:
`task.Plan(ctx, prompt, opts)`

The workflow orchestrator (`runWorkflowLoop`) no longer depends on, compiles, or executes DAG graphs directly. It strictly calls `task.Execute`, which encapsulates:
1. LLM planning or heuristic compilation fallbacks
2. Kahn topological sorting (level-by-level parallel scheduling)
3. Parallel execution and state updates

Similarly, the HTTP server `/api/chat` handler now delegates DAG planning entirely to `task.Plan`, completely removing duplicate cloud/heuristic planning implementations.

## Design Outcomes

- **High Locality**: All DAG compilation and execution details reside entirely within `internal/task`, `internal/compiler`, and `internal/executor`.
- **Minimal Coupling**: The orchestrator (`internal/workflow`) no longer imports `internal/compiler`, `internal/executor`, or provider configuration. It communicates solely through memory structures and the deep `task` seam.
- **Unified Planning**: The cloud and heuristic planner are unified in a single module, eliminating duplicate/diverged implementations between the Chat API handler and the workflow background loops.
- **Enhanced Testability**: We introduced isolated TDD tests in `internal/task/task_test.go` checking planner fallbacks and topological level generation without invoking running execution sidecars.

## Considered Options

- **Deep Seam via Service Interface**: Introduce a `TaskService` interface with mock bindings. Rejected as premature; a direct package function `task.Execute` is simpler, cleaner, and has perfect encapsulation for current requirements.
- **Partial Separation**: Keep heuristic planning in `server.go` and cloud planning in `orchestrator.go`. Rejected — leads to code duplication, design drift, and increased maintenance overhead.
