# ADR-0046: Data Profiler and Cache Bridge Node

## Status

Proposed

## Context

The `read_file` tool (ADR-0019) was designed for source code: it returns raw file content, capped at 500 lines, and explicitly bypasses the **Compaction Pipeline**. This works well for code files where the **Local Model** needs verbatim content to reason about structure and logic.

However, tabular data files (CSV, TSV, Excel, large JSON arrays) have fundamentally different properties that make raw line dumping ineffective:

1. **Depth problem**: A 50K-row CSV would require 100+ sequential `read_file` calls at the 500-line cap. The **Local Model** cannot hold all rows in its context window, and earlier rows are forgotten before later ones are read. Meaningful aggregation or analysis is impossible.

2. **Width problem**: A single CSV row can have hundreds of columns. Enterprise CRM exports, financial datasets, and Excel reports routinely produce rows exceeding 5,000 characters — potentially exceeding the **Local Model**'s entire context window on a single line.

3. **The Compaction Pipeline already solves this for API outputs** (ADR-0005), but is not connected to `read_file`. The pipeline's L2 (tabular JSON → TSV), L4 (flatten nested hierarchies), and **Disk-Backed JQ Cache** envelope mechanism handle exactly these problems — for API responses. Files on disk are structurally identical but bypass all of it.

### Considered Options

- **Option A: Keep `read_file` raw, add a separate `profile_data` tool.** Rejected — the **Local Model** would still call `read_file` on CSVs out of habit. The model doesn't know to use a specialized tool unless it's steered, and adding tool selection friction to every data file interaction creates a new failure mode.

- **Option B: Compaction Pipeline at `read_file` output.** Rejected — the Compaction Pipeline is designed for JSON API outputs, not raw CSV/TSV. Running a CSV through JSON-first layers (L2, L3, L4) would require unnecessary format conversions.

- **Option C: Content-aware routing in `read_file` with deterministic graph expansion.** Selected — `read_file` detects tabular file formats, profiles them, and returns a structured envelope. The DAG engine deterministically injects **Cache Bridge Nodes** to ensure downstream consumers can access the cached data.

## Decision

### 1. Data Profiler in `read_file`

`read_file` gains a content-aware routing layer that detects tabular files by extension and returns a **Data Profile** instead of raw content. This narrows the ADR-0019 Compaction Pipeline bypass to non-tabular files only.

**Routing rules:**

| Format | Condition | Treatment |
|--------|-----------|-----------|
| `.csv`, `.tsv` | Always | Data Profile |
| `.xlsx`, `.xls` | Always | Data Profile |
| `.json` | >200 lines OR >10KB | Data Profile |
| `.json` | ≤200 lines AND ≤10KB | Raw (current behavior) |
| All other formats | Always | Raw (current behavior) |

CSV/TSV files are always profiled regardless of size because the width problem exists independent of row count.

**Profile output structure:**

```json
{
  "dataProfile": {
    "format": "csv",
    "path": "/path/to/sales.csv",
    "delimiter": ",",
    "rowCount": 49832,
    "columnCount": 14,
    "fileSizeBytes": 8421900,
    "columns": [
      {"name": "id", "type": "integer", "nullRate": 0.0, "cardinality": 49832},
      {"name": "status", "type": "enum", "nullRate": 0.02, "cardinality": 3, "values": ["active", "inactive", "pending"]},
      {"name": "revenue", "type": "float", "nullRate": 0.15, "min": 0.0, "max": 9842000.50}
    ],
    "sampleRows": "id\tname\tstatus\trevenue\n1\tAcme Corp\tactive\t142000.50\n...",
    "cacheId": "cache_1720882934"
  }
}
```

**Adaptive sample sizing:** Start with 5 sample rows (first 3 + 2 reservoir-sampled). If the TSV string exceeds 10K characters, reduce to 3 rows; if still over, reduce to 1 row. This handles arbitrarily wide files gracefully.

**Excel multi-sheet handling:** Uses `excelize/v2` (MIT, no CGO). Returns per-sheet summary (name, row count, column count) for all sheets, but only includes full profiling (columns, statistics, sample rows) for the first sheet. The model can drill into other sheets via `jq_cached_data`.

