# Data Profiler & Cache Bridge Node — Working Spec

> **ADR**: [0046-data-profiler-and-cache-bridge-node](../adr/0046-data-profiler-and-cache-bridge-node.md)
> **Status**: Ready for implementation
> **Glossary terms**: Data Profiler, Cache Bridge Node (added to CONTEXT.md)

---

## 1. Problem Statement

The `read_file` tool returns raw file content capped at 500 lines. This design assumes source code semantics — linear, human-readable, small enough to window through. Tabular data files break these assumptions:

- **Depth**: A 50K-row CSV requires 100+ sequential calls. The Local Model cannot hold or aggregate across that volume.
- **Width**: A single CSV row with 200 columns can exceed the Local Model's entire context window.
- **Existing solution gap**: The Compaction Pipeline (ADR-0005) solves both problems for API outputs but is not connected to `read_file`.

## 2. Solution Overview

Two new concepts:

1. **Data Profiler** — a content-aware layer in `read_file` that detects tabular files and returns a structured profile (schema + statistics + samples + cacheId) instead of raw content.
2. **Cache Bridge Node** — a deterministic node auto-injected into the DAG to ensure downstream consumers can access cached tabular data.

---

## 3. Data Profiler

### 3.1 Routing Rules

Detection is extension-based, applied at the start of `read_file` before any content is read:

| Extension | Condition | Treatment |
|-----------|-----------|-----------|
| `.csv`, `.tsv` | Always | Profile |
| `.xlsx`, `.xls` | Always | Profile |
| `.json` | >200 lines OR >10KB | Profile |
| `.json` | ≤200 lines AND ≤10KB | Raw (unchanged) |
| Everything else | Always | Raw (unchanged) |

CSV/TSV always profile because the width problem is independent of row count.

### 3.2 Profile Output Schema

```go
type DataProfile struct {
    Format       string          `json:"format"`       // "csv", "tsv", "xlsx", "json"
    Path         string          `json:"path"`
    Delimiter    string          `json:"delimiter,omitempty"` // for csv/tsv
    RowCount     int             `json:"rowCount"`
    ColumnCount  int             `json:"columnCount"`
    FileSizeBytes int64          `json:"fileSizeBytes"`
    Columns      []ColumnProfile `json:"columns"`
    SampleRows   string          `json:"sampleRows"`   // TSV-formatted
    CacheID      string          `json:"cacheId"`
    // Excel-specific
    Sheets       []SheetSummary  `json:"sheets,omitempty"`
    ActiveSheet  string          `json:"activeSheet,omitempty"`
}

type ColumnProfile struct {
    Name        string      `json:"name"`
    Type        string      `json:"type"`        // "integer", "float", "string", "boolean", "enum", "mixed"
    NullRate    float64     `json:"nullRate"`
    Cardinality interface{} `json:"cardinality"` // int or ">1000"
    // Conditional fields
    Values      []string    `json:"values,omitempty"`     // only for enum (cardinality ≤ 20)
    Min         *float64    `json:"min,omitempty"`        // only for numeric types
    Max         *float64    `json:"max,omitempty"`        // only for numeric types
}

type SheetSummary struct {
    Name        string `json:"name"`
    RowCount    int    `json:"rowCount"`
    ColumnCount int    `json:"columnCount"`
}
```

The `read_file` tool returns this under a `"dataProfile"` key in its ToolResult, replacing the normal `"content"` key. The ToolResult `Hint` is set to:

```
This is a tabular data file. Data has been profiled and cached.
Use introspect_cache, read_cached_data, or jq_cached_data with cacheId "{cacheId}" for targeted access.
```

### 3.3 Adaptive Sample Sizing

Goal: keep sample rows under a 10K character budget to avoid context bloat from wide files.

```
Algorithm:
  1. Select 5 rows: first 3 + 2 reservoir-sampled from remaining rows
  2. Format as TSV string (header + data rows)
  3. If len(tsvString) > 10_000:
     a. Reduce to 3 rows (first 2 + 1 reservoir-sampled)
     b. If still > 10_000: reduce to 1 row (first data row only)
  4. Apply deterministic column pruning to sample rows (not schema)
```

