# ADR-0074: Structured Query Composition for Data Analysis Tasks

## Status

Accepted

## Context

The 4B local model has a **0% success rate** generating raw SQL strings for
`sql_cached_data` in the AnalyzePhases `query_dev` phase. When the model fails
to emit `sql_cached_data`, the forced fallback at `phase_runner.go` generates a
generic `SELECT * FROM {cacheId} LIMIT 5` query via `defaultSQLForCacheId()`.
This query ignores the task goal entirely, causing 2 of 5 datanal benchmarks to
produce incorrect answers (score: 1.25/5).

Additionally, the local planner sometimes generates flat DAGs with compound data
tools (`group_by`, `filter_where`, `top_n`) as deterministic exec nodes instead
of routing through the `analyze` node template. This causes:
- **Cache GC failures**: Ephemeral cache tables expire between exec nodes (ERR).
- **Compaction destruction**: 68K+ exec output compacted to 134 chars for
  terminal synthesis (hallucinated statistics).

The 4B model reliably fills structured parameters via GBNF-constrained JSON but
consistently fails at free-form SQL string generation.

## Decision

### 1. `query_builder` composite tool

Replace `sql_cached_data` with a `query_builder` tool that accepts composable,
structured operations and deterministically assembles SQL. Supported operations:

| Operation | Purpose | Key Fields |
|-----------|---------|------------|
| `filter` | WHERE clause | column, operator, value |
| `group_by` | GROUP BY clause | column |
| `aggregate` | COUNT/SUM/AVG/MIN/MAX/GROUP_CONCAT | function, column, distinct, alias |
| `order_by` | ORDER BY clause | column, direction |
| `select` | Specific column selection | columns[] |

The tool validates operators and aggregate functions against whitelists,
bracket-escapes column names for special characters, and escapes values to
prevent SQL injection.

### 2. AnalyzePhases v2 (3-phase pipeline)

Simplify the 4-phase pipeline to 3 phases:

```
schema_orient (introspect_cache)
  → query (query_builder, MinToolCalls: 1)
  → synthesize (no tools)
```

The `query_dev` and `compute` phases are collapsed into a single `query` phase
because `query_builder` handles both exploration and aggregation in a single call.

### 3. Local planner tool scoping

Compound data tools (`group_by`, `filter_where`, `top_n`, `count_by`,
`describe_cache`, `sql_cached_data`, `query_builder`) are hidden from the local
planner's tool inventory via `internalDataTools` filter. This prevents flat DAG
misroutes. The `introspect_cache` tool remains visible as a classification
signal for the `data-analysis` template.

## Consequences

- The 4B model provides structured intent (operations array) instead of raw SQL.
  This plays to the model's strength in parameter extraction.
- AnalyzePhases v2 reduces phase count from 4 to 3, lowering latency and token
  usage per data analysis task.
- `sql_cached_data` is retained in the `query` phase's AllowedTools for backward
  compatibility during transition. It will be removed in a future ADR.
- Complex SQL beyond `query_builder` capabilities (joins, subqueries, CTEs) is
  not supported. This is acceptable for the current benchmark scope (single-table
  CSV analysis).
- The `query_builder` tool is registered globally but hidden from the planner.
  It is only accessible internally via AnalyzePhases.
