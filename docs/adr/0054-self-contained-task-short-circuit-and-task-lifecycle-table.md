# ADR-0054: Self-Contained Task Short-Circuit and Task Lifecycle Table

## Status

Accepted

## Context

When `tzro_run` receives a prompt that requires no external tool calls (all data inline, pure synthesis), the planning pipeline fails silently. The Strategic Planner (local 4B model) tries to decompose the prompt into a DAG with `allowedTools` per node, but since no tools are needed, it either produces invalid JSON or a graph with hallucinated tool names. SCT expansion fails, `Plan()` returns an error, and the daemon's fire-and-forget goroutine (`server.go:1769`) discards the error via `_, _, _ = task.Execute(...)`. No nodes are written to SQLite, so `tzro_status` returns `"task not found"` and `tzro_list_tasks` never shows the task.

Three contributing factors combine into a silent black hole:

1. **Error discarded**: The daemon goroutine discards the `Execute()` error return. No StreamBus event is emitted on planning failure.
2. **No task-level record**: Task existence is inferred from `node_states` rows. If planning fails before any nodes are persisted, the task is invisible to the entire system.
3. **Planner can't compile tool-less prompts**: The Kahn Compiler expects prompts that decompose into tool-calling nodes. Pure synthesis prompts have no valid tool bindings.

Note: ADR-0048 (Plan Template Registry) describes a template selection mechanism that would address the planner's structural limitations, but it has not been implemented. The planner still generates graphs from scratch. This ADR provides a tactical fix until template selection is built.

### Considered Options

**For detecting self-contained prompts:**

- **Classifier call**: A GBNF-constrained inference call (`needs_tools | self_contained`) before planning. Works for all callers but adds ~1s latency per task.
- **Text heuristic**: Detect inline data via prompt length threshold and absence of action verbs. Zero cost but brittle — false positives on long prompts with tool requirements.
- **Caller hint (`selfContained` flag)**: The MCP caller or CLI user explicitly signals that the prompt is self-contained. Zero inference cost, most honest contract — the caller knows whether tools are needed. Selected.

**For the deterministic graph shape:**

- **New node type (`save_memory`)**: Rejected — contradicts the node type taxonomy. The problem isn't a missing node type, it's a missing task category. `save_memory` has no Thought Chain, no tool calls, and no analogue to Probe/Analyze/Recall.
- **Probe Node in Direct Synthesis mode**: Selected — reuses existing infrastructure. The prompt itself serves as the "pre-compiled context." The Confidence Tier gate handles quality: if the local model's output is insufficient, it escalates to cloud automatically.

## Decision

### 1. `selfContained` Flag on `ExecuteOptions` and MCP Schema

A new optional boolean `selfContained` is added to `TzroRunArgs` (MCP tool schema) and propagated through `ExecuteOptions`. The CLI exposes it as `--self-contained`. When true, `Plan()` bypasses the planner entirely and emits a deterministic graph: a single Probe Node with `DirectSynthesis: true`, using the prompt as the pre-compiled context, with `save_memory` as an allowed tool for memory persistence.

### 2. `tasks` Table for Lifecycle Tracking

A new SQLite table tracks task-level lifecycle:

```sql
CREATE TABLE IF NOT EXISTS tasks (
    task_id TEXT PRIMARY KEY,
    status TEXT DEFAULT 'planning',
    error TEXT DEFAULT '',
    prompt TEXT DEFAULT '',
    created_at INTEGER,
    completed_at INTEGER DEFAULT 0
);
```

`task.Execute()` inserts a row at entry (`status: "planning"`), updates to `"running"` after successful planning, and updates to `"completed"` or `"failed"` at exit. The daemon goroutine captures the error and persists it:

```go
go func() {
    defer recoverTaskPanic(req.TaskID)
    _, _, err := task.Execute(ctx, req.Prompt, execOpts)
    if err != nil {
        memory.DB.UpdateTaskStatus(req.TaskID, "failed", err.Error())
        stream.GlobalBus.Publish(stream.StreamChunk{
            TaskID: req.TaskID, Type: "task_failed",
            Content: err.Error(),
        })
    }
}()
```

`tzro_status` checks `tasks` first for task-level status. `tzro_list_tasks` queries `tasks` directly instead of aggregating `node_states`.

### 3. StreamBus Event on Planning Failure

When `Plan()` fails, a `task_planning_failed` event is emitted on the StreamBus so the MCP server's SSE listener can detect it and return `{"status": "failed", "error": "..."}` instead of timing out to `{"status": "planning"}`.

## Consequences

- `selfContained: true` creates a public MCP API contract. Once AGENTS.md tells callers to use it, removing it is a breaking change.
- The `tasks` table is a schema migration. `GetRecentTasks` can be simplified from a derived-status SQL aggregation to a direct query.
- Planning failures become visible: `tzro_status` returns `{"status": "failed", "error": "SCT expansion failed: ..."}` instead of `"task not found"`.
- Pure synthesis prompts from MCP callers and CLI users will produce results instead of silently failing.
- ADR-0048 (Plan Template Registry) remains the strategic path for handling diverse task categories without caller hints. `selfContained` is the tactical bridge.
