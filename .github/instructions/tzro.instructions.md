---
applyTo: "**/*"
---

# tzro — The Local Token Shield & Context Optimization Engine

`tzro` is a compiled, zero-dependency native Go CLI engine designed for token optimization. It eliminates cloud API rate limits, locks prompt cache prefixes, and provides rapid local codebase discovery.

---

## ⚖️ Token Optimization & Discovery Guidelines

When operating in this codebase, agents **must** use Tzro's local discovery and compaction tools rather than burning quadratic cloud context.

### 1. Task Context Assembly: Use `tzro context`
Run `tzro context "<task description>" --budget 2000` to assemble a ranked, token-budgeted context pack containing relevant symbol definitions, call graphs, TypeScript path aliases, and nearby tests.

### 2. Pre-Edit Impact Analysis: Use `tzro impact`
Run `tzro impact <files...>` before editing shared code to identify callers, dependent packages, and existing test coverage.

### 3. Local Search: Use `tzro search` & `tzro probe`
- Fast symbol discovery: `tzro probe "<query>"` (<5ms, line numbers and syntax boundaries).
- Unified multi-source evidence: `tzro search "<query>"` (searches code, specs, ADRs, docs, store artifacts, and logs).

### 4. Large File Reads: Use `tzro skeleton` & `tzro expand`
- Understand interfaces/signatures: `tzro skeleton <filepath>`
- Retrieve specific elided method bodies or artifacts: `tzro expand <hash-or-art-id> [--lines start-end]`

### 5. Test & Log Compaction: Use `tzro compact --run`
- Run test/build suites via `tzro compact --run "<command>"` or pipe stdout via `... | tzro compact`.
- Enforces an evidence contract with exit-code confidence, failure summaries within a 10-line cap, and overflow hashes stored in SQLite.

### 6. Task Continuity: Use `tzro session`
- Save state before handoffs: `tzro session save --objective "<goal>"`
- Resume state: `tzro session load <manifest.json>`
- Check freshness: `tzro session status`

### 7. Tabular Data Analysis: Use `tzro ingest` & `tzro query`
- Import data into a local SQLite table: `tzro ingest <file>`
- Query the imported data: `tzro query <table> "<sql>"`

### 8. System Diagnostics: Use `tzro doctor`
Run `tzro doctor` to verify proxy routes, upstream provider reachability, SQLite FTS5 status, and registered agent hooks.

---

## 🎛️ CLI Reference

- `tzro context "<task>" --budget <n>`: Assembles ranked context pack with AST & call graph under token budget.
- `tzro impact [files...]`: Computes change-impact graph, callers, and test coverage before edits.
- `tzro probe "<query>"`: Fast local symbol and file discovery using ripgrep + Tree-sitter AST.
- `tzro search "<query>"`: Unified evidence search across code, specs, docs, logs, and artifacts.
- `tzro skeleton <file>`: Skeletons a code file, eliding function bodies into SHA-256 hashes.
- `tzro expand <hash-or-art-id>`: Retrieves elided code body or stored artifact with optional `--lines`.
- `tzro compact [--run "<cmd>"]`: Compactor with evidence guarantees, exit-code confidence, and 10-line cap.
- `tzro session save / load`: Portable, git-aware agent session manifest with freshness validation.
- `tzro inspect explain <trace_id>`: Explains context pack ranking decisions and stage-by-stage omissions.
- `tzro doctor`: Synthetic health check for proxy, routes, FTS5 engine, and agent hooks.
- `tzro ingest <file>`: Import CSV/TSV/JSON into SQLite, returns envelope with table pointer.
- `tzro query <table> "<sql>"`: Execute read-only SQL against imported tabular data.
- `tzro start --port 7878`: Launches the transparent HTTP/HTTPS loopback reverse proxy (Locks KV-cache prefix).
- `tzro status`: Displays real-time shielded tokens, memory usage, and proxy metrics.

## 🔌 Agent Lifecycle Hooks

`tzro` supports lifecycle hooks for agents via scripts in `pkg/hooks`. You can use these hooks for setup, validation, or post-execution routines during agent execution workflows.

