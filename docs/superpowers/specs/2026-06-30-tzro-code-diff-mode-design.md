# `tzro_code` Diff/Patch Mode & File Size Guard — Design Spec

**Date:** 2026-06-30
**Derived from:** Codegen Quality Pipeline benchmark results (ADR-0035 implementation)
**Predecessor:** [tzro_code design](./2026-06-28-tzro-code-design.md), [Codegen Quality Pipeline](./2026-06-29-codegen-quality-pipeline-design.md)

---

## Problem Statement

Benchmark results from the ADR-0035 implementation exposed a critical limitation: **`tzro_code` file updates have a 0% clean success rate** for files over ~300 lines. The root cause is the whole-file-rewrite model — the local model must output the *complete* file contents, but its token budget truncates output at ~300 lines, silently destroying the rest of the file. This was the primary failure mode in 4 of 9 benchmark tasks.

| Metric | Value |
|--------|-------|
| File updates attempted | 5 |
| Clean success (no fix-up needed) | 0 (0%) |
| Files destroyed by truncation | 4 (80%) |
| Required surgical frontier fallback | 4 (80%) |
| New file creation success | 3/4 (75%) |

The pattern is clear: **whole-file rewrite works for small new files, catastrophically fails for existing files of any size.** The local model can reason about a 2000-line file's structure just fine — it can't *output* 2000 lines.

Two complementary fixes:

1. **Diff/Patch Mode**: Instead of outputting the entire file, the model outputs only the changed hunks in a structured diff format. This reduces output token requirements by 10-50x for typical edits.
2. **File Size Guard**: Hard enforcement at 500 lines. Files exceeding this threshold are refused for whole-file rewrite, forcing the caller to either use diff mode or decompose.

---

## Design Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Unified diff or structured JSON hunks? | **Structured JSON hunks** — more constrained, GBNF-enforceable, no ambiguous line matching |
| 2 | Who decides diff vs. whole-file? | **Automatic**: existing file > 500 lines → diff mode mandatory. ≤ 500 lines → caller choice via `mode` parameter, defaults to whole-file for creates and diff for updates |
| 3 | How does the model locate edit positions? | **Anchor lines**: each hunk specifies `searchContent` (exact substring to find) and `replaceContent` (replacement). Same semantics as frontier model edit tools |
| 4 | What if the model can't find the anchor? | **Fuzzy fallback** with Levenshtein tolerance (±3 chars per line), then hard fail with error message |
| 5 | Where does the 500-line guard live? | **In `handleTzroCode`** — pre-DAG check. If existing file > 500 lines and mode is `"full"`, reject with actionable error |
| 6 | Can the caller force whole-file on large files? | **No.** The guard is hard. Files > 500 lines *must* use diff mode or be decomposed |
| 7 | How does diff mode interact with the compilation gate? | **Same** — `validate_code` node runs after patching, Edge Thought can spawn retry if compilation fails |
| 8 | What about new file creation with diff mode? | **Rejected** — diff mode requires an existing file. `mode: "diff"` on a non-existent file returns an error |

---

## Workstream 1: Structured Diff Output Format

**Impact:** Eliminates truncation failure mode | **Priority:** P0

### 1.1 Hunk schema definition

Define the JSON schema for structured diff output. This schema will be used as the GBNF grammar constraint on the `reason_code` node when operating in diff mode.

**[NEW] `internal/codegen/diff_types.go`**

```go
// DiffHunk represents a single edit operation within a file.
type DiffHunk struct {
    // SearchContent is the exact substring to locate in the existing file.
    // Must match a unique location. Include enough surrounding lines for uniqueness.
    SearchContent string `json:"searchContent"`

    // ReplaceContent is the content to substitute for SearchContent.
    // Empty string means deletion.
    ReplaceContent string `json:"replaceContent"`

    // Description is a brief explanation of what this hunk does (for logging).
    Description string `json:"description,omitempty"`
}

// DiffOutput is the structured output from the reason_code node in diff mode.
type DiffOutput struct {
    Hunks []DiffHunk `json:"hunks"`
}
```

JSON Schema for GBNF grammar constraint:
```json
{
  "type": "object",
  "properties": {
    "hunks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "searchContent": { "type": "string" },
          "replaceContent": { "type": "string" },
          "description": { "type": "string" }
        },
        "required": ["searchContent", "replaceContent"]
      }
    }
  },
  "required": ["hunks"]
}
```

### 1.2 Diff prompt construction

**[NEW] `internal/codegen/diff_prompt.go`**

```go
// BuildDiffPrompt assembles the prompt for the reason_code node in diff mode.
// Unlike BuildCodePrompt (which asks for complete file output), this prompt
// asks the model to output only the changed hunks.
func BuildDiffPrompt(spec, filePath, language, existingContent string,
    siblings map[string]string) string
```

The prompt structure:

