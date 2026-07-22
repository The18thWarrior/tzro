# ADR-0053: Analytical Evidence for Data Analysis Tasks

The 4B Local Model consistently fails to synthesize accurate data analysis results — hallucinating data values, entering repetition loops, and producing intent statements instead of actual results (observed: 0% pass rate across 5 benchmark tasks in Run 6). Rather than attempting to fix synthesis quality through prompt engineering or mandatory cloud synthesis, we change the output contract: successful `sql_cached_data` query results from Analyze Node thought chains are materialized into a dedicated `analytical_evidence` column on the task result row, alongside `terminal_synthesis`. The evidence is the primary ground-truth output; the synthesis text is best-effort commentary.

## Status

Proposed

## Considered Options

- **Option A: Mandatory cloud synthesis for analyze nodes.** Rejected — adds ~500 cloud tokens per synthesis and still depends on the cloud model interpreting compacted thought chain context correctly. Doesn't guarantee data fidelity.

- **Option B: Data anchor check (extend Symbol Anchor Check to data values).** Rejected as primary fix — the specific columns and data envelope aren't known at compile time, making reliable extraction fragile. The consuming harness (typically a frontier model) is better positioned to ground against raw query results than a post-hoc validation gate.

- **Option C: Analytical Evidence — ship raw query results alongside synthesis.** Selected — sidesteps the synthesis quality problem entirely. The consuming harness gets the actual data and can compose its own answer. The 4B model's synthesis becomes advisory, not authoritative.

## Decision

### 1. Evidence Capture (at tool dispatch time)

When `sql_cached_data` returns results inside an Analyze Node's probe loop, the executor stashes the query metadata into a side-channel list on the node:

```json
[
  {
    "sql": "SELECT Country, COUNT(*) as cnt FROM cache_123 GROUP BY Country ORDER BY cnt DESC",
    "rows": [{"Country": "USA", "cnt": 215}, ...],
    "totalRows": 30,
    "capped": true
  }
]
```

Each evidence item is capped at 5 result rows. The total row count and `capped` flag tell the consumer whether the full dataset was included.

### 2. Storage (new column on task result)

A new `analytical_evidence` TEXT column (JSON) on the task result row, alongside the existing `output_text` (terminal_synthesis). Written at task completion alongside the synthesis output.

### 3. Phase Gate for Synthesis Eligibility

Synthesis readiness in Analyze Nodes now uses a phase gate instead of a flat successful-call counter:

- `hasDiscovery`: any `introspect_cache` call succeeded (schema inspection)
- `hasAnalytical`: any `sql_cached_data` call succeeded (actual data query)

Synthesis requires `hasAnalytical == true`. This prevents premature synthesis after schema-only discovery calls.

### 4. Recall Node Injection (never skip for analyze)

The Kahn Compiler's sole-leaf skip rule is exempted for Analyze Nodes. Analyze nodes always get a downstream Recall Node, even when they are the only leaf in the graph. The probe's internal Pass 2 synthesis is not sufficient for data analysis results.

## Consequences

- **Harness consumers must handle the new column.** MCP Server Mode, CLI output, and Native Plugin Mode need to surface `analytical_evidence` alongside `terminal_synthesis`. Consumers that only read `output_text` continue to work but miss the ground-truth data.
- **Token efficiency.** A 5-row capped evidence item is ~200 tokens. For a task with 3 SQL queries, that's ~600 tokens of evidence — negligible vs. the ~180K local tokens consumed by the task.
- **Synthesis becomes advisory.** The synthesis text may still hallucinate, but it no longer matters for correctness — the evidence carries the data. This is a deliberate quality/cost trade-off.

## References

- ADR-0050: Analyze Node
- ADR-0051: SQL Query Language for Cached Data
- Benchmark Run 6 analysis (2026-07-21)