### 3.4 Deterministic Column Pruning (Sample Rows Only)

Applied to sample rows to reduce width. The full column list is always preserved in the `columns` schema.

**Drop from samples (not schema) if:**
- Column is >90% null
- Column cardinality >95% of row count AND type is string (likely free-text/ID)
- Column has a single constant value across all rows

### 3.5 Streaming Statistics Engine

The profiler streams the file in a single pass. No full-file memory load.

```go
type StreamingProfiler struct {
    rowCount    int
    columns     []columnAccumulator
    reservoir   [][]string  // reservoir-sampled rows
}

type columnAccumulator struct {
    name         string
    nullCount    int
    distinctSet  map[string]struct{} // capped at 1000
    cappedOut    bool                // true when distinctSet hit 1000
    typeTracker  typeInference       // running type inference
    numericMin   *float64
    numericMax   *float64
}
```

**Cardinality cap**: Track distinct values per column up to 1,000. Once exceeded, set `cappedOut = true` and stop inserting. Report as `">1000"` in the profile. Sufficient for enum detection (≤20 unique values).

**Type inference**: Running inference over sampled values per column. Priority order: `integer` > `float` > `boolean` > `string`. If mixed types detected, report `"mixed"`. Once a column is classified as `"string"`, it stays string (no promotion back to numeric).

### 3.6 CSV/TSV Parsing

**Delimiter detection** (`.csv` files only — `.tsv` always uses `\t`):

```
Algorithm:
  1. Read first 5 lines of the file
  2. For each candidate delimiter [',', ';', '|', '\t']:
     a. Split each line by delimiter
     b. Count fields per line
     c. Score = consistency of field count across lines × total fields
  3. Select delimiter with highest score
  4. Default to ',' if ambiguous (tie or all scores equal)
```

**Encoding detection**:
1. Check for BOM: UTF-16 LE/BE → transcode; UTF-8 BOM → strip BOM
2. Attempt UTF-8 parse
3. On invalid UTF-8: fall back to Latin-1 via `golang.org/x/text/encoding/charmap`

**Header assumption**: Row 0 is always treated as the header row. No heuristic detection in v1.

### 3.7 Excel Parsing

**Library**: `github.com/xuri/excelize/v2` (MIT, no CGO, ~5MB binary increase)

