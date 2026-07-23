# ADR-0051: SQL Query Language for Cached Data Analysis

## Status

Accepted

## Context

ADR-0050 introduced the **Analyze Node** to give the **Strategic Planner** a clean abstraction for data analysis tasks. The Analyze Node runs a **Thought Chain** with cache exploration tools (`introspect_cache`, `read_cached_data`, `jq_cached_data`) to query tabular data stored in the **Disk-Backed JQ Cache**.

However, the `jq_cached_data` tool asks the 4B **Local Model** to generate free-form jq filter strings. jq is a niche DSL with ~0.01% representation in training data. Its `//` alternative operator, `if/then/else/end` blocks, implicit pipe semantics, and expression-in-argument patterns are unlike any mainstream language. Evidence from 6 benchmark runs showed consistent syntax failures:

- `//` operator misuse (alternative operator confused with division)
- `if` inside `group_by()` (invalid syntax)
- Wrong target paths (querying `.dataProfile` envelope instead of flat records)
- Malformed pipe chains

The model consistently understood the *intent* (group by Sector, count, handle blanks, sort descending) but could not express it in valid jq syntax.

### Considered Options

- **Option A: Structured Query Spec (GBNF-constrained JSON).** A fixed schema of operations (`group_by`, `filter`, `count`, `top_n`) that the model emits as structured JSON and Go interprets deterministically. Safe and GBNF-constrainable, but creates a custom query language that must be expanded as new operations are needed. Every new analytical pattern requires a Go code change.

- **Option B: SQL.** Replace jq with standard SQL executed against materialized tables in SQLite. SQL has massive training data representation, the infrastructure (SQLite, Dialect Adapter) already exists, and the operations needed (SELECT, WHERE, GROUP BY, ORDER BY, COALESCE, LIMIT) are bread-and-butter for any model.

- **Option C: Keep jq with improved fallback.** Expand `basicJQFallback` in Go to handle more jq patterns. Already attempted — the regex-based fallback grew to 200+ lines of pattern matching and still couldn't cover the combinatorial space of jq syntax the model generates.

**Selected: Option B (SQL).** The entire purpose of this refactor is to align the query language with the model's training distribution. SQL is the most widely represented query language in LLM training data. Even a 4B model will reliably generate `SELECT Sector, COUNT(*) FROM t GROUP BY Sector ORDER BY COUNT(*) DESC`.

## Decision

### 1. Ephemeral Materialized Tables

When the **Data Profiler** caches tabular data, a real SQLite table is materialized in a separate **ephemeral query database** (`<TZRO_DIR>/.tzro/cache/query.db`). The table uses the cacheId as its name (e.g., `cache_1784005696353229000`) and column types inferred from the Data Profiler's metadata (`integer → INTEGER`, `float → REAL`, `string/enum → TEXT`). Values that fail type coercion are inserted as `NULL`.

The ephemeral DB is isolated from the production database — it contains only cache tables and a `_cache_tables` metadata table for lifecycle tracking. The JSON blob persists in the production `disk_cache` table as the long-term storage format.

### 2. Tool Surface

- **`sql_cached_data`** (NEW): Accepts `{cacheId, sql}`. Executes the SQL against the ephemeral query DB. Returns results as a JSON array of objects, capped at 500 rows.
- **`introspect_cache`** (UNCHANGED): Returns the Data Profiler envelope with column metadata, types, null rates, cardinality, and sample records. Essential for the model to understand the schema before writing SQL.
- **`read_cached_data`** (REMOVED): Subsumed by `SELECT * FROM cache_<id> LIMIT n OFFSET m`.
- **`jq_cached_data`** (REMOVED): Replaced by `sql_cached_data`.

### 3. SQL Safety (4 Layers)

1. **Separate ephemeral database**: SQL executes against `query.db`, not the production database. The model physically cannot access `fact_memories`, `node_states`, or any system table.
2. **Statement parsing**: Only `SELECT` statements are permitted. `INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `CREATE`, `ATTACH`, and `PRAGMA` are rejected.
3. **Table allowlist**: Referenced tables in `FROM` and `JOIN` clauses must match `cache_*`. Defense-in-depth against any query that escapes the statement filter.
4. **Query timeout and row cap**: Execution bounded by context deadline (5 seconds). Results capped at 500 rows with a pagination hint in the response.

### 4. Table Lifecycle

- **Creation**: Eager materialization at cache time (during `Store` / `StoreFileRef`). Table is guaranteed ready before any Analyze Node queries it.
- **Task completion cleanup**: `DROP TABLE IF EXISTS cache_<id>` for every cacheId used by the completing task.
- **TTL sweep**: 1-day background sweep drops orphaned tables from crashed or abandoned tasks. The JSON blob in the production DB enables lazy re-materialization if a paused task resumes after cleanup.

### 5. Lazy Re-materialization

If `sql_cached_data` is called and the materialized table is missing (TTL'd or post-restart), the handler:
1. Checks the production `disk_cache` table for the raw JSON payload
2. Re-materializes the table using the stored envelope metadata
3. Executes the SQL query
4. Returns results transparently — the model never knows the table was rebuilt

### 6. Cache Bridge Node Update

The **Cache Bridge Node** skips injection when the downstream consumer is an **Analyze Node** (which queries SQL directly). For non-analyze downstream nodes (synthesis, action), the bridge hydrates data via `SELECT * FROM cache_<id> LIMIT 100` instead of a jq expression.

### 7. Ephemeral DB Connection

Managed as a singleton `cache.QueryDB` in the `cache` package, opened lazily on first table materialization. The ephemeral DB includes a metadata table for lifecycle tracking:

```sql
CREATE TABLE IF NOT EXISTS _cache_tables (
    table_name TEXT PRIMARY KEY,
    task_id TEXT,
    created_at INTEGER
);
```

## Consequences

### Positive

- **Eliminates jq syntax failures**: SQL's `GROUP BY`, `ORDER BY`, `COALESCE`, `WHERE` are mainstream constructs any model can generate reliably.
- **Zero custom query language**: No structured spec to maintain and expand. SQL is inherently composable — new analytical patterns require zero Go code changes.
- **Multi-step composition**: The Analyze Node's Thought Chain can issue multiple simple SQL queries across steps, computing derived values (percentages, ratios) in reasoning rather than requiring complex single-query window functions.
- **Leverages existing infrastructure**: SQLite is already the production database. The Dialect Adapter, Database Manager, and connection patterns are established.
- **Complete isolation**: The ephemeral query DB provides stronger safety than any application-level sandboxing of the production DB.

### Negative

- **Dual storage during task execution**: Data exists as both a JSON blob (production) and a relational table (ephemeral) while a task is active. Acceptable trade-off — the ephemeral copy is cleaned up promptly.
- **Materialization cost**: Parsing JSON and inserting rows takes time at cache creation. For a 50K-row CSV, this adds ~1-2 seconds. Acceptable since caching is already an I/O-heavy path.
- **SQL injection surface**: Despite 4 safety layers, free-form SQL carries inherent risk. The separate ephemeral DB is the strongest mitigation — even a worst-case injection can only affect disposable cache tables.
- **Breaking change**: `jq_cached_data` and `read_cached_data` are removed. All prompt references, benchmark fixtures, skills, and system prompts that reference jq patterns must be updated.

## References

- ADR-0005: 5-Layer Context Compaction & Disk-Backed JQ Cache
- ADR-0049: Data Profiler and Cache Bridge Node
- ADR-0050: Analyze Node
