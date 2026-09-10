---
name: tzro
description: >-
  Token optimization, context pack assembly, change-impact analysis,
  and tabular data analysis using the tzro CLI. Use when starting tasks,
  analyzing blast radius before editing code, exploring a codebase,
  reading large files, compacting verbose test/build output, or managing
  portable session handoffs.
---

# tzro — The Local Token Shield & Context Optimization Engine

`tzro` is a compiled, zero-dependency native Go engine (<50 MB RAM) that eliminates cloud API rate limits, locks prompt cache prefixes (benchmarked at 70–99% cache read hit rates across 8 models), and provides sub-millisecond local codebase discovery (`tzro probe`).

---

## Token Optimization & Discovery Guidelines

When operating in this codebase, coding agents **must** use tzro's local discovery and compaction tools rather than burning quadratic cloud context.

### 1. Task Context Assembly: Use `tzro context`
When starting work on a feature, bug, or refactor:
- Run `tzro context "<task description>" --budget 2000` via `run_command`.
- Assembles a ranked, token-budgeted context pack containing relevant symbol definitions, call graphs, TypeScript path alias resolutions, and nearby tests in a single turn.

### 2. Pre-Edit Blast Radius: Use `tzro impact`
Before modifying shared code, utilities, or types:
- Run `tzro impact <filepath...>` (or `tzro impact` with no arguments to analyze uncommitted git changes).
- Identifies direct callers, dependent modules, downstream consumers, and existing test coverage so you know what tests to run before and after your changes.

### 3. Unified Evidence Search: Use `tzro search` & `tzro probe`
- For fast symbol and syntax discovery: Run `tzro probe "<query>"` (<5ms, line numbers and syntax boundaries).
- For cross-evidence lookup: Run `tzro search "<query>"` across code, design specs, ADRs, documentation, stored artifacts, and logs with content deduplication and AST span extraction.

### 4. Large File Reads: Use `tzro skeleton` & `tzro expand`
- If you only need to understand the interface, imports, or method signatures of a large file, run `tzro skeleton <filepath>`.
- If you need to inspect an elided body (`// [body elided: #hash]`), retrieve only those lines using `tzro expand <hash>`.
- To inspect stored artifacts or slice lines: Use `tzro expand art_<id> --lines 10-50`.

### 5. Test & Log Compaction: Use `tzro compact --run`
- When running test suites or builds, execute them through the compactor:
  `tzro compact --run "go test ./..."` or pipe stdout via `... | tzro compact`.
- Enforces an evidence contract: captures exit-code confidence (`observed`), surfaces root cause diagnostics within a 10-line inline cap, and stores complete failure output in SQLite with an expansion hash.

### 6. Agent Task Continuity: Use `tzro session`
- When handing off work to another agent or pausing a session:
  - Save session state: `tzro session save --objective "<goal>" --constraints "<rules>"`
  - Resume session state: `tzro session load <manifest.json>`
  - Inspect continuity and check freshness: `tzro session status`
- Detects stale evidence, git tree deviations, and missing artifacts before redundant work is performed.

### 7. Tabular Data Analysis: Use `tzro ingest` & `tzro query`
- When you encounter large CSV, TSV, or JSON array data (from files or API responses), import it with `tzro ingest <file>` or pipe it via `cat data.csv | tzro ingest -`.
- Query with SQL: `tzro query <table> "SELECT col, COUNT(*) FROM <table> GROUP BY col"`. All columns are TEXT — use `CAST(col AS INTEGER)` or `CAST(col AS REAL)`.
- **Never** dump a full large CSV into context — **97%+ prompt token reduction** on tabular workloads.

### 8. System Diagnostics: Use `tzro doctor`
- If you encounter proxy route errors, unexpected LLM gateway timeouts, or need to verify FTS5 indexing / hooks configuration:
  - Run `tzro doctor` to run synthetic health checks and provider route probing.

---

## CLI Reference for Agents

| Command | Purpose | Token Impact |
| :--- | :--- | :--- |
| `tzro context "<task>" --budget <n>` | Assembles ranked, token-budgeted context pack with AST & call graph | **Replaces 5–10 exploration turns (<2k tokens)** |
| `tzro impact [files...]` | Computes change-impact graph, callers, and test coverage before edits | **Prevents broken refactors and missing test runs** |
| `tzro probe "<query>"` | Fast local symbol and file discovery using ripgrep + Tree-sitter AST | **0 cloud tokens (<500 tokens output)** |
| `tzro search "<query>"` | Unified evidence search across code, specs, docs, logs, and artifacts | **<500 tokens across heterogeneous sources** |
| `tzro skeleton <file>` | Skeletons a code file, eliding function bodies into SHA-256 hashes | **70%–90% token reduction** |
| `tzro expand <hash-or-art-id>` | Retrieves elided code body or stored artifact with optional `--lines` | Fetches only the required ~20 lines |
| `tzro compact [--run "<cmd>"]` | Compactor with evidence guarantees, exit-code confidence, 10-line cap | **80% token reduction on test/build logs** |
| `tzro session save / load` | Portable, git-aware agent session manifest with freshness validation | **Eliminates full transcript/repo re-reads** |
| `tzro inspect explain <trace_id>`| Explains context pack ranking decisions and stage-by-stage omissions | Offline explainability at 0 tokens |
| `tzro doctor` | Synthetic health check for proxy, routes, FTS5 engine, and agent hooks | Instant diagnostic verification |
| `tzro ingest <file>` | Import CSV/TSV/JSON into SQLite, returns envelope with table pointer | **97%+ token reduction on tabular data** |
| `tzro query <table> "<sql>"` | Execute read-only SQL against imported tabular data | Fetches only the query results |
| `tzro start --port 7878` | Launches the transparent loopback reverse proxy | **Locks KV-cache prefix (70–99% hit rate)** |
| `tzro status` | Displays real-time shielded tokens, memory usage, and proxy metrics | Diagnostic monitoring |
