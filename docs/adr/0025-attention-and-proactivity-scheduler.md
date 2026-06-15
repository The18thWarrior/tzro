# Attention and Proactivity Scheduler

An event-driven, OS-style background coordinator (`AttentionScheduler`) is introduced to schedule, execute, and filter low-priority background daemons (like Observer, Sentinel, Compactor, Prefetcher) under strict resource budgets, foreground preemption, and safety policies. 

It implements a proactivity ladder (L0 Observe, L1 Prepare, L2 Suggest, L3 Reversible Action, L4 External Side Effect) and uses the existing `durable_notifications` system to persist suggestions and approval-gated actions in the Attention Queue.

## Status

Accepted.

## Considered Options

- **Durable Notification Re-use vs. Separate Attention Queue SQLite Table.** We reuse the existing `durable_notifications` table instead of adding a new table and executing database migrations. This ensures immediate persistence, leverages the existing StreamBus SSE event broadcast stream, and integrates seamlessly with existing client UIs.
- **In-Memory Active Task Registry vs. Database State Flag.** We track active user-initiated task IDs (`IsForegroundActive()`) in a thread-safe in-memory registry rather than writing execution flags to SQLite. This avoids database read/write latency on every scheduler check and prevents locking conflicts.
- **Context-based Preemption vs. Thread Interruption.** Running background actions are executed within a cancellable `context.Context`. When a foreground user/MCP task starts, the scheduler cancels all running background contexts, immediately aborting execution and yielding local model/CPU slots to the user.
- **Execution + Interval Resource Budgeting vs. Rolling Window.** Enforce both execution-scoped limits (preventing a single run from consuming all resources) and interval accumulators (e.g. hourly token and tool call limits) to balance short-term safety with long-term cost containment.
- **Telemetry Event Ingestion vs. Custom Dispatching.** The scheduler subscribes to the global `telemetry.TelemetryManager` stream, translating relevant stream chunks (such as workflow or tool failures) into scheduler events, avoiding code pollution in other packages.

## Consequences

- **A new `proactivity` package is created** containing types, the active task registry, policy gate, scheduler loop, and built-in daemons.
- **`task.ExecuteOptions` is extended** with `IsForeground bool`. User-initiated entrypoints (chat endpoint, MCP tool `tzro_run`) set this to true, triggering automatic registry/deregistration and background preemption.
- **Background work is completely safe and budgeted.** Daemons cannot invoke side-effects or LLMs directly; they propose actions that pass through the Sentinel Gate and, if needed, wait in the Attention Queue for explicit user approval before execution.
