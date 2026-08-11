# ADR-0063: Foreground-Priority Compute Preemption

**Status**: Accepted  
**Date**: 2026-07-29  
**Context**: Background processes (Sentinel, Observer, daemons) were competing with user-initiated foreground tasks for the single-slot local inference sidecar and CPU resources.

## Decision

Implement a multi-layer preemption system that ensures foreground tasks (CLI/MCP-initiated) have absolute compute priority over background processes.

### Layer 1: Inference Admission Gate (proactivity.WaitForForegroundClear)

A `sync.Cond`-based blocking primitive in the proactivity registry. Background tasks call `WaitForForegroundClear(ctx)` before acquiring compute resources. When `DeregisterActiveUserTask` empties the foreground registry, it broadcasts to wake all blocked background callers.

### Layer 2: Sentinel Full-Cycle Deferral

The Sentinel's `evaluateHeartbeat()` now checks `IsForegroundActive()` at the **top** of the method — before workspace scanning and semantic retrieval — not just before LLM synthesis. This prevents I/O and CPU consumption during foreground activity.

### Layer 3: Background Task Queuing Gate

`ExecuteGraphReactive` now checks `graph.IsForeground` before acquiring the engine mutex. Background tasks (`IsForeground == false`) block on `WaitForForegroundClear` before proceeding. Foreground tasks pass through immediately.

### Layer 4: Automatic KV Cache Preemption

The inference package registers preemption/resume callbacks via `init()`:
- **Preemption**: Calls `PreemptForChat` to save background KV cache state to disk
- **Resume**: Calls `RestoreAfterChat` to reload the cache after foreground completes

### Layer 5: CLI Foreground Default

The daemon REST API (`handleTasksRun`) now defaults CLI-submitted tasks to `IsForeground: true`. Previously all REST API tasks were `IsForeground: false`, meaning CLI-initiated tasks were incorrectly treated as background.

## Consequences

- Background tasks may experience increased latency when foreground tasks are active (by design)
- Scheduled/cron tasks remain `IsForeground: false` — they wait for interactive tasks
- The `IsForeground` field on `ExecutionGraph` is additive and backward compatible (JSON `omitempty`)
- No import cycles: `inference` → `proactivity` (new), existing `executor` → `proactivity` preserved
