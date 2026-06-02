# Synchronous DAG Execution Hooks

Execution hooks provide developers with a synchronous, thread-safe middleware mechanism to intercept, validate, mutate, or pause execution of Kahn-sorted Directed Acyclic Graph (DAG) tasks in `tzro`.

---

## Context & Motivation

In previous versions of `tzro`, executing a compiled task was a closed-loop system:
1. The **Kahn Compiler** sorted nodes into levels.
2. The **Go Graph Executor** dispatched them concurrently in goroutines.
3. Node status updates and telemetry events were published asynchronously over the `StreamBus`.

While asynchronous telemetry is perfect for monitoring and UI updates, it is completely non-blocking. Developers had no way to:
* **Sanitize or redact sensitive tool outputs** (e.g., PII scrubbing) before the data was stored in the SQLite checkpointer database or fed into downstream steps.
* **Inject runtime environment credentials** immediately prior to execution, avoiding persisting secrets inside graph state configurations.
* **Establish synchronous safety guardrails** (e.g., stopping execution if parameter constraints were violated).
* **Enact Human-in-the-Loop approval gates** (T2 Supervised tier) to durably pause active tasks between topological level transitions.

Execution hooks solve these limitations by providing direct, structured hook endpoints inside the core execution lifecycle loop.

---

## Technical Architecture

The hooks system is defined directly inside `internal/executor/executor.go` and integrated directly into the `ExecuteGraph` and `executeSingleNode` processing paths.

### 1. The `ExecutionHook` Interface

```go
type HookAction string

const (
	ActionContinue HookAction = "continue" // Proceed with execution
	ActionSkip     HookAction = "skip"     // Bypass the current node/level and propagate skip downstream
	ActionPause    HookAction = "pause"    // Pause task execution and yield ErrTaskPaused
	ActionAbort    HookAction = "abort"    // Interrupt level execution and fail the task
)

type ExecutionHook interface {
	// BeforeLevel intercepts level execution before concurrent steps are launched
	BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)

	// AfterLevel executes after all concurrent steps in a level finish processing
	AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)

	// BeforeNode runs immediately before a single node begins execution
	BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error)

	// AfterNode runs immediately after a tool call completes, enabling output mutation
	AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error)
}
```

### 2. Lifecycles & Integration Flow

Execution hooks are integrated into `ExecutionEngine` and process in the following sequential order:

```
[ExecuteGraph]
      │
      ▼
[Fetch activeHooks via getHooksUnlocked()]
      │
      ▼
┌────────────────────────────────────────────────────────┐
│ Loop through Kahn Topological Levels                   │
│ ────────────────────────────────────────────────────── │
│ 1. Trigger BeforeLevel hooks on levelNodes             │
│    - Handles Pause, Skip (marks nodes & propagates),   │
│      or Abort actions.                                 │
│ 2. Launch concurrent level step goroutines:            │
│    ┌─────────────────────────────────────────────────┐ │
│    │ Goroutine for single nodeID                     │ │
│    │ ─────────────────────────────────────────────── │ │
│    │ a. Trigger BeforeNode hooks                     │ │
│    │ b. Run tool call / Local Model extraction       │ │
│    │ c. Trigger AfterNode hooks (allows mutating     │ │
│    │    rawOutput inline)                            │ │
│    │ d. Handle Compaction, Caching, & Checkpoints    │ │
│    └─────────────────────────────────────────────────┘ │
│ 3. Wait for goroutine pool completion                  │
│ 4. Handle ErrTaskPaused or Level Errors                │
│ 5. Trigger AfterLevel hooks on levelNodes              │
└────────────────────────────────────────────────────────┘
```

---

## Key Features

### Concurrency & Thread Safety
Since steps within a single Kahn level execute concurrently inside goroutines, any registered `BeforeNode` and `AfterNode` hooks will execute in parallel.
To avoid race conditions or lock contention, the engine retrieves a copied snapshot of the registered hooks slice exactly once at the beginning of `ExecuteGraph` (`e.getHooksUnlocked()`), and thread-safely passes the immutable hooks slice down to the goroutines and `executeSingleNode` calls, ensuring 100% deadlock-proof execution.

### Durable Resumption (`ActionPause`)
If any hook returns `ActionPause` (e.g. waiting for user confirmation):
1. The executor immediately halts execution of the active topological level.
2. Completed steps are saved and checkpointed in SQLite under their `completed` status.
3. The executor yields a concrete `ErrTaskPaused` sentinel error back to the caller.
4. The background task daemon de-allocates context and local memory slots.
5. Once resumed, the Kahn compiler naturally skips previously completed steps and resumes execution from the first incomplete level, preserving local memory and hardware performance.

### Data Mutation (`AfterNode`)
The `AfterNode` hook receives a string pointer (`*string`) to the tool's raw output. Modifying this pointer:
* Dynamically overwrites the raw tool response.
* Ensures subsequent context compaction (`cache.Process`) and SQLite checkpoints record the sanitized payload.
* Preserves secure, clean variable interpolation for downstream nodes that reference `{{nodes.node_id.output}}`.

---

## Verification & Testing

The hook suite is fully covered by Go unit tests located in `internal/executor/executor_hooks_test.go`:
* `TestHooksSequencing`: Verifies that `BeforeLevel` -> `BeforeNode` -> `AfterNode` -> `AfterLevel` events fire in the exact sequential order.
* `TestHooksActionSkipAndPropagation`: Validates that skipping a node via hooks propagates skips downstream and does not run tool calls.
* `TestHooksActionAbort`: Assures that abort actions halt engine execution and report clean failures.
* `TestHooksActionPauseAndResume`: Confirms the durable yield pattern by pausing on a middle level, verifying checkpoint persistence, and completing the graph run upon resumption.
* `TestHooksOutputMutation`: Verifies that mutating the `rawOutput` string pointer in `AfterNode` is correctly reflected in SQLite raw output state checkpoints.
* `TestHooksConcurrencySafety`: Launches multiple parallel tasks utilizing registered hooks under high goroutine load to verify thread-safety and SQLite transaction locking stability.