**Column pruning:** Deterministic statistical pruning only — no LLM inference call at `read_file` time. Columns that are >90% null, >95% unique (likely IDs/free-text), or single-constant-value are dropped from sample rows but retained in the schema. LLM-based column pruning via the existing `ColumnPruner` continues to apply if the node output later flows through the Compaction Pipeline at the DAG level.

**CSV edge cases:**
- **Headers:** Always assume row 0 is a header. Headerless files will have odd column names but remain fully queryable via cache tools.
- **Delimiter detection:** Sniff the first 5 lines for the most frequent delimiter from candidates (`,`, `;`, `|`, `\t`). Default to comma if ambiguous.
- **Encoding:** Detect BOM for UTF-16/UTF-8-BOM, attempt UTF-8, fall back to Latin-1 via `golang.org/x/text/encoding`.

**Performance:**
- **Streaming parse:** Stream the file line-by-line. Update running accumulators for row count, per-column null count, type inference, and min/max.
- **Cardinality cap:** Track distinct values per column up to 1,000. Beyond that, report `">1000"`. Sufficient for enum detection (≤20 unique values).
- **Reservoir sampling:** Select random sample rows during the single-pass stream. No second pass needed.

**Cache storage:** Store a **path reference** to the original file, not a copy. Files are durable on disk — unlike ephemeral API payloads, copying a 500MB CSV into `.tzro/cache/` is wasteful. The `read_cached_data` and `jq_cached_data` tools lazily parse from the original file path when queried.

### 2. Cache Bridge Node

A lightweight deterministic node auto-injected between a node that produces a **Disk-Backed JQ Cache** envelope and its downstream consumers. Fires unconditionally — one local inference call is cheap insurance against downstream nodes receiving an opaque envelope they can't use.

**Two-layer injection with deduplication:**

**Layer 1 — Compile-time (Kahn Compiler):** When the SCT compiler detects a node whose prompt references a tabular file extension (`.csv`, `.tsv`, `.xlsx`, `.xls`), it checks whether any downstream node already has cache exploration tools (`introspect_cache`, `read_cached_data`, `jq_cached_data`) in its `allowedTools`. If not, it injects a Cache Bridge Node between the reading node and its downstream targets. Same pattern as Recall Node injection for Probe Nodes.

**Layer 2 — Runtime (Executor post-execution hook):** When a node completes and its output contains a `cacheId` from the Data Profiler, the executor checks downstream nodes for cache tools. If none exist and no compile-time Cache Bridge was already injected (detected by node ID prefix `cache_bridge_`), it uses `ApplySpawn` to insert a Cache Bridge Node. This catches cases where the planner didn't anticipate the file format.

### 3. Probe Node Integration

When a **Probe Node**'s `allowedTools` contains `read_file`, the SCT compiler automatically appends `introspect_cache`, `read_cached_data`, and `jq_cached_data` to the allowed set. This ensures the **Thought Chain** can seamlessly transition from receiving a Data Profile envelope to querying the cached data in subsequent steps.

### 4. `peek_file` Behavior

`peek_file` continues to return raw content (first 20 lines) for all file types, including tabular files. When the file is a tabular format, a hint is appended: *"This is a tabular data file (.csv). Use read_file for full profiling and cached data access."* This preserves `peek_file`'s simplicity while steering the model toward the Data Profiler path.

## Consequences

- **Enables tabular data analysis:** The **Local Model** can now reason about arbitrarily large CSV/TSV/Excel files through schema awareness, statistical grounding, and targeted cache queries — without needing to read 50K rows sequentially.
- **Narrows ADR-0019 bypass:** The Compaction Pipeline bypass now applies to non-tabular files only. Source code, markdown, and other text files continue to be injected raw.
- **New dependency:** `excelize/v2` adds ~5MB to the binary for Excel support.
- **Path-reference brittleness:** If the original file is modified or deleted during task execution, cache queries will return stale or failed results. Acceptable trade-off vs. copying large files.
- **Graph mutation overhead:** Two-layer Cache Bridge Node injection adds complexity to both the compiler and executor. Mitigated by following the established Recall Node injection pattern.
- **Local Model tool surface:** Probe Nodes with filesystem tools gain three additional cache tools in their schema reference, slightly increasing prompt size.