```
You are a precise code editor. Apply the requested changes to an existing file
using structured edit hunks.

## Spec
{spec}

## Target File
Path: {filePath}
Language: {language}

## Current File Content
```
{existingContent}
```

## Sibling Files (for context)
{siblings}

## Rules
- Output a JSON object with a "hunks" array
- Each hunk has "searchContent" (exact text to find) and "replaceContent" (replacement)
- searchContent MUST be an exact substring of the current file content
- Include enough context lines in searchContent to ensure uniqueness
- Order hunks from top of file to bottom
- For insertions, use a nearby line as searchContent and include it in replaceContent
- For deletions, set replaceContent to empty string
```

### 1.3 Diff application engine

**[NEW] `internal/codegen/diff_apply.go`**

```go
// ApplyDiffHunks applies structured hunks to existing file content.
// Returns the patched content or an error if any hunk fails to match.
//
// Matching strategy (per hunk):
// 1. Exact substring match (strings.Index)
// 2. Fuzzy match: normalize whitespace and retry (collapse runs of spaces/tabs)
// 3. Fail with error identifying the unmatched searchContent (first 80 chars)
//
// Hunks are applied sequentially in order. Each successful application updates
// the working content, so subsequent hunks match against the already-patched file.
func ApplyDiffHunks(existingContent string, hunks []DiffHunk) (string, error)
```

**Edge cases:**
- **Duplicate matches**: If `searchContent` matches multiple locations, return error asking for more context lines
- **Overlapping hunks**: If hunk N's replacement overlaps with hunk N+1's search region, detect and return error
- **Empty file**: If existing content is empty and hunks are provided, error (should use full mode for new files)

---

## Workstream 2: File Size Guard

**Impact:** Prevents data loss from truncation | **Priority:** P0

### 2.1 Pre-DAG size check

**[MODIFY] `cmd/tzro-mcp/tools.go` — `handleTzroCode`**

After `GatherContext`, check the existing file size and enforce the guard:

```go
// File size guard: files > 500 lines MUST use diff mode
const maxFullRewriteLines = 500

if codeCtx.Exists {
    existingLines := strings.Count(codeCtx.ExistingContent, "\n")
    if existingLines > maxFullRewriteLines && mode != "diff" {
        return errorResult(fmt.Sprintf(
            "File %s has %d lines (limit: %d for full rewrite). "+
            "Use mode: \"diff\" for surgical edits, or decompose the file into "+
            "smaller single-responsibility files.",
            args.Filepath, existingLines, maxFullRewriteLines,
        ))
    }
}
```

### 2.2 Auto-mode selection

**[MODIFY] `cmd/tzro-mcp/tools.go` — `handleTzroCode`**

When `mode` is not explicitly set by the caller, apply auto-selection:

```go
// Auto-mode: select based on file state
if mode == "" {
    if !codeCtx.Exists {
        mode = "full"  // New file creation → whole file
    } else {
        existingLines := strings.Count(codeCtx.ExistingContent, "\n")
        if existingLines > 200 {
            mode = "diff"  // Large existing file → diff mode
        } else {
            mode = "full"  // Small existing file → whole file OK
        }
    }
}
```

Threshold at 200 lines (not 500) for auto-selection to prefer diff mode early, well before the hard guard kicks in.

---

## Workstream 3: MCP Interface Changes

**Impact:** Caller-facing API | **Priority:** P0

### 3.1 Add `mode` parameter

**[MODIFY] `cmd/tzro-mcp/tools.go` — `TzroCodeArgs`**

```diff
 type TzroCodeArgs struct {
     Spec     string `json:"spec"`
     Filepath string `json:"filepath"`
     Language string `json:"language,omitempty"`
     MaxLines int    `json:"maxLines,omitempty"`
     Timeout  int    `json:"timeout,omitempty"`
+    Mode     string `json:"mode,omitempty"`
 }
```

| Value | Behavior |
|-------|----------|
| `""` (default) | Auto-select: `"full"` for new files or small files (≤200 lines), `"diff"` for larger files |
| `"full"` | Force whole-file output. Rejected if existing file > 500 lines |
| `"diff"` | Force diff mode. Rejected if file doesn't exist |

### 3.2 Response shape changes

**Diff mode success:**
```json
{
  "status": "completed",
  "taskId": "abc-123",
  "filepath": "/path/to/file.go",
  "action": "updated",
  "mode": "diff",
  "hunksApplied": 3,
  "linesChanged": 47,
  "totalLines": 892
}
```

**Guard rejection:**
```json
{
  "status": "failed",
  "taskId": "",
  "error": "File /path/to/file.go has 2659 lines (limit: 500 for full rewrite). Use mode: \"diff\" for surgical edits.",
  "suggestion": "diff"
}
```

---

## Workstream 4: DAG Changes for Diff Mode

**Impact:** Internal execution path | **Priority:** P1

### 4.1 Diff-mode DAG builder

