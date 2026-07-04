---
name: tzro-batch
description: Batch-execute multiple code generation tasks in parallel using tzro_code, with cloud-model planning and review. Use when user wants to implement a multi-file feature, batch code changes, parallel codegen, or says "batch", "implement this feature", "update these files", or "generate all of these".
---

# tzro Batch Codegen

Orchestrate multiple `tzro_code` calls in parallel with a **Cloud Supervisor / Local Worker** loop.

## Quick Start

When the user describes a feature that touches multiple files:

1. **Decompose** the feature into atomic file-level tasks
2. **Submit** all tasks as parallel `tzro_code` calls
3. **Await** all tasks, then **review** results
4. **Fix-up** any issues with targeted follow-up tasks
5. **Verify** with build/test, then present final diff

## Workflow

### Phase 1 — Decompose (You, the cloud model)

Break the user's request into independent, atomic file tasks. Each task targets **one file** with a **self-contained spec**.

**Checklist:**
- [ ] Each task has: `spec`, `filepath`, `language` (optional)
- [ ] Tasks are independent — no task depends on another's output
- [ ] Spec includes enough context (function signatures, types, imports) for the local model
- [ ] For `update` tasks, mention the specific functions/sections to modify

**Output a task table for the user to review before executing:**

```markdown
| # | File | Action | Spec Summary |
|---|------|--------|-------------|
| 1 | `internal/handlers/list.go` | update | Add limit/offset params to ListUsers |
| 2 | `internal/handlers/list_test.go` | update | Add pagination test cases |
| 3 | `internal/repository/queries.go` | update | Add LIMIT/OFFSET to SQL queries |
```

### Phase 2 — Parallel Execute (tzro_code)

Submit **all tasks simultaneously** as parallel `tzro_code` calls:

```
tzro_code({ spec: "...", filepath: "/abs/path/to/file.go" })
tzro_code({ spec: "...", filepath: "/abs/path/to/other.go" })
tzro_code({ spec: "...", filepath: "/abs/path/to/test.go" })
```

> **IMPORTANT:** Call all `tzro_code` invocations in a single parallel tool-call block.
> Do NOT wait for one to finish before starting the next.

After submitting, set a wakeup timer:

```
schedule({ DurationSeconds: 120, Prompt: "Check batch codegen task results" })
```

### Phase 3 — Await & Collect

When the timer fires (or tasks complete), check each task's status:

```
tzro_status({ taskId: "task_id_1" })
tzro_status({ taskId: "task_id_2" })
```

Collect the `terminal_synthesis` output from each completed task.

### Phase 4 — Review (You, the cloud model)

Read all generated/modified files and check:

- [ ] **Cross-file consistency** — do function signatures match across caller/callee?
- [ ] **Import correctness** — are all needed imports present?
- [ ] **Type alignment** — do struct fields, interfaces match?
- [ ] **Test coverage** — do tests actually test the new code paths?

If issues are found, generate **fix-up tasks** — small, targeted `tzro_code` calls with precise specs:

```
tzro_code({
  spec: "Fix ListItems call on line 47: add ctx as first parameter to match queries.ListItems(ctx, limit, offset) signature",
  filepath: "/abs/path/to/handlers/list.go"
})
```

### Phase 5 — Verify & Present

Run `go build ./...` and `go test ./... -count=1` (or equivalent). If tests fail, extract the error, generate a fix-up task, and repeat Phase 4 (max 2 fix-up cycles).

Once passing, show the user: files modified/created, key decisions, test results, and any manual review items.

## Spec Writing & Convergence

See [REFERENCE.md](REFERENCE.md) for spec writing tips, good/bad examples, and convergence rules.
