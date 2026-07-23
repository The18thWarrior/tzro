# Use Case: SQL Querying for Cached Structured Data

**Actor**: Developer / Data Analyst using tzro execution engine or tzro-mcp
**Route**: /mcp or CLI / tzro daemon execution
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

The user wants to perform robust, SQL-based analysis over cached tabular datasets (CSVs, structured tool outputs) using the SQLite-backed query cache and Analyze Nodes instead of fragile JQ expressions or full-context JSON dumps.

## Preconditions

- tzro daemon or MCP server is running.
- A structured dataset (e.g. CSV or JSON dataset) has been profiled or cached into the disk-backed query cache.

## Success Criteria

- [ ] Data profiler automatically extracts table schemas and creates SQLite table identifiers for tabular datasets.
- [ ] SQL queries over cached tables execute reliably via SQLite engine with reserved keyword sanitization and column name matching.
- [ ] Analyze nodes execute data analysis using SQLite tools and maintain CompactPreserve semantics without dropping dataset records.
- [ ] Tabular query results and cache IDs are preserved during probe compaction and recall injection.
- [ ] Evidence collection captures analytical query outputs and feeds upstream DAG context accurately.
- [ ] Self-contained task short-circuiting routes data analysis tasks cleanly according to task lifecycle state.

## Edge Cases to Probe

- Querying tables with reserved SQL keywords in column names (e.g. `select`, `group`, `order`).
- Querying CSV datasets with quotes, spaces, or non-standard delimiters.
- CompactPreserve compaction behavior when probe outputs exceed token budget floors.
- Concurrent read/write queries on disk-backed SQLite query cache.

## Anti-Patterns to Watch For

- [ ] Falling back to raw JQ string parsing when SQLite cache ID is available.
- [ ] Data truncation or lost table schema details during probe node compaction.
- [ ] Unsanitized table or column identifiers causing SQL syntax errors.
- [ ] Empty or silent fallback responses when SQL queries fail on valid cached tables.
