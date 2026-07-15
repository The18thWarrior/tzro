# Goal-Directed File Compaction for Probe Reads

**Date:** 2026-07-15
**Status:** Draft

## Problem

Probe nodes read source code files using `read_file`, which returns up to 200 lines of raw content per call. A single large Go file (e.g., `local_model.go` at 1207 lines) can produce 8-12K tokens of raw output per read. This raw output is injected directly into the probe's thought chain, where it accumulates across steps and eventually exceeds the router sidecar's 16K context window.

**Evidence from docgen-12 benchmark:** The `inference_module_docs` task failed because the probe read three large files (`backend_llama.go`, `backend_remote.go`, `local_model.go`). After compaction at steps 6-10, the accumulated context was still 35,722 chars of **deterministic skeleton output** — entirely from tool results, not LLM reasoning. The compaction pipeline couldn't help because it only compresses LLM reasoning text; tool outputs are skeletonized deterministically, and code skeletons of large files are still large.

The root issue: probe nodes treat every `read_file` result as equally important, injecting the full raw content into the thought chain regardless of what the probe actually needs from that file. A probe exploring architecture doesn't need every function body — it needs exported symbols, type relationships, and patterns relevant to its goal.

## Design

### Core Mechanism

When `read_file` produces output exceeding a configurable threshold, the tool transparently replaces the raw content with a **goal-directed summary** — a compressed representation that retains only the information relevant to the probe's current objective.

The parent probe is completely unaware this is happening. It calls `read_file`, gets back content. The content just happens to be pre-compressed for its goal rather than raw source.

### Architecture

```
Parent Probe                           read_file Tool
    │                                      │
    ├── calls read_file(path)              │
    │                                      │
    │                      ┌───────────────┤
    │                      │ Execute read  │
    │                      │ (normal path) │
    │                      │               │
    │                      │ Output > threshold?
    │                      │  No → return raw content
    │                      │  Yes ↓
    │                      │               │
    │                      │ Read goal from ctx
    │                      │               │
    │                      │ Chunk file into ~100-line windows
    │                      │               │
    │                      │  ┌──────────────────────────┐
    │                      │  │ For each chunk:          │
    │                      │  │  Router: "Given goal X,  │
    │                      │  │  extract relevant info   │
    │                      │  │  from this code chunk"   │
    │                      │  │  → append to summary     │
    │                      │  └──────────────────────────┘
    │                      │               │
    │                      │ Return: [File: path (N lines,│
    │                      │  goal-compressed)] + summary │
    │                      └───────────────┤
    │                                      │
    ├── receives compressed result         │
    │   (thinks it's normal read_file)     │
```

### Goal Propagation

The probe executor injects the probe's goal into the context before executing any tool call:

```go
// probe.go — before tool execution
ctx = context.WithValue(ctx, tools.FileReadGoalKey, config.Goal)
result := tool.Execute(ctx, input)
```

The `read_file` tool reads this goal from the context:

```go
// filesystem.go — inside read_file executeFn
goal, _ := ctx.Value(FileReadGoalKey).(string)
```

If no goal is present (non-probe callers, action nodes, direct CLI usage), the tool returns raw content unchanged — preserving backward compatibility.

### Compaction Pipeline

For a file exceeding the threshold:

1. **Chunk** the raw content into windows of `chunkSize` lines (default: 100 lines). Windows do not overlap.

2. **Compress** each chunk via the router model with a focused prompt:
   ```
   System: "Given the goal: '{goal}', extract only the relevant information
   from this code. Output: relevant function signatures, types, constants,
   and key logic. Skip irrelevant implementation details. Be concise."

   User: {chunk_content}
   ```
   Each call is capped at 256 tokens (`MaxTokensKey`) to prevent inflation — matching the compaction cap from Fix 1.

3. **Concatenate** chunk summaries with a header:
   ```
   [File: internal/inference/local_model.go (1207 lines, goal-compressed)]
   {chunk_1_summary}
   {chunk_2_summary}
   ...
   ```

