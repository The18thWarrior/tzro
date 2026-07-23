# SQL Query Language for Cached Data Analysis — Implementation Spec

> **ADR**: [0051-sql-query-language-for-cached-data.md](file:///Users/jp/Desktop/Repos/tzro/docs/adr/0051-sql-query-language-for-cached-data.md)
> **Supersedes**: jq-based `jq_cached_data` tool and `basicJQFallback` in `query.go`
> **References**: [ADR-0005](file:///Users/jp/Desktop/Repos/tzro/docs/adr/0005-5-layer-context-compaction-and-jq-cache.md), [ADR-0049](file:///Users/jp/Desktop/Repos/tzro/docs/adr/0049-data-profiler-and-cache-bridge-node.md), [ADR-0050](file:///Users/jp/Desktop/Repos/tzro/docs/adr/0050-analyze-node.md)

---

## 1. Overview

Replace jq with SQL as the query language for the **Disk-Backed Query Cache**. Cached tabular data is materialized as real SQLite tables in a separate ephemeral database (`query.db`). The 4B **Local Model** generates standard SQL (`SELECT`, `WHERE`, `GROUP BY`, `ORDER BY`, `COALESCE`, `LIMIT`) through a tool interface, and Go executes it against SQLite.

### Architecture Summary

```
┌─────────────────────────────────────────────────────────────────┐
│  Data Flow                                                       │
│                                                                  │
│  Data Profiler (read_file)                                       │
│       │                                                          │
│       ├──► JSON blob ──► disk_cache table (prod DB: tzro.db)     │
│       │    [long-term storage — persists across restarts]         │
│       │                                                          │
│       └──► CREATE TABLE + INSERT ──► query.db (ephemeral DB)     │
│            [acceleration layer — cleaned up after task]           │
│                                                                  │
│  Analyze Node (Thought Chain)                                    │
│       │                                                          │
│       ├──► introspect_cache ──► reads from prod DB               │
│       └──► sql_cached_data  ──► SELECT against query.db          │
│                                                                  │
│  Task Completion                                                 │
│       └──► DROP TABLE cache_<id> from query.db                   │
│                                                                  │
│  TTL Sweep (1 day)                                               │
│       └──► DROP orphaned tables from query.db                    │
└─────────────────────────────────────────────────────────────────┘
```

---

## 2. Ephemeral Query Database

### 2.1 Connection Management

**File**: [internal/cache/query_db.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query_db.go) (NEW)

A singleton `*sql.DB` connection in the `cache` package, opened lazily on first table materialization. Located at `<TZRO_DIR>/.tzro/cache/query.db`.

```go
package cache

import (
    "database/sql"
    "sync"
    "tzro/internal/config"
    "path/filepath"
    "os"
)

var (
    queryDB     *sql.DB
    queryDBOnce sync.Once
    queryDBMu   sync.Mutex
)

// QueryDB returns the ephemeral query database connection, creating it lazily.
func QueryDB() *sql.DB {
    queryDBOnce.Do(func() {
        dbPath := config.ResolvePath(filepath.Join(".tzro", "cache", "query.db"))
        os.MkdirAll(filepath.Dir(dbPath), 0755)
        db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
        if err != nil {
            fmt.Fprintf(os.Stderr, "[Cache/QueryDB] Failed to open ephemeral DB: %v\n", err)
            return
        }
        // Create metadata table
        db.Exec(`CREATE TABLE IF NOT EXISTS _cache_tables (
            table_name TEXT PRIMARY KEY,
            task_id TEXT,
            created_at INTEGER
        )`)
        queryDB = db
    })
    return queryDB
}

// CloseQueryDB closes the ephemeral database connection.
func CloseQueryDB() {
    queryDBMu.Lock()
    defer queryDBMu.Unlock()
    if queryDB != nil {
        queryDB.Close()
        queryDB = nil
    }
}
```

> [!IMPORTANT]
> The ephemeral DB is a **separate file** from `tzro.db`. SQL from the model physically cannot access production tables (`fact_memories`, `node_states`, `kg_nodes`, etc.).

### 2.2 Metadata Table Schema

```sql
CREATE TABLE IF NOT EXISTS _cache_tables (
    table_name TEXT PRIMARY KEY,
    task_id    TEXT,
    created_at INTEGER
);
```

Used by the TTL sweep and task-completion cleanup to track which tables exist, which task created them, and when.

---

## 3. Table Materialization

### 3.1 When

Materialization happens **eagerly at cache time** — during `Store()` and `StoreFileRef()` in [cache.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/cache.go). After writing the JSON blob to the prod DB, the code also materializes a table in `query.db`.

### 3.2 Schema Inference

Column types are derived from two sources:

| Cache Path | Type Source | Available Metadata |
|------------|------------|-------------------|
| `StoreFileRef` (Data Profiler path) | [ColumnProfile.Type](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tabular.go#L33-L41) from `DataProfile` | `integer`, `float`, `string`, `boolean`, `enum`, `mixed` |
| `Store` (Compaction Pipeline path) | [CacheEnvelope.FieldTypes](file:///Users/jp/Desktop/Repos/tzro/internal/cache/cache.go#L25) | Go type strings: `string`, `float64`, `bool` |

**Type mapping**:

| Profiler/Envelope Type | SQLite Column Type |
|----------------------|-------------------|
| `integer` | `INTEGER` |
| `float`, `float64` | `REAL` |
| `boolean`, `bool` | `INTEGER` |
| `string`, `enum`, `mixed`, anything else | `TEXT` |

### 3.3 Materialization Function

**File**: [internal/cache/query_db.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query_db.go) (NEW)

```go
// MaterializeTable creates a table in the ephemeral query DB from a JSON
// array of records using the provided column type metadata.
//
// Parameters:
//   - cacheID: used as the table name (e.g., "cache_1784005696353229000")
//   - rawPayload: JSON string containing an array of record objects
//   - columnTypes: map of column name → SQLite type ("TEXT", "INTEGER", "REAL")
//   - taskID: the owning task ID for lifecycle tracking
//
// Column values that fail type coercion are inserted as NULL.
func MaterializeTable(cacheID, rawPayload string, columnTypes map[string]string, taskID string) error
```

**Algorithm**:

1. Parse `rawPayload` as JSON → extract records array (same root-path logic as current `basicJQFallback`)
2. Collect all unique column names from the first record
3. Build `CREATE TABLE cache_<id> (col1 TYPE1, col2 TYPE2, ...)` from `columnTypes` map
4. Begin transaction
5. Batch `INSERT` records (100 per batch) using prepared statements
   - For each value: attempt type coercion to the declared column type
   - On coercion failure: insert `NULL`
6. Insert metadata row: `INSERT INTO _cache_tables (table_name, task_id, created_at) VALUES (?, ?, ?)`
7. Commit transaction

### 3.4 Integration Points

#### In `Store()` ([cache.go:207](file:///Users/jp/Desktop/Repos/tzro/internal/cache/cache.go#L207))

After the existing `INSERT OR REPLACE INTO disk_cache`, add:

```go
// Materialize in ephemeral query DB
columnTypes := envelopeFieldTypesToSQLite(envelope.FieldTypes)
if err := MaterializeTable(cacheID, rawPayload, columnTypes, ""); err != nil {
    fmt.Fprintf(os.Stderr, "[Cache] Materialization warning: %v\n", err)
    // Non-fatal — SQL queries will lazily re-materialize
}
```

The `taskID` is empty at `Store()` time because the task context isn't available in the cache layer. The task ID is populated when the table is first queried by a node (via the `sql_cached_data` handler, which has task context).

#### In `StoreFileRef()` ([cache.go:236](file:///Users/jp/Desktop/Repos/tzro/internal/cache/cache.go#L236))

After the DB insert, read the file, parse the `envelopeJSON` to extract column types from the `DataProfile`, and materialize:

```go
// Parse envelope to get column types from DataProfile
columnTypes := extractColumnTypesFromEnvelope(envelopeJSON)
rawJSON := readFileAsJSON(filePath) // already exists
if !strings.HasPrefix(rawJSON, "Error:") {
    if err := MaterializeTable(cacheID, rawJSON, columnTypes, ""); err != nil {
        fmt.Fprintf(os.Stderr, "[Cache] File materialization warning: %v\n", err)
    }
}
```

### 3.5 Helper: Extract Column Types from Envelope

```go
// extractColumnTypesFromEnvelope parses the envelope JSON and extracts column
// types suitable for SQLite DDL, handling both CacheEnvelope format (FieldTypes)
// and DataProfile format (Columns[].Type).
func extractColumnTypesFromEnvelope(envelopeJSON string) map[string]string
```

This function handles two envelope formats:
1. **CacheEnvelope** (from `Store`): `envelope.FieldTypes` → `{"Name": "string", "Revenue": "float64"}`
2. **DataProfile** (from `StoreFileRef`): `profile.Columns[].Type` → `{"Name": "string", "Revenue": "float"}`

Both are mapped through the type mapping table in §3.2.

---

## 4. Tool Changes

### 4.1 New Tool: `sql_cached_data`

**File**: [internal/tools/tools.go](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tools.go) (MODIFY — replace `jq_cached_data` registration at [line 326-347](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tools.go#L326-L347))

**Schema**:
```json
{
  "type": "object",
  "properties": {
    "tool_arguments": {
      "type": "object",
      "properties": {
        "cacheId": { "type": "string", "description": "The cache identifier from the data profile" },
        "sql": { "type": "string", "description": "SQL SELECT query to execute against the cached data table. The table name is the cacheId." }
      },
      "required": ["cacheId", "sql"]
    }
  },
  "required": ["tool_arguments"]
}
```

**Handler**:
```go
Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
    cacheID, _ := args["cacheId"].(string)
    sqlQuery, _ := args["sql"].(string)
    return cache.ExecuteSQL(ctx, cacheID, sqlQuery)
}
```

### 4.2 Remove `read_cached_data`

**File**: [internal/tools/tools.go](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tools.go) (MODIFY — delete registration at ~[line 305-324](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tools.go#L305-L324))

Subsumed by `SELECT * FROM cache_<id> LIMIT n OFFSET m`.

### 4.3 Keep `introspect_cache`

No changes. Still reads from the prod DB's `disk_cache.envelope_json` column.

---

## 5. SQL Execution Engine

### 5.1 Core Function

**File**: [internal/cache/query.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go) (REWRITE)

Replace the entire jq-based `QueryEngine` with a SQL execution engine:

```go
// ExecuteSQL runs a SQL query against the ephemeral query database.
// It applies 3 safety layers before execution and returns results as
// a JSON array of objects, capped at 500 rows.
//
// Safety layers:
//   1. Statement type check — only SELECT is permitted
//   2. Table allowlist — only cache_* tables allowed in FROM/JOIN
//   3. Query timeout — 5-second context deadline
//
// If the materialized table is missing (TTL'd or post-restart), it
// attempts lazy re-materialization from the prod DB before failing.
func ExecuteSQL(ctx context.Context, cacheID, sqlQuery string) (string, error)
```

### 5.2 Safety Layers

#### Layer 1: Statement Type Check

```go
func validateStatementType(sql string) error {
    // Normalize: trim whitespace, strip leading comments
    normalized := strings.TrimSpace(sql)
    // Remove SQL comments (-- and /* */)
    normalized = stripSQLComments(normalized)
    upper := strings.ToUpper(normalized)
    
    allowed := []string{"SELECT", "WITH"}  // WITH for CTEs
    for _, prefix := range allowed {
        if strings.HasPrefix(upper, prefix) {
            return nil
        }
    }
    return fmt.Errorf("only SELECT statements are permitted, got: %.50s", normalized)
}
```

> [!NOTE]
> `WITH` (CTEs) are allowed because `WITH ... AS (...) SELECT ...` is a common analytical pattern and is read-only.

#### Layer 2: Table Allowlist

```go
func validateTableReferences(sql string) error {
    // Extract table names from FROM and JOIN clauses
    tables := extractTableNames(sql)
    for _, table := range tables {
        if !strings.HasPrefix(table, "cache_") && table != "_cache_tables" {
            return fmt.Errorf("query references disallowed table: %s (only cache_* tables permitted)", table)
        }
    }
    return nil
}

// extractTableNames uses regex to find table names after FROM and JOIN keywords.
// Handles: FROM table, JOIN table, FROM table AS alias, subqueries are caught
// by the recursive table extraction on CTE/subquery bodies.
func extractTableNames(sql string) []string
```

> [!WARNING]
> This is a regex-based heuristic, not a full SQL parser. It's defense-in-depth — the primary safety boundary is the separate ephemeral database file. Even if table extraction has gaps, the model can only access cache tables.

#### Layer 3: Query Timeout & Row Cap

```go
const (
    sqlQueryTimeout = 5 * time.Second
    sqlMaxRows      = 500
)

// Applied in ExecuteSQL:
queryCtx, cancel := context.WithTimeout(ctx, sqlQueryTimeout)
defer cancel()

// Row cap: rewrite query if no LIMIT clause exists
if !hasLimitClause(sqlQuery) {
    sqlQuery = sqlQuery + " LIMIT 501" // fetch 501 to detect overflow
}
```

When 501 rows are returned, truncate to 500 and append a footer:
```json
{"_note": "Showing 500 of more rows. Use LIMIT/OFFSET for pagination."}
```

### 5.3 Result Formatting

Query results are returned as a JSON array of objects:

```go
func rowsToJSON(rows *sql.Rows) (string, error) {
    columns, _ := rows.Columns()
    var results []map[string]interface{}
    
    for rows.Next() {
        values := make([]interface{}, len(columns))
        ptrs := make([]interface{}, len(columns))
        for i := range values {
            ptrs[i] = &values[i]
        }
        rows.Scan(ptrs...)
        
        row := make(map[string]interface{})
        for i, col := range columns {
            row[col] = values[i]
        }
        results = append(results, row)
    }
    
    resBytes, _ := json.MarshalIndent(results, "", "  ")
    return string(resBytes), nil
}
```

### 5.4 Lazy Re-materialization

When `ExecuteSQL` detects the table doesn't exist (SQLite error: "no such table"):

```go
func (re)materializeFromProd(cacheID string) error {
    // 1. Fetch raw payload and envelope from prod DB
    db := memory.DB.RawDB()
    var rawPayload, envelopeJSON string
    err := db.QueryRow(
        "SELECT raw_payload, envelope_json FROM disk_cache WHERE cache_id = ?",
        cacheID,
    ).Scan(&rawPayload, &envelopeJSON)
    
    if rawPayload == "" {
        // Check for file_path reference
        var filePath string
        db.QueryRow("SELECT COALESCE(file_path, '') FROM disk_cache WHERE cache_id = ?", cacheID).Scan(&filePath)
        if filePath != "" {
            rawPayload = readFileAsJSON(filePath)
        }
    }
    
    // 2. Extract column types from envelope
    columnTypes := extractColumnTypesFromEnvelope(envelopeJSON)
    
    // 3. Re-materialize
    return MaterializeTable(cacheID, rawPayload, columnTypes, "")
}
```

### 5.5 Full ExecuteSQL Flow

```
ExecuteSQL(ctx, cacheID, sqlQuery)
  │
  ├── validateStatementType(sqlQuery)  → reject non-SELECT
  ├── validateTableReferences(sqlQuery) → reject non-cache tables
  │
  ├── Apply timeout context (5s)
  ├── Apply row cap (LIMIT 501 if no LIMIT)
  │
  ├── Execute against QueryDB()
  │     │
  │     ├── Success → rowsToJSON → return
  │     │
  │     └── "no such table" error
  │           │
  │           ├── rematerializeFromProd(cacheID)
  │           │     │
  │           │     ├── Success → retry query → return
  │           │     └── No prod data → return error
  │           │
  │           └── Other error → return error
  │
  └── If >500 rows: truncate + append pagination note
```

---

## 6. Table Lifecycle & Cleanup

### 6.1 Task Completion Cleanup

**File**: [internal/executor/executor.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/executor.go) (MODIFY)

Add a cleanup call at the end of `ExecuteGraph` (after all nodes complete):

```go
// After task execution completes (success or failure):
cache.DropTaskTables(graph.TaskID)
```

**File**: [internal/cache/query_db.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query_db.go) (NEW)

```go
// DropTaskTables removes all materialized cache tables owned by the given taskID.
func DropTaskTables(taskID string) {
    db := QueryDB()
    if db == nil {
        return
    }
    
    rows, err := db.Query("SELECT table_name FROM _cache_tables WHERE task_id = ?", taskID)
    if err != nil {
        return
    }
    defer rows.Close()
    
    for rows.Next() {
        var tableName string
        rows.Scan(&tableName)
        db.Exec("DROP TABLE IF EXISTS " + sanitizeTableName(tableName))
        db.Exec("DELETE FROM _cache_tables WHERE table_name = ?", tableName)
    }
}
```

> [!NOTE]
> Since `taskID` may be empty at materialization time (the cache layer doesn't have task context), the `sql_cached_data` handler should update `_cache_tables.task_id` on first query if it's currently empty. This associates the table with the executing task for cleanup.

### 6.2 TTL Sweep (1 Day)

**File**: [internal/cache/query_db.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query_db.go) (NEW)

```go
// SweepExpiredTables drops tables older than the TTL from the ephemeral DB.
// Called periodically by the Attention Scheduler or daemon background loop.
const CacheTableTTL = 24 * time.Hour

func SweepExpiredTables() {
    db := QueryDB()
    if db == nil {
        return
    }
    
    cutoff := time.Now().Add(-CacheTableTTL).Unix()
    rows, err := db.Query("SELECT table_name FROM _cache_tables WHERE created_at < ?", cutoff)
    if err != nil {
        return
    }
    defer rows.Close()
    
    var count int
    for rows.Next() {
        var tableName string
        rows.Scan(&tableName)
        db.Exec("DROP TABLE IF EXISTS " + sanitizeTableName(tableName))
        db.Exec("DELETE FROM _cache_tables WHERE table_name = ?", tableName)
        count++
    }
    
    if count > 0 {
        fmt.Fprintf(os.Stderr, "[Cache/TTL] Swept %d expired tables (older than %v)\n", count, CacheTableTTL)
    }
}
```

**Integration**: Call `SweepExpiredTables()` from the existing daemon idle loop or Two-Tier Cache GC Tier 2 path.

---

## 7. Prompt Updates

### 7.1 Analyze Node System Prompt

**File**: [internal/executor/probe.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/probe.go) — `buildAnalyzeSystemPrompt()` ([line 690-722](file:///Users/jp/Desktop/Repos/tzro/internal/executor/probe.go#L690-L722))

Replace the jq patterns section:

```
## Data Analysis Strategy
You analyze data from upstream nodes using a systematic approach:

1. First, check the accumulated context for a cacheId from an upstream data source.
2. If a cacheId is available:
   - Use 'introspect_cache' to understand the data schema (column names, types, sample records)
   - Use 'sql_cached_data' to query the data using standard SQL
   - The table name is the cacheId itself (e.g., SELECT * FROM cache_178...)
3. If no cacheId is available, synthesize your analysis from the raw text data in the accumulated context.

Common SQL patterns for data analysis:
- Count all records: SELECT COUNT(*) FROM cache_<id>
- Group and count: SELECT Sector, COUNT(*) as cnt FROM cache_<id> GROUP BY Sector ORDER BY cnt DESC
- Handle blanks: SELECT COALESCE(Sector, 'Unspecified') as Sector, COUNT(*) as cnt FROM cache_<id> GROUP BY COALESCE(Sector, 'Unspecified')
- Filter rows: SELECT * FROM cache_<id> WHERE Status = 'Active'
- Unique values: SELECT DISTINCT Sector FROM cache_<id>
- Top N: SELECT * FROM cache_<id> ORDER BY Revenue DESC LIMIT 5
```

### 7.2 `isAnalyzeConfig()`

**File**: [internal/executor/probe.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/probe.go#L657-L663) (MODIFY)

Update tool name check:

```go
func isAnalyzeConfig(allowedTools []string) bool {
    for _, t := range allowedTools {
        if t == "introspect_cache" || t == "sql_cached_data" {
            return true
        }
    }
    return false
}
```

### 7.3 CacheExplorationGuide

**File**: [internal/executor/executor.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/executor.go#L108-L121) (MODIFY)

```go
const CacheExplorationGuide = `

### DISK-BACKED CACHE EXPLORATION GUIDE
A previous step resulted in a large payload that has been cached on disk to protect the context window.
You have access to the following special tools to explore and query this cached data:

1. 'introspect_cache': Retrieve schema, field lists, types, and sample record of the cached payload.
   Format: {"tool_arguments": {"cacheId": "cache_..."}}
2. 'sql_cached_data': Query the cached data using standard SQL. The table name is the cacheId.
   Format: {"tool_arguments": {"cacheId": "cache_...", "sql": "SELECT Sector, COUNT(*) as cnt FROM cache_... GROUP BY Sector ORDER BY cnt DESC"}}

If you need to analyze, filter, paginate, or count records from the cache, you MUST use one of these tools.`
```

### 7.4 Cache Bridge Context Enrichment

**File**: [internal/executor/cache_bridge.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/cache_bridge.go#L43-L56) (MODIFY)

Update the enrichment block to teach SQL patterns instead of jq:

```go
enrichment := fmt.Sprintf(`

## CACHE DATA SCHEMA (from introspect_cache)
The cached data for cacheId '%s' is stored in a SQL table named '%s'.
Query it using standard SQL via the sql_cached_data tool.

Example SQL patterns:
- Count records: SELECT COUNT(*) FROM %s
- Group by field: SELECT FieldName, COUNT(*) as cnt FROM %s GROUP BY FieldName ORDER BY cnt DESC
- Filter: SELECT * FROM %s WHERE FieldName = 'value'
- Unique values: SELECT DISTINCT FieldName FROM %s

Schema introspection result:
%s`, match, match, match, match, match, match, schema)
```

---

## 8. Compiler Updates

### 8.1 Cache Tools List

**File**: [internal/compiler/sct_compiler.go](file:///Users/jp/Desktop/Repos/tzro/internal/compiler/sct_compiler.go#L266) (MODIFY)

```go
// cacheTools are the tools available to cache bridge and analyze nodes.
var cacheTools = []string{"introspect_cache", "sql_cached_data"}
```

### 8.2 Cache Bridge Node Injection

**File**: [internal/compiler/sct_compiler.go](file:///Users/jp/Desktop/Repos/tzro/internal/compiler/sct_compiler.go#L344-L356) (MODIFY)

Update the bridge node definition:

```go
bridgeNode := GraphNode{
    ID:     bridgeID,
    Type:   "action",
    Action: "sql_cached_data",
    Instructions: "Query the cached tabular data from the upstream node's Data Profile. " +
        "Use the cacheId from the upstream output. " +
        "Execute: SELECT * FROM cache_<id> LIMIT 100 to return a representative sample.",
    AllowedTools:        cacheTools,
    Status:              "pending",
    ActivationThreshold: 0.0,
}
```

### 8.3 Analyze Node Skip

**File**: [internal/compiler/sct_compiler.go](file:///Users/jp/Desktop/Repos/tzro/internal/compiler/sct_compiler.go#L318-L320) (MODIFY)

Add `analyze` to the skip list for cache bridge injection:

```go
if origNode.Type == "probe" || origNode.Type == "synthesis" || origNode.Type == "analyze" {
    continue // Probes handle cache tools via expansion; synthesis doesn't produce profiles;
             // analyze nodes query SQL directly
}
```

Also update the downstream check to skip bridge injection when any downstream node is type `analyze`:

```go
// Check if any downstream node is an analyze node (queries SQL directly)
for _, edge := range sctEdges {
    if edge.SourceID == execID {
        for _, node := range sctNodes {
            if node.ID == edge.TargetID && node.Type == "analyze" {
                hasDownstreamCacheTools = true
                break
            }
        }
    }
}
```

---

## 9. Runtime Cache Bridge Update

**File**: [internal/executor/cache_bridge.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/cache_bridge.go#L99-L110) (MODIFY)

Update the runtime bridge node:

```go
bridgeNode := compiler.GraphNode{
    ID:     bridgeID,
    Type:   "action",
    Action: "sql_cached_data",
    Instructions: "Query the cached tabular data from the upstream node's Data Profile. " +
        "Use the cacheId from the upstream output. " +
        "Execute: SELECT * FROM cache_<id> LIMIT 100 to return a representative sample.",
    AllowedTools:        cacheToolNames,
    Status:              "pending",
    ActivationThreshold: 0.0,
}
```

Update `cacheToolNames`:

```go
var cacheToolNames = []string{"introspect_cache", "sql_cached_data"}
```

---

## 10. Analyze Node Compiler Expansion

**File**: [internal/compiler/sct_compiler.go](file:///Users/jp/Desktop/Repos/tzro/internal/compiler/sct_compiler.go) (MODIFY)

Where the SCT compiler auto-creates `ProbeConfig` for analyze nodes, update the allowed tools:

```go
// For analyze nodes:
AllowedTools: []string{"introspect_cache", "sql_cached_data"}
```

---

## 11. Files Changed Summary

### New Files

| File | Description |
|------|-------------|
| `internal/cache/query_db.go` | Ephemeral query DB singleton, `MaterializeTable`, `DropTaskTables`, `SweepExpiredTables`, type mapping helpers |
| `internal/cache/query_db_test.go` | Unit tests for materialization, SQL execution, safety layers, cleanup, re-materialization |

### Modified Files

| File | Changes |
|------|---------|
| [internal/cache/query.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go) | **Rewrite**: Replace `QueryEngine` interface + `jqQueryEngine` + `basicJQFallback` with `ExecuteSQL` + safety layers + `rowsToJSON`. Remove jq dependency entirely. |
| [internal/cache/cache.go](file:///Users/jp/Desktop/Repos/tzro/internal/cache/cache.go#L207-L252) | Add `MaterializeTable` calls in `Store()` and `StoreFileRef()`. Add `extractColumnTypesFromEnvelope` helper. |
| [internal/tools/tools.go](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tools.go#L326-L347) | Replace `jq_cached_data` with `sql_cached_data`. Remove `read_cached_data` registration. |
| [internal/executor/probe.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/probe.go#L655-L722) | Update `isAnalyzeConfig()` tool names. Rewrite `buildAnalyzeSystemPrompt()` — SQL patterns instead of jq. |
| [internal/executor/executor.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/executor.go#L108-L121) | Rewrite `CacheExplorationGuide` constant. Add `cache.DropTaskTables()` at task completion. |
| [internal/executor/cache_bridge.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/cache_bridge.go) | Update `cacheToolNames`. Update enrichment block to SQL patterns. Update bridge node definition. |
| [internal/compiler/sct_compiler.go](file:///Users/jp/Desktop/Repos/tzro/internal/compiler/sct_compiler.go#L266-L356) | Update `cacheTools` list. Update bridge node Action/Instructions. Add `analyze` to bridge skip list. |
| [internal/compiler/cache_bridge_test.go](file:///Users/jp/Desktop/Repos/tzro/internal/compiler/cache_bridge_test.go) | Update assertions: `jq_cached_data` → `sql_cached_data`, remove `read_cached_data` references. |

### Deleted Code

| What | Where |
|------|-------|
| `QueryEngine` interface | [query.go:17-19](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go#L17-L19) |
| `jqQueryEngine` struct + `Query` method | [query.go:23-46](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go#L23-L46) |
| `basicJQFallback` function (214 lines) | [query.go:48-213](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go#L48-L213) |
| `DefaultQueryEngine` variable | [query.go:26](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go#L26) |
| `read_cached_data` tool registration | [tools.go:~305-324](file:///Users/jp/Desktop/Repos/tzro/internal/tools/tools.go#L305-L324) |
| External jq dependency (`exec.LookPath("jq")`) | [query.go:30](file:///Users/jp/Desktop/Repos/tzro/internal/cache/query.go#L30) |

---

## 12. Benchmark Validation

```bash
TZRO_DIR=/path/to/tzro tzro compare \
  --category datanal \
  --condition cooperative \
  --task lead_sector_breakdown \
  --output /tmp/sql-query-test
```

**Success Criteria**:
1. ✅ No jq syntax errors in logs
2. ✅ No `jq_cached_data` or `read_cached_data` tool calls in output
3. ✅ Model generates valid SQL (`SELECT ... FROM cache_<id> ...`)
4. ✅ `sql_cached_data` returns correct JSON results
5. ✅ Recall synthesis contains accurate sector counts and percentages
6. ✅ Quality ≥ 4.0/5.0 (currently 2.25 with incremental fixes, 1.00 without)

**Run 3+ times** to account for planner non-determinism.

---

## 13. Migration / Backward Compatibility

> [!CAUTION]
> This is a **breaking change** for any existing DAG plans, skills, or benchmark fixtures that reference `jq_cached_data` or `read_cached_data`.

### What breaks:
- Existing compiled DAG plans referencing `jq_cached_data` as a tool action
- Benchmark fixtures with expected tool calls to `jq_cached_data` or `read_cached_data`
- Any micro-skills teaching jq patterns for cache exploration
- The `CacheStore.Query()` method signature (changes from jq to SQL)

### Migration steps:
1. Update benchmark fixtures: search for `jq_cached_data` and `read_cached_data` in all test files
2. Update or delete micro-skills referencing jq patterns
3. Clear any cached/compiled DAG plans that reference old tool names
4. `CacheStore` interface: update `Query` signature from `(ctx, cacheID, jqExpr)` to `(ctx, cacheID, sql)`, or deprecate it and route through `ExecuteSQL` directly
