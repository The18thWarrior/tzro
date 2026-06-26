# Red Team Loop — Reference

## Diagnostic SQL Queries

These queries run against `tzro.db` to extract behavioral signals from a completed task.

### Tool Call Distribution
```sql
SELECT tool_name, COUNT(*) as calls
FROM thought_chain
WHERE task_id = '<TASK_ID>'
GROUP BY tool_name
ORDER BY calls DESC;
```

### Repeated Identical Calls (Looping Detection)
```sql
SELECT tool_name, tool_args, COUNT(*) as repeats
FROM thought_chain
WHERE task_id = '<TASK_ID>'
GROUP BY tool_name, tool_args
HAVING repeats >= 3
ORDER BY repeats DESC;
```

### Failed Tool Calls
```sql
SELECT step_index, tool_name, tool_args, substr(tool_output, 1, 200)
FROM thought_chain
WHERE task_id = '<TASK_ID>'
  AND tool_output LIKE '%"success":false%'
ORDER BY step_index;
```

### Rolling Summary at Each Step (Context Pressure)
```sql
SELECT step_index, LENGTH(thought) as thought_len, tool_name
FROM thought_chain
WHERE task_id = '<TASK_ID>'
ORDER BY step_index;
```

### Compaction Summaries
```sql
SELECT step_index, substr(thought, 1, 300)
FROM thought_chain_summaries
WHERE task_id = '<TASK_ID>'
ORDER BY step_index;
```

## Success Criteria Templates

### Exploration Task
| # | Criterion | How to Check |
|---|-----------|--------------|
| 1 | Project language correctly identified | Search terminal_synthesis for language name |
| 2 | Key frameworks/build system named | Check for go.mod, Cargo.toml, pyproject.toml, package.json |
| 3 | Source code actually read | Count `read_file` and `peek_file` calls on source files |
| 4 | No confabulated implementation details | Cross-reference claims against tool outputs |
| 5 | Passive agents/services identified | Check for observer, sentinel, proactivity mentions |
| 6 | Tool call efficiency | No tool called with same args 3+ times |

### DAG Workflow Task
| # | Criterion | How to Check |
|---|-----------|--------------|
| 1 | All planned nodes completed | `tzro_status` shows all nodes `completed` |
| 2 | Correct tool selection per node | Check each node's tool calls match its `allowedTools` |
| 3 | Variable binding resolved | No `{{nodes.*.output.*}}` templates in final output |
| 4 | Terminal synthesis is coherent | Read the synthesis, check it answers the original prompt |

## Failure Mode Taxonomy

### 1. Premature Anchoring
**Symptom**: Model identifies project as wrong language/framework.
**Root cause**: Directory listing contains misleading entries (node_modules, vendor, dist) that activate wrong model weights.
**Typical fix**: Noise filtering in `list_dir`, statistical profiling.

### 2. Tool Routing Failure
**Symptom**: Model calls `list_dir` on a file, or `read_file` on a directory.
**Root cause**: Model's tool selection heuristic favors `list_dir` over file-reading tools.
**Typical fix**: Prompt-level forcing, tool hints in responses, auto-peek injection.

### 3. Path Fabrication
**Symptom**: Model invents paths like `/path/to/repo/agent-related` that don't exist.
**Root cause**: Model infers path names from its training data rather than from observed directory listings.
**Typical fix**: Epistemic prompting ("do not guess paths"), path suggestion hints in tool output.

### 4. Stuck Loops
**Symptom**: Same tool called with same args 3+ consecutive times.
**Root cause**: Model's stuck detection threshold too high, or model not recognizing error responses.
**Typical fix**: Lower `stuckThreshold` in loop config, improve error message format.

### 5. Confabulation
**Symptom**: Model states implementation details ("sets up the global event dispatcher") not grounded in any tool output.
**Root cause**: Context vacuum — model has directory names but no file content, fills gap with training priors.
**Typical fix**: Force file reads before synthesis, epistemic prompt, auto-peek injection.

### 6. Context Loss (Compaction Drift)
**Symptom**: Model forgets critical findings from early steps after compaction.
**Root cause**: Compaction prompt doesn't preserve project-identifying signals.
**Typical fix**: Harden compaction system prompt to explicitly preserve language, build system, file extensions.

## Cycle Artifact Template

```markdown
# Red Team Cycle <N>

**Task ID**: `<task_id>`
**Prompt**: "<the test prompt>"
**Date**: <timestamp>

## Scorecard

| # | Criterion | Result | Evidence |
|---|-----------|--------|----------|
| 1 | ...       | ✅/❌  | ...      |

## Tool Call Distribution

| Tool | Calls | Success | Failed |
|------|-------|---------|--------|
| ...  | ...   | ...     | ...    |

## Failure Modes

### [FM-1]: <Name>
- **Steps affected**: <step numbers>
- **Symptom**: <what happened>
- **Root cause**: <why>
- **Proposed fix**: <what to change>

## Proposed Fixes (Ranked)

1. **[Fix]** — <description>
   - Addresses: FM-1
   - Files: `internal/tools/filesystem.go`
   - Confidence: High/Medium/Low

## Comparison to Previous Cycle

| Dimension | Cycle N-1 | Cycle N | Delta |
|-----------|-----------|---------|-------|
| Pass rate | 2/5       | 3/5     | +1    |
| Tool calls| 20        | 15      | -5    |
| Confabs   | 3         | 1       | -2    |
```

## Cross-Skill Handoffs

- If a failure mode requires architectural change → hand off to `improve-codebase-architecture`
- If a failure mode is a hard bug → hand off to `diagnose`
- If findings should be preserved → log to `local-wiki`
- If the fix needs benchmark validation → hand off to `analyze-benchmark-run`
