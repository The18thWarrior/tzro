# `tzro_code` — Local Code Generation MCP Tool

**Date**: 2026-06-28
**Status**: Approved

---

## Purpose

`tzro_code` is an MCP tool that accepts a specification/jdoc and a filepath, then uses a static 3-node DAG executed by the local model to create or update that file. It offloads long-running code generation to the local engine at zero cloud cost, and structurally encourages callers to plan compact, single-responsibility files.

### Design Philosophy

The local model produces a **draft**. The frontier model (which invoked `tzro_code`) verifies cheaply via input tokens. This exploits the cost asymmetry: input tokens are ~10x cheaper than output tokens. The frontier model specifies *what* to write; the local model does the expensive *writing*; the frontier model reviews the result.

---

## MCP Tool Interface

### `tzro_code`

| Parameter | Type | Required | Description |
|:---|:---|:---|:---|
| `spec` | string | ✅ | The specification/jdoc describing what the file should contain. May include type signatures, behavior descriptions, example usage, imports, constraints. |
| `filepath` | string | ✅ | Absolute path to the target file (create or update). |
| `language` | string | ❌ | Language hint (e.g. `"go"`, `"typescript"`). Auto-detected from file extension if omitted. |
| `maxLines` | integer | ❌ | Override the default line cap. Must be ≤ the configured `codeMaxLines` in config.json. |
| `timeout` | integer | ❌ | Seconds before async fallback. Default 90. |

### Response Shape

**Success:**
```json
{
  "status": "completed",
  "taskId": "abc-123",
  "filepath": "/path/to/file.go",
  "action": "created",
  "linesWritten": 127,
  "nodes": [
    {"id": "check_context", "status": "completed"},
    {"id": "reason_code", "status": "completed"},
    {"id": "write_code", "status": "completed"}
  ]
}
```

**Line cap exceeded (no file written):**
```json
{
  "status": "failed",
  "taskId": "abc-123",
  "error": "Generated code is 612 lines (limit: 500). Decompose the spec into smaller files."
}
```

**Async fallback (timeout exceeded):**
```json
{
  "status": "running",
  "taskId": "abc-123",
  "message": "Code generation in progress. Check tzro_status for completion."
}
```

### Configuration

In `config.json`:
```json
{
  "codeMaxLines": 500
}
```

Default is 500. Users with more powerful local models can increase this.

---

## Static DAG Architecture

The DAG is hardcoded — no planner involved. Built programmatically in Go, like the dashboard workflow.

```
┌─────────────────┐     ┌─────────────────┐     ┌──────────────┐
│  check_context   │────▶│   reason_code    │────▶│  write_code  │
│  (deterministic) │     │    (action)      │     │(deterministic)│
└─────────────────┘     └─────────────────┘     └──────────────┘
```

### Node 1: `check_context` (deterministic)

No LLM. Pure Go logic.

**Steps:**
1. Validate filepath via PathValidator (workspace bounds, restricted dirs).
2. `os.Stat(filepath)` — check if file exists.
3. If exists: read the file content. If >500 lines, apply content-aware truncation from `internal/executor/truncation.go` (preserves function signatures, doc comments, bracket-depth structure).
4. If exists: check for binary content (non-UTF-8). Fail if binary.
5. List the parent directory. Select up to **5 sibling files** sorted by relevance:
   - Same file extension first (most likely to contain related types/patterns).
   - Then alphabetically within each group.
   - Each sibling capped at 200 lines; content-aware truncation applied if exceeded.
6. Detect language from file extension (`.go` → `"go"`, `.ts` → `"typescript"`, etc.).

**Output:**
```json
{
  "exists": true,
  "existingContent": "package foo\n...",
  "language": "go",
  "siblings": {
    "types.go": "package foo\ntype Config struct {...}",
    "handler.go": "package foo\nfunc Handle(...) {...}"
  }
}
```

### Node 2: `reason_code` (action — local model inference)

**AllowedTools:** None — pure generation, no tool calls.

The local model receives a structured prompt:

```
You are a code generator. Write code for a single file based on the spec.

## Spec
{spec from user}

## Target File
Path: {filepath}
Language: {language}
Action: {create | update}

## Existing Content (if updating)
{existingContent — truncated if large}

## Sibling Files (for context — follow their conventions)
### {sibling_name}
{sibling_content}

## Rules
- Output ONLY the complete file content
- No markdown fences, no explanation, no commentary
- If updating: output the COMPLETE updated file, not a diff
- Maximum {maxLines} lines
- Follow the conventions visible in sibling files (naming, formatting, imports)
- Include appropriate imports/package declarations
```

**Output:** Raw file content string.

### Node 3: `write_code` (deterministic)

No LLM. Pure Go logic.

