# Use Case: Tabular Data Ingest and SQL Query

**Actor**: AI coding agent or developer analyzing CSV/TSV/JSON data
**Route**: CLI — `tzro ingest <file>` and `tzro query <table> "<sql>"`
**Backend**: Local SQLite store with auto-format detection
**Priority**: P1

---

## Intent

An agent encounters large tabular data (CSV files, JSON arrays, API responses) and wants to analyze it without dumping the entire dataset into the LLM context. The agent imports the data into a local SQLite table using `tzro ingest`, receives a compact envelope with schema and sample rows, then queries specific slices using `tzro query` with SQL — achieving 97%+ token reduction on tabular workloads.

## Preconditions

- Tzro binary is installed and on PATH
- Input data is in CSV, TSV, or JSON array format
- SQLite content-hash store is writable

## Success Criteria

- [ ] `tzro ingest data.csv` detects CSV format, imports rows, and prints a data envelope
- [ ] `tzro ingest data.json` detects JSON array format and imports correctly
- [ ] `tzro ingest -` reads from stdin (pipe-friendly)
- [ ] Data envelope includes: table name, column schema, row count, and 5 sample rows
- [ ] `tzro query <table> "SELECT col, COUNT(*) FROM <table> GROUP BY col"` returns markdown table
- [ ] All columns are stored as TEXT (documented behavior)
- [ ] Custom table names work via `--name` flag
- [ ] Auto-generated table names are deterministic (content-hash based)
- [ ] Query results are formatted as compact markdown tables with row count header
- [ ] Empty query results show a clear "No results" message

## Edge Cases to Probe

- Ingest an empty file — should fail with clear error
- Ingest a file that is not tabular (e.g., a Go source file) — should reject with format error
- Query a table that doesn't exist — should return a clear error
- Ingest very large CSV (>100k rows) — should complete without OOM
- Ingest CSV with commas in quoted fields — should parse correctly
- SQL injection in query argument — should be safe (read-only queries only)

## Anti-Patterns to Watch For

- [ ] Ingest silently truncates rows without warning
- [ ] Query allows write operations (INSERT, DELETE, DROP) against the store
- [ ] Table name collisions overwrite existing data without warning
- [ ] Envelope dumps all rows instead of just the sample
- [ ] Non-UTF8 data causes panics instead of graceful handling
