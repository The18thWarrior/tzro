---
applyTo: "**/*"
---

# tzro — The Local Token Shield & Context Optimization Engine

`tzro` is a compiled, zero-dependency native Go CLI engine designed for token optimization. It eliminates cloud API rate limits, locks prompt cache prefixes, and provides rapid local codebase discovery.

---

## ⚖️ Token Optimization & Discovery Guidelines

When operating in this codebase, agents **must** use Tzro's local discovery and compaction tools rather than burning quadratic cloud context.

### 1. Codebase Exploration: Use `tzro probe`
Run `tzro probe "<search goal>"` via `run_command` to get exact line numbers, symbol signatures, and content hashes in <5ms with 0 cloud tokens. Never make sequential cloud tool calls (`list_dir`, `grep_search`, `view_file`) for exploration.

### 2. Large File Reads: Use `tzro skeleton` & `tzro expand`
- Understand interfaces/signatures: `tzro skeleton <filepath>`
- Retrieve specific elided method bodies: `tzro expand <hash>`

### 3. Log & Test Output Compaction: Pipe through `tzro compact`
Pipe verbose outputs through `tzro compact` to strip redundant runtime stack frames and flatten JSON arrays into compact Markdown tables.

### 4. Tabular Data Analysis: Use `tzro ingest` & `tzro query`
- Import data into a local SQLite table: `tzro ingest <file>`
- Query the imported data: `tzro query <table> "<sql>"`

---

## 🎛️ CLI Reference

- `tzro probe "<query>"`: Fast local symbol and file discovery using ripgrep + Tree-sitter AST.
- `tzro skeleton <file>`: Skeletons a code file, eliding function bodies into SHA-256 hashes.
- `tzro expand <hash>`: Retrieves the full original function body from local SQLite.
- `tzro compact`: Stdin/stdout pipe for log, stack trace, and JSON array compaction.
- `tzro ingest <file>`: Import CSV/TSV/JSON into SQLite, returns envelope with table pointer.
- `tzro query <table> "<sql>"`: Execute read-only SQL against imported tabular data.
- `tzro start --port 7878`: Launches the transparent HTTP/HTTPS loopback reverse proxy (Locks KV-cache prefix).
- `tzro status`: Displays real-time shielded tokens, memory usage, and proxy metrics.

## 🔌 Agent Lifecycle Hooks

`tzro` supports lifecycle hooks for agents via scripts in `pkg/hooks`. You can use these hooks for setup, validation, or post-execution routines during agent execution workflows.
