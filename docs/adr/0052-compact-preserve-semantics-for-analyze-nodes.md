# ADR-0052: CompactPreserve Semantics for Analyze Nodes

## Status

Accepted

## Context

ADR-0050 introduced the **Analyze Node** type and specified that the **Kahn Compiler** should set `CompactionLevel: CompactPreserve` on its `ProbeConfig`. The intent was to prevent the **Structured Compactor** from destroying SQL query results during rolling compaction — results that ARE the deliverable, unlike probe nodes where tool outputs are navigational breadcrumbs.

However, the `CompactionLevel` parameter was never threaded through to the compaction implementation. The `compactThoughtChain` function accepted it as a parameter but ignored it entirely:

```go
// The compactionLevel parameter is retained for API compatibility but
// the structured compactor handles content-type-aware compaction internally.
```

This caused analyze nodes to receive the same deterministic compaction as probe nodes — `CompactContent` applied skeleton extraction to code outputs and tabular truncation to SQL results, discarding the actual aggregation data. The benchmark `results-data-2` showed 2/5 tasks producing prose summaries ("SQL returned results") instead of actual data.

### Considered Options

- **Option A: CompactPreserve means no compaction at all.** The literal reading of the `"preserve"` name — skip both tool output compaction AND reasoning text compression. Rejected because analyze nodes can run 15 steps of SQL exploration, and their reasoning text ("I will try GROUP BY next...") adds significant context pressure without value. Compressing reasoning is safe and keeps the context window lean.

- **Option B: CompactPreserve means preserve tool outputs, compress reasoning.** Selected — tool output data (SQL results, cache introspection) is the deliverable and must survive compaction verbatim. Reasoning text is the model's internal deliberation and can be safely compressed by the 1B router.

- **Option C: Skip compaction entirely for analyze nodes.** Rejected — while analyze nodes have small step budgets (15), skipping reasoning compression risks context overflow on edge cases with many verbose SQL results.

## Decision

`CompactPreserve` semantics are defined as: **preserve tool output data verbatim; reasoning text is still LLM-compressed.**

Implementation: `CompactSteps` accepts a `preserveToolOutput bool` parameter. When true, the `CompactContent` call on tool outputs is skipped — they pass through raw. The `compactReasoning` call on thought text continues to run regardless.

The caller (`compactThoughtChain`) translates the `CompactionLevel` into this flag:

```go
preserveOutput := compactionLevel == compiler.CompactPreserve
```

## Consequences

### Positive

- **Fixes data loss in analyze node synthesis**: SQL results, cache introspection output, and aggregation data survive compaction and are available to the synthesis pass.
- **Minimal blast radius**: Only affects nodes with `CompactionLevel: CompactPreserve` (currently only analyze nodes). Probe nodes and all other compaction paths are unchanged.
- **Context budget still managed**: Reasoning compression continues to run, preventing context overflow from verbose model deliberation.

### Negative

- **Larger compacted summaries for analyze nodes**: Preserving raw SQL results (typically 1-3KB each) produces larger rolling summaries than skeleton-extracted ones. Mitigated by the analyze node's smaller step budget (15 vs 30) and typically small SQL result sizes.
- **CompactPreserve name is slightly misleading**: It preserves tool outputs but not reasoning. The doc comment clarifies this, but the name alone could confuse future readers.

## References

- ADR-0046: Structured Content-Aware Compaction
- ADR-0050: Analyze Node
- ADR-0051: SQL Query Language for Cached Data
