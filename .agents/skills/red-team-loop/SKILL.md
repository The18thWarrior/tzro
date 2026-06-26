---
name: red-team-loop
description: Iterative QA loop for tzro execution quality. Run a test execution via tzro_run, evaluate the output against success criteria, identify failure modes, conduct root cause analysis, propose fixes (with user gate), implement, and re-run. Accepts a `max` parameter for cycle cap. Use when user says "red team", "qa loop", "test loop", "run and evaluate", wants to iteratively improve tzro exploration/execution quality, or mentions "red-team-loop".
---

# Red Team Loop

Iterative run → evaluate → diagnose → fix → re-run cycle for tzro execution quality.

## Parameters

- **`max`** (required): Maximum number of full loops before stopping. Passed by the user (e.g., "red team this with max 3"). Default to 3 if not specified.
- **`prompt`**: The tzro task prompt to test. If not given, ask the user.
- **`criteria`**: Success criteria to evaluate against. If not given, infer from the prompt.

## Quick Start

```
User: "Red team the exploration quality — run 'Explore /path/to/repo and explain its architecture' with max 3"
```

## The Loop

For each cycle `i` of `max`:

### Phase 1 — Execute

1. Rebuild the binary if source files changed since last build:
   ```bash
   go build -o bin/tzrod ./cmd/tzrod && go build -o bin/tzro ./cmd/tzro
   ```
2. Restart the daemon via `tzro_restart` (or kill + relaunch if MCP unavailable).
3. Submit the test prompt via `tzro_run` with mode and allowedTools appropriate to the task.
4. Wait for completion using `schedule` + `tzro_status` (do NOT poll — follow the Wait Protocol).
5. Retrieve the `terminal_synthesis` output.

### Phase 2 — Evaluate

Score the output against each success criterion. Produce a scorecard:

```
| # | Criterion                        | Pass/Fail | Evidence                    |
|---|----------------------------------|-----------|-----------------------------|
| 1 | Project correctly identified     | ✅ PASS   | "Go-based agent framework"  |
| 2 | peek_file called at least once   | ❌ FAIL   | 0 calls in thought_chain    |
```

Also inspect the raw thought chain for behavioral signals:
```sql
SELECT step_index, tool_name, substr(thought, 1, 100)
FROM thought_chain WHERE task_id='<id>' ORDER BY step_index
```

Record: tool call distribution, repeated calls, failed paths, confabulated statements.

### Phase 3 — Identify Failure Modes

Cluster failures into categories:
- **Anchoring**: Model drew conclusions from directory names without reading files
- **Tool routing**: Model used the wrong tool (e.g., `list_dir` on a file)
- **Path fabrication**: Model invented paths that don't exist
- **Looping**: Model repeated the same call 3+ times
- **Confabulation**: Model stated facts not grounded in any tool output
- **Context loss**: Compaction dropped critical signals

### Phase 4 — Root Cause Analysis

For each failure mode, trace the causal chain:
1. Which thought chain step first exhibited the failure?
2. What was in the model's context at that point (rolling summary)?
3. What tool output (or lack thereof) triggered the wrong inference?
4. Is the root cause in the **prompt**, the **tool output format**, the **compaction**, or the **model's routing**?

Write findings to an artifact: `red_team_cycle_<i>.md`

### Phase 5 — Propose Fixes ⛔ USER GATE

Present the diagnosis and proposed fixes to the user. **STOP and wait for approval.**

Format the proposal as:
```
## Cycle <i> Findings

### Failure Modes Found
- [list with evidence]

### Proposed Fixes (ranked by leverage)
1. [Fix A] — addresses [failure mode], changes [file(s)]
2. [Fix B] — addresses [failure mode], changes [file(s)]

### Questions
- [Any ambiguities or design choices that need user input]
```

**Do NOT proceed to Phase 6 until the user explicitly approves.** The user may:
- Approve all fixes → proceed
- Approve some fixes → implement only approved ones
- Redirect → adjust approach based on user feedback
- Stop → end the loop early

### Phase 6 — Implement & Verify

1. Implement the approved fixes.
2. Run `go build` to verify compilation.
3. Run relevant unit tests (`go test ./...` or targeted package).
4. If tests fail, fix before proceeding.

Then **return to Phase 1** for the next cycle.

## Loop Termination

Stop when ANY of these conditions is met:
- All `max` cycles exhausted
- All success criteria pass (declare victory)
- User says stop
- Two consecutive cycles produce no new failure modes (plateau)

## Artifacts & Handoffs

Each cycle produces `red_team_cycle_<i>.md`. Final cycle produces `red_team_summary.md`.

For artifact templates, diagnostic SQL queries, failure mode taxonomy, and cross-skill handoffs, see [REFERENCE.md](REFERENCE.md).