4. **Return** the compressed result as the tool output. The `ToolResult.Hint` field notes the compression for debugging:
   ```
   Hint: "File was 1207 lines (est. ~9600 tokens). Goal-compressed to ~1200 chars for probe goal: 'Explore the inference module architecture'."
   ```

### Threshold

- **Default**: 100 lines (~800 tokens of code at ~8 tokens/line)
- **Rationale**: Files under 100 lines are small enough to inject raw without context pressure. The 200-line `read_file` cap means at most 2 chunks for any single call — fast and bounded.
- **Configuration**: Not initially configurable. Hardcoded as a constant, like `compactEvery`. If tuning is needed later, promote to a config field.

### Cost Model

For a 200-line `read_file` result (the maximum):
- 2 chunks × 1 router call each
- Each call: ~400-token prompt + 256-token max generation = ~656 tokens
- Total: ~1,312 tokens, ~1-2 seconds on the router
- Output: ~500-1000 chars vs ~8,000 chars raw — **8-16× reduction**

For a file read with `startLine`/`endLine` specifying ≤100 lines: no compaction triggered — the user explicitly requested a specific range.

### Edge Cases

1. **Binary/PDF files**: Not affected — PDF parsing has its own path; binary files are rejected by `read_file`.

2. **Non-code text files**: The compaction prompt works for any text, not just code. Markdown, config files, and logs are all compressible.

3. **Goal is empty string**: If the probe goal somehow isn't propagated, fall back to a generic structural extraction prompt: *"Extract the key structural elements: function signatures, type definitions, constants, and important comments."*

4. **Router model failure**: If any chunk's router call fails, fall back to deterministic truncation of that chunk (first and last 20 lines with `[... N lines omitted ...]`). Never let a compaction failure cascade to a probe failure.

5. **Already-short output**: If `read_file` returns ≤ threshold lines, return raw content unchanged. No overhead for small files.

## Files Changed

### `internal/tools/filesystem.go`
- Add `FileReadGoalKey` context key type and exported variable
- After the existing 200-line cap logic, add the compaction check: if output exceeds threshold and goal is present, run the chunk-compress pipeline
- Add helper function `compressFileForGoal(ctx, content, goal string) (string, error)` that handles chunking and router calls

### `internal/executor/probe.go`
- Before tool execution in the probe loop, inject `config.Goal` into the context via `context.WithValue(ctx, tools.FileReadGoalKey, config.Goal)`

### `internal/tools/filesystem_test.go`
- Test: file under threshold returns raw content
- Test: file over threshold with goal returns compressed content (mock router)
- Test: file over threshold without goal returns raw content (backward compat)
- Test: router failure falls back to deterministic truncation

## Non-Goals

- **Caching compressed results across probes**: Two probes reading the same file with different goals need different compressions. Caching is not useful here.
- **Applying to `list_dir` or `search_files`**: These tools already produce compact output. Only `read_file` has the large-output problem.
- **Configurable threshold**: Hardcoded for now. Promote to config if benchmarks show a need.
- **Observability/SQLite persistence**: Lightweight pass only. If debugging is needed later, add logging or promote to a node type.

## Risks

1. **Information loss**: The router model may drop relevant details during compression. Mitigation: the probe can always call `read_file` again with a specific `startLine`/`endLine` range to get raw content for a region of interest.

2. **Latency**: 2 router calls per large file read adds ~2-4s. For a probe that reads 5 large files, that's ~10-20s of overhead. Acceptable vs. the alternative (probe crash from context overflow).

3. **Router quality for code understanding**: The 1B router model may produce poor summaries of complex code. Mitigation: the 256-token cap keeps output focused, and the goal-directed prompt narrows the scope. If quality is insufficient, the `probeUseWorkerModel` config flag already routes probe inference through the worker — extending this to file compaction would be a natural follow-up.
