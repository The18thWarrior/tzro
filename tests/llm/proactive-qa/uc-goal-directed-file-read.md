# Use Case: Goal-Directed File Read Compaction

**Actor**: Developer delegating codebase exploration via `tzro_run` with probe nodes
**Route**: N/A (backend tool — `read_file` tool in probe execution context)
**Backend**: `internal/tools/filesystem.go` goal-directed compaction path
**Priority**: P1

---

## Intent

When a probe node reads a large file (>100 lines) during a thought chain, the read_file tool should automatically compress the output against the probe's goal using the local model. This prevents a single large file read from consuming most of the context window, while keeping the goal-relevant sections intact. The user doesn't interact with this directly — it transparently improves probe node reliability for exploration tasks.

## Preconditions

- tzro daemon is running with router model loaded
- A probe node is executing a thought chain with `read_file` in its allowed tools
- The probe has a goal set (e.g., "Explore the architecture of this project")
- Target file exists and is >100 lines

## Success Criteria

- [ ] Files ≤100 lines are returned raw without any compaction
- [ ] Files >100 lines are chunked and goal-compressed when a probe goal is present in context
- [ ] Goal-compressed output retains sections relevant to the probe's goal
- [ ] Non-probe callers (direct `read_file` without goal context) always get raw output
- [ ] Compression uses the router model (not the worker model) for speed
- [ ] Chunk size is 100 lines per chunk, processing sequentially
- [ ] File content that is entirely code preserves function signatures and structure
- [ ] Goal compression result is smaller than the raw input

## Edge Cases to Probe

- Reading a 200-line file (exactly 2 chunks at the boundary)
- Reading a file with `startLine`/`endLine` that crosses the 100-line threshold after slicing
- Probe goal is empty string — should fall through to raw output
- Router model error during compression — should fall back to raw content
- Binary file or PDF — goal compaction should not apply (different code path)

## Anti-Patterns to Watch For

- [ ] Small files (<100 lines) are being unnecessarily compressed (wasted latency)
- [ ] Goal compaction removes critical code that the probe needs for its next step
- [ ] Non-probe callers get compressed output (regression for direct tool usage)
- [ ] Goal compaction runs on PDF files or binary files
- [ ] Compression output is larger than the original file content
- [ ] Error during compression causes the read_file tool to return an error instead of falling back