**Steps:**
1. Extract raw code content from `reason_code` output. Strip any markdown fences the model may have added despite instructions.
2. Count lines. If > `maxLines` (from args or config), **fail** with decomposition error. No file is written.
3. Create parent directories: `os.MkdirAll(filepath.Dir(target), 0755)`.
4. If file exists: write backup to `.tzro/backups/{sha256_of_path}.bak`. LRU eviction at 50 files.
5. Write file: `os.WriteFile(target, content, 0644)`.
6. Output: `{"action": "created|updated", "linesWritten": N, "filepath": "..."}`.

---

## New `write_file` Filesystem Tool

A general-purpose tool registered in `internal/tools/filesystem.go`.

### Schema
```json
{
  "path": {"type": "string", "description": "Absolute path to write"},
  "content": {"type": "string", "description": "File content to write"}
}
```

### Safety Constraints

1. **PathValidator**: Same validation as `read_file` — path must resolve within allowed workspace directories.
2. **Restricted directories**: Honors `config.restrictedDirectories`.
3. **UTF-8 only**: Reject content containing null bytes or invalid UTF-8 sequences.
4. **Proactivity level**: L3 (Local Side Effect). Background daemons require approval; user-initiated tasks execute directly.
5. **Backup on overwrite**: Before overwriting an existing file, save a copy to `.tzro/backups/{path_hash}.bak`. LRU eviction at 50 backup files.
6. **Parent directory creation**: Automatically creates parent directories.

### Why a general tool?

The `write_code` deterministic node in `tzro_code`'s DAG calls `write_file` as a registered tool. Making it general-purpose means:
- Other DAGs can use it (config generation, template scaffolding).
- Goes through the standard tool validation pipeline.
- Testable in isolation.

---

## Error Handling

| Error | Stage | Behavior |
|:---|:---|:---|
| Empty spec | MCP handler | Reject before DAG construction |
| Filepath outside workspace | `check_context` | PathValidator error |
| Filepath in restricted dir | `check_context` | Restricted directory error |
| Target is a directory | `check_context` | "filepath must point to a file" |
| Target is binary (non-UTF-8) | `check_context` | "tzro_code only supports text files" |
| No local model available | `reason_code` | "no active inference backend" |
| Model produces empty output | `reason_code` | "model produced no code output" |
| Output exceeds maxLines | `write_code` | Fail with decomposition message (no file written) |
| Parent dir creation fails | `write_code` | OS permission error |
| Disk write fails | `write_code` | OS error |
| Circuit breaker timeout | Any | Remaining nodes `timed_out`, no file written |

### Large File Handling

When the existing file or sibling files exceed size thresholds, content-aware truncation from `internal/executor/truncation.go` is applied:
- **Code**: Truncated at lowest bracket nesting level, preserving function signatures and doc comments. 500-char floor per file.
- **Tabular data**: Retains 3 sample rows plus summary statistics.
- **Text/prose**: Middle-out elision (keep first and last 30 lines).

---

## Testing Strategy

### Unit Tests

1. **`check_context` logic:**
   - File exists → returns content + language detection
   - File doesn't exist → `exists: false`, empty content
   - Sibling prioritization → same-extension first
   - Large file → content-aware truncation applied
   - Binary file → error
   - Restricted path → error

2. **`write_file` tool:**
   - Creates file with parent dirs
   - Overwrites + creates backup
   - Rejects paths outside workspace
   - Rejects binary content
   - Backup LRU eviction at cap

3. **Line cap enforcement:**
   - At 500 lines → passes
   - At 501 lines → fails with decomposition error
   - Custom `maxLines` respected

4. **DAG construction:**
   - Graph has exactly 3 nodes with correct types and edges
   - AllowedTools correctly constrained per node

### Integration Test

Gated behind `TZRO_INTEGRATION=1`:
1. Create temp directory with a Go struct file.
2. Call `tzro_code` with spec: "Add a `Validate()` method".
3. Assert: file updated, compiles, contains the method.

---

## File Manifest

| File | Action | Purpose |
|:---|:---|:---|
| `internal/tools/filesystem.go` | MODIFY | Add `NewWriteFileTool` |
| `internal/tools/filesystem_test.go` | MODIFY | Tests for `write_file` |
| `internal/codegen/codegen.go` | NEW | `check_context` logic, DAG builder, prompt template |
| `internal/codegen/codegen_test.go` | NEW | Unit tests for context gathering and line cap |
| `internal/config/config.go` | MODIFY | Add `CodeMaxLines` field |
| `cmd/tzro-mcp/tools.go` | MODIFY | Register `tzro_code` handler, args struct |
| `docs/ARCHITECTURE.md` | MODIFY | Add §3.13 Code Generation subsection |
