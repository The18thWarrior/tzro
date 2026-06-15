# Attention and Proactivity Scheduler

The `proactivity` package implements an event-driven background coordinator (`AttentionScheduler`) that schedules, executes, and filters low-priority background daemons under strict resource budgets, foreground priority preemption, and safety policies.

## Design Philosophy
1. **Safety First**: The LLM is used for planning and handling ambiguity, while the Go runtime manages execution, budgets, state, and concurrency.
2. **Foreground Priority**: User-initiated tasks (submitted via chat, CLI, or MCP) always have absolute priority. Background work is preempted immediately upon foreground activity.
3. **Budget Gated**: Background resource usage is governed by both execution-scoped limits and interval-based (hourly) token/CPU/tool call accumulators.
4. **User Control**: Actions with side-effects or high costs require explicit user approval via the Attention Queue.

---

## Proactivity Level Ladder

The system classifies background actions into five levels (`ProactivityLevel`):

| Level | Name | Description | Policy / Approval Requirement |
|---|---|---|---|
| **L0** | `Observe` | State inspection, log reading, telemetry observation. | Safe by default. Runs automatically. |
| **L1** | `Prepare` | Local deterministic preparation (e.g., compaction, cache warming). | Runs automatically if budget permits. |
| **L2** | `Suggest` | Surfaces recommendations/alerts to the user. | Enqueued to Attention Queue. User-visible, no side effects. |
| **L3** | `ReversibleAction` | Performs local reversible mutations under policy guidelines. | Enqueued to Attention Queue. Requires user approval by default. |
| **L4** | `ExternalSideEffect` | Performs costly or externally visible actions (e.g., git commits, publishing). | Enqueued to Attention Queue. Always requires explicit user approval. |

---

## Core Components

### 1. `AttentionScheduler` (`scheduler.go`)
The central coordinator that:
- Subscribes to the `telemetry.TelemetryManager` stream to ingest execution event chunks (failures, latency spikes, cache events).
- Dispatches ingested events to matching registered `Daemon` instances.
- Passes proposed actions through the `SentinelGate` to verify budgets, permissions, and level constraints.
- Enqueues approval-required actions into the `durable_notifications` SQLite table.
- Executes safe actions asynchronously in the background.

### 2. `SentinelGate` (`policy.go`)
Evaluates `ProposedAction`s before execution. Returns a `PolicyDecision` specifying:
- If the action is permitted to run (`Allowed`).
- If the action must be gated behind user confirmation (`ApprovalRequired`).
- A message explaining the logic (`Reason`).

### 3. `BudgetTracker` (`budget.go`)
Measures and limits resource consumption using:
- **Execution Budgets**: Caps CPU time, LLM tokens, and tool calls for a single run to prevent resource hogs.
- **Interval Budgets**: Accumulates CPU execution time, tokens, and tool calls per-interval (resets hourly) to prevent compounding background costs.

### 4. Foreground Registry (`registry.go`)
A thread-safe, in-memory registry (`RegisterActiveUserTask` / `DeregisterActiveUserTask`) tracking active user-initiated task IDs.
- When a foreground task is registered, it immediately triggers the scheduler's registered preemption hook.
- The scheduler cancels all running background contexts, immediately yielding local model/CPU slots.

---

## Built-in Daemons (`daemons.go`)

- **Observer Daemon**: Monitors workflow and tool failures. If a workflow fails two or more times consecutively, it suggests retrying with a smaller context window (L2 Suggestion).
- **Compactor Daemon**: Monitors memory/context pressure. Suggests summarizing and compacting cached disk contents when context capacity is reached (L1 Prepare).
- **Reconciler Daemon**: Monitors plan drift or stale caches to trigger cache refreshes (L1) or suggest plan realignments (L2).
- **Prefetcher Daemon**: Monitors user idle states (`user.idle`) to warm up prompt templates and static context caches (L1).

---

## Verification & Testing

Tests are written in `scheduler_test.go` and use a dedicated local SQLite database (`test_proactivity_scheduler.db`) to isolate notifications during test execution.

To run the unit tests:
```bash
go test -v ./internal/proactivity/...
```
