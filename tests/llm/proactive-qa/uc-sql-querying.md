# Use Case: SQL Querying for Cached Structured Data

**Actor**: Developer / Data Analyst using tzro execution engine or tzro-mcp
**Route**: /mcp or CLI / tzro daemon execution
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

The user wants to perform robust, SQL-based analysis over cached tabular datasets (CSVs, structured tool outputs) using the SQLite-backed query cache and Analyze Nodes instead of fragile JQ expressions or full-context JSON dumps. The Deterministic Query Path (ADR-0076) ensures reliable query execution by having the model extract intent via GBNF constraints while code composes the actual SQL.

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
- [ ] GBNF-constrained QueryIntent extraction pulls structured keywords (filter column, operator, value, group column, aggregation function, order) from the goal instead of asking the model to compose raw SQL.
- [ ] QueryIntent supports multi-filter clauses and multiple aggregation extras (ADR-0076).
- [ ] The `query_builder` tool accepts structured query operations (filter, group, aggregate, order, select) and composes valid SQLite SQL deterministically.
- [ ] Schema enrichment injects column names and sample values into the GBNF extraction context so the model selects real column names, not hallucinated ones.
- [ ] Filter operators are enum-constrained to: =, !=, LIKE, >, <, >=, <=, IS NULL, IS NOT NULL.
- [ ] Aggregation functions are enum-constrained to: COUNT, SUM, AVG, MIN, MAX, GROUP_CONCAT.
- [ ] Cache ID extraction from tool outputs sanitizes identifiers to prevent SQL injection and invalid table references.
- [ ] When no cache data is available, cache-specific examples are conditionally omitted from the prompt to prevent cache ID hallucination.

## Edge Cases to Probe

- Querying tables with reserved SQL keywords in column names (e.g. `select`, `group`, `order`).
- Querying CSV datasets with quotes, spaces, or non-standard delimiters.
- CompactPreserve compaction behavior when probe outputs exceed token budget floors.
- Concurrent read/write queries on disk-backed SQLite query cache.
- QueryIntent with empty filterValue but filterOperator set to "IS NULL" — verify valid SQL generation.
- Multi-filter intent with 3+ clauses — verify all are composed into the WHERE clause.
- Column name containing spaces or special characters — verify proper quoting in generated SQL.
- Query referencing a column that doesn't exist in the schema — verify clean error, not a crash.

## Anti-Patterns to Watch For

- [ ] Falling back to raw JQ string parsing when SQLite cache ID is available.
- [ ] Data truncation or lost table schema details during probe node compaction.
- [ ] Unsanitized table or column identifiers causing SQL syntax errors.
- [ ] Empty or silent fallback responses when SQL queries fail on valid cached tables.
- [ ] The model composing raw SQL strings instead of going through the QueryIntent → query_builder path.
- [ ] Cache ID hallucination — the model invents a cache ID that doesn't correspond to any cached dataset.
- [ ] GBNF extraction selecting a column name not present in the schema enrichment context.
- [ ] query_builder producing SQL with SQLite-incompatible syntax (e.g., ILIKE instead of LIKE).