**Multi-sheet handling**:
1. Read all sheet names and compute per-sheet row/column counts → `sheets` array
2. Set `activeSheet` to the first sheet (or the workbook's default active sheet)
3. Full profiling (columns, statistics, sample rows) only for the active sheet
4. All sheet data is cached; the model can query any sheet via `jq_cached_data` using sheet-qualified paths

**Data extraction**: `excelize` returns cell values as strings. The streaming profiler handles type inference identically to CSV.

### 3.8 Large JSON Array Profiling

When a `.json` file exceeds the 200-line / 10KB threshold:
1. Parse the top-level structure
2. If it's an array of objects: treat as tabular (objects are rows, keys are columns)
3. If it's a single object or non-tabular array: fall through to the existing Compaction Pipeline via `cache.Process()`

### 3.9 Cache Storage — Path Reference Mode

New storage mode for the `CacheStore` interface:

```go
// In sqlCacheStore.StoreFileRef — new method
// Stores a reference to an on-disk file rather than copying content
func (s *sqlCacheStore) StoreFileRef(ctx context.Context, filePath string, envelope CacheEnvelope) (string, string, error)
```

The `disk_cache` table gains a nullable `file_path` column:
```sql
ALTER TABLE disk_cache ADD COLUMN file_path TEXT;
```

When `file_path` is non-null, `read_cached_data` and `jq_cached_data` lazily parse from the file instead of reading `raw_payload`. The `raw_payload` column is left empty for path-referenced entries.

**Query behavior**: The existing `jq_cached_data` tool needs to handle CSV/TSV parsing (not just JSON). When the referenced file is a CSV/TSV, the tool first converts it to a JSON array of objects, then applies the JQ filter. This conversion happens on-demand per query, not pre-computed.

---

## 4. Cache Bridge Node

### 4.1 Node Definition

```go
GraphNode{
    ID:           "cache_bridge_{sourceNodeID}",
    Type:         "action",
    Action:       "jq_cached_data",
    Instructions: "Query the cached tabular data from the upstream node's Data Profile. " +
                  "Use the cacheId from the upstream output to access the data via jq_cached_data. " +
                  "Return the most relevant subset of data for the downstream task.",
    Status:       "pending",
    AllowedTools: []string{"introspect_cache", "read_cached_data", "jq_cached_data"},
    ActivationThreshold: 0.0, // Deterministic — no Edge Thought overhead
}
```

### 4.2 Compile-Time Injection (Kahn Compiler)

Location: `internal/compiler/sct_compiler.go`, alongside existing Recall Node injection.

```
Algorithm:
  FOR each node in the abstract graph:
    IF node's prompt/instructions reference a tabular file extension (.csv, .tsv, .xlsx, .xls):
      IF NO downstream node has cache tools in allowedTools:
        Inject Cache Bridge Node between this node and all its downstream targets
        Re-wire edges: node → cache_bridge → [original downstream targets]
```

**Deduplication**: Scan outgoing edges. If any child node already has `jq_cached_data`, `read_cached_data`, or `introspect_cache` in its `allowedTools`, skip injection. Same pattern as `hasPlannedSynthesisChild` for Recall Nodes.

**Extension detection in prompts**: Simple regex match for file extensions in the node's `Instructions` field:

```go
var tabularExtRe = regexp.MustCompile(`\.(csv|tsv|xlsx|xls)\b`)
```

### 4.3 Runtime Injection (Executor Post-Execution Hook)

Location: `internal/executor/ready_queue.go`, as a post-execution check after node completion.

```
Algorithm:
  AFTER a node completes:
    IF node output contains "cacheId" AND "dataProfile":
      IF NO downstream node has cache tools in allowedTools:
        IF NO compile-time Cache Bridge already exists (no node with ID prefix "cache_bridge_"):
          Call ApplySpawn(graph, nodeID, cacheBridgeNode)
          Register new node in nodeIndex
          Set node state to "pending" in SQLite
```

This catches:
- Files whose format wasn't predictable from the prompt (e.g., a variable path)
- JSON files that exceeded the size threshold at runtime
- Any case where the planner didn't anticipate the Data Profiler triggering

### 4.4 Deduplication Between Layers

The runtime layer skips injection if:
1. Any downstream node already has cache tools (planner handled it), OR
2. A node with ID matching `cache_bridge_{thisNodeID}` already exists (compiler handled it)

This prevents double-bridging when both layers detect the same tabular file.

---

## 5. Probe Node Integration

### 5.1 AllowedTools Auto-Expansion

Location: `internal/compiler/sct_compiler.go`

When a Probe Node's `allowedTools` contains `read_file`, the compiler automatically appends:
- `introspect_cache`
- `read_cached_data`
- `jq_cached_data`

This ensures the Thought Chain can seamlessly transition from receiving a Data Profile envelope to querying cached data in subsequent steps.

### 5.2 Thought Chain Behavior

No changes to the Thought Chain execution loop. When `read_file` returns a Data Profile:
1. Step N: Probe calls `read_file("data.csv")` → receives profile with cacheId
2. Step N+1: Probe sees cacheId in its accumulated context → calls `jq_cached_data` with appropriate filter
3. Subsequent steps: iterate with different JQ filters as needed

The existing Cache Exploration Guide injection is already triggered by cacheId detection in the accumulated context.

---

## 6. `peek_file` Behavior

`peek_file` continues to return raw content (first 20 lines) for ALL file types, including tabular files. No profiling, no cache setup.

When the file extension matches a tabular format (`.csv`, `.tsv`, `.xlsx`, `.xls`), append to the ToolResult hint:

```
This is a tabular data file. Use read_file for full profiling and cached data access.
```

For `.xlsx`/`.xls` files, `peek_file` returns an error with a hint since Excel files are binary:

```
Cannot peek binary Excel file. Use read_file for full profiling and cached data access.
```

---

## 7. Code Organization

### New Files

| File | Contents |
|------|----------|
| `internal/tools/tabular.go` | `ProfileTabularFile()`, `StreamingProfiler`, delimiter detection, encoding detection, type inference, adaptive sampling |
| `internal/tools/tabular_test.go` | Unit tests for profiling, edge cases, adaptive sizing |
| `internal/tools/excel.go` | Excel-specific parsing via excelize, multi-sheet handling |
| `internal/tools/excel_test.go` | Excel parsing tests |

### Modified Files

| File | Changes |
|------|---------|
| `internal/tools/filesystem.go` | `NewReadFileTool`: add extension detection, delegate to `ProfileTabularFile()`. `NewPeekFileTool`: add tabular hint. |
| `internal/cache/cache.go` | `CacheStore` interface: add `StoreFileRef()`. `sqlCacheStore`: implement path-reference storage. |
| `internal/compiler/sct_compiler.go` | Add Cache Bridge Node injection after tabular extension detection. Add cache tools to Probe `allowedTools`. |
| `internal/executor/ready_queue.go` | Add post-execution hook for runtime Cache Bridge injection. |
| `go.mod` | Add `github.com/xuri/excelize/v2`, `golang.org/x/text` dependencies. |

### Database Migration

```sql
-- Migration: add file_path column to disk_cache
ALTER TABLE disk_cache ADD COLUMN file_path TEXT;
```

Add to the migration list in `internal/memory/migrations.go`.

---

## 8. Dependencies

| Dependency | Purpose | License | Binary Impact |
|-----------|---------|---------|---------------|
| `github.com/xuri/excelize/v2` | Excel parsing (.xlsx/.xls) | MIT | ~5MB |
| `golang.org/x/text/encoding` | Character encoding detection/transcoding | BSD-3 | Minimal (likely already transitive) |

---

## 9. Test Plan

### Unit Tests

| Test | File | What it validates |
|------|------|-------------------|
| CSV profiling | `tabular_test.go` | Correct column types, null rates, cardinality, sample rows for a known CSV |
| TSV profiling | `tabular_test.go` | Tab-delimited parsing |
| Delimiter sniffing | `tabular_test.go` | Correctly identifies `,`, `;`, `\|`, `\t` from file content |
| Adaptive sampling | `tabular_test.go` | 5→3→1 cascade when sample exceeds 10K chars |
| Wide CSV (200 cols) | `tabular_test.go` | Single row exceeds budget → reduces to 1 sample row |
| Column pruning | `tabular_test.go` | High-null, constant, and high-cardinality-string columns dropped from samples |
| Cardinality cap | `tabular_test.go` | Columns with >1000 unique values report `">1000"` |
| Encoding fallback | `tabular_test.go` | Latin-1 and UTF-16 BOM files parse correctly |
| Headerless CSV | `tabular_test.go` | Row 0 treated as header (known limitation) |
| Excel multi-sheet | `excel_test.go` | All sheets summarized, first sheet profiled |
| JSON conditional | `tabular_test.go` | Small JSON returns raw, large JSON profiles |
| Cache Bridge compile-time | `compiler_test.go` | Bridge injected when tabular extension detected in prompt |
| Cache Bridge dedup | `compiler_test.go` | Bridge NOT injected when downstream has cache tools |
| Cache Bridge runtime | `ready_queue_test.go` | Bridge spawned when node output contains dataProfile |
| Probe allowedTools | `compiler_test.go` | Cache tools auto-added when read_file in Probe tools |
| Path-reference cache | `cache_test.go` | File-backed cache entries read from original path |

### Integration Tests

| Test | What it validates |
|------|-------------------|
| End-to-end CSV DAG | Probe reads CSV → gets profile → queries via jq_cached_data → synthesizes result |
| Planner-unaware CSV | Action node reads CSV unexpectedly → runtime Cache Bridge injected → downstream succeeds |
| Excel workflow | read_file on .xlsx → profile with sheet summary → cache query on specific sheet |