**[NEW] `internal/codegen/codegen_diff.go`**

```go
// BuildDiffDAG constructs a single-node DAG for diff-mode code generation.
// The reason_code node uses structured JSON output (GBNF-constrained) to
// produce DiffOutput instead of raw source code.
func BuildDiffDAG(taskID, spec, filePath, language string,
    codeCtx *CodeContext) *compiler.ExecutionGraph
```

The DAG has one node:
- **reason_code**: `Type: "synthesis"`, GBNF schema constrains output to `DiffOutput` JSON
- No validation node (diff application itself validates by checking hunk matches)

### 4.2 Post-DAG diff application

**[MODIFY] `cmd/tzro-mcp/tools.go` — `handleTzroCode`**

In the post-processing section, branch on mode:

```go
if status == "completed" {
    switch mode {
    case "diff":
        // Parse structured diff output
        var diffOutput codegen.DiffOutput
        if err := json.Unmarshal([]byte(rawCode), &diffOutput); err != nil {
            // Fallback: try to extract JSON from markdown fences
            ...
        }
        
        // Apply hunks to existing content
        patched, err := codegen.ApplyDiffHunks(codeCtx.ExistingContent, diffOutput.Hunks)
        if err != nil {
            respMap["status"] = "failed"
            respMap["error"] = fmt.Sprintf("diff application failed: %v", err)
        } else {
            // Write patched file
            ...
            respMap["hunksApplied"] = len(diffOutput.Hunks)
        }
        
    default: // "full"
        // Existing whole-file write path (unchanged)
        ...
    }
}
```

---

## Workstream 5: Exploration DAG Diff Support

**Impact:** Complex edits with codebase exploration | **Priority:** P2

### 5.1 Diff-mode exploration DAG

**[MODIFY] `internal/codegen/codegen_exploration.go`**

Add a `BuildDiffDAGWithExploration` variant that mirrors `BuildCodeDAGWithExploration` but uses the diff schema on the `reason_code` node:

```go
func BuildDiffDAGWithExploration(taskID, spec, filePath, language string,
    codeCtx *CodeContext) *compiler.ExecutionGraph
```

Three nodes:
1. **explore_context**: `action` node with `ActivationThreshold: 0.8` (same as full mode)
2. **reason_code**: `synthesis` node with GBNF-constrained `DiffOutput` schema
3. **validate_code**: `deterministic` node with `ActivationThreshold: 0.7` (compilation gate)

### 5.2 Complexity router update

**[MODIFY] `cmd/tzro-mcp/classify_complexity.go`** and **`handleTzroCode`**

Extend the routing matrix:

| Complexity | Mode | DAG |
|-----------|------|-----|
| simple | full | `BuildCodeDAG` |
| simple | diff | `BuildDiffDAG` |
| moderate/complex | full | `BuildCodeDAGWithExploration` |
| moderate/complex | diff | `BuildDiffDAGWithExploration` |

---

## Verification Plan

### Automated Tests

**[NEW] `internal/codegen/diff_apply_test.go`**

```
- TestApplyDiffHunks_SingleHunk
- TestApplyDiffHunks_MultipleHunks
- TestApplyDiffHunks_DuplicateMatch_Fails
- TestApplyDiffHunks_NoMatch_Fails
- TestApplyDiffHunks_FuzzyWhitespaceMatch
- TestApplyDiffHunks_DeletionHunk
- TestApplyDiffHunks_InsertionHunk
- TestApplyDiffHunks_EmptyFile_Fails
```

**[NEW] `internal/codegen/diff_prompt_test.go`**

```
- TestBuildDiffPrompt_IncludesExistingContent
- TestBuildDiffPrompt_IncludesSiblings
```

**Existing tests:**
```bash
go test ./internal/codegen/... ./internal/executor/... ./internal/tools/... -count=1
```

### Manual Verification

1. **Small file update (< 200 lines)**: `tzro_code` with no mode → auto-selects `"full"`, whole-file rewrite succeeds
2. **Medium file update (200–500 lines)**: `tzro_code` with no mode → auto-selects `"diff"`, hunks applied correctly
3. **Large file update (> 500 lines)**: `tzro_code` with `mode: "full"` → rejected with guard error
4. **Large file update (> 500 lines)**: `tzro_code` with `mode: "diff"` → hunks applied, compilation passes
5. **New file creation**: `tzro_code` with `mode: "diff"` → rejected (no existing file)
6. **New file creation**: `tzro_code` with no mode → auto-selects `"full"`, creation succeeds

### Benchmark Rerun

Re-execute the 9 ADR-0035 benchmark tasks with diff mode. Expected improvement:

| Metric | Before (whole-file) | Target (with diff) |
|--------|--------------------|--------------------|
| File update success rate | 0% | ≥ 60% |
| Fix-ups required | 5/9 | ≤ 2/9 |
| Files destroyed by truncation | 4/9 | 0/9 |
