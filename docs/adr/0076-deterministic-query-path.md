# ADR-0076: Deterministic Query Path for Analyze Nodes

**Status:** Proposed
**Date:** 2026-08-10
**Deciders:** JP
**Context:** 8 cycles of datanal red-team (C19–C26) plateaued at 1.75–3.50/5 with high variance from FM-14 (cache not found), FM-17 (filter extraction unreliable), FM-18 (plan stochasticity).

## Decision

For data analysis tasks, **deterministic regex-based intent extraction and SQL assembly take priority over model-driven tool selection** when extraction confidence is high. A three-band confidence routing system (Deterministic ≥ 0.70, Warm 0.50–0.69, Cold < 0.50) controls whether the Analyze Node's query phase uses the new Deterministic Query Path or falls back to the existing Thought Chain.

### Key Design Choices

1. **Regex-first, model-fallback:** Per-phrase regex scanning extracts filter, group-by, aggregate, and order signals deterministically. The 4B model runs only for fields the regex didn't resolve. Neural embedding resolves column names when string matching fails.

2. **Multi-filter QueryIntent:** Expanded from single-filter to support compound WHERE clauses. Composed into single SQL statements, avoiding the intermediate derived tables that caused FM-14.

3. **Derived Cache Table materialization:** GROUP BY results are always materialized in the ephemeral query DB as a safety net, regardless of which confidence band executes. Idempotent with deterministic naming (`cache_derived_{hash}`).

4. **Synthetic ThoughtStep injection:** The Deterministic Query Path injects a synthetic ThoughtStep tagged as `sql_cached_data` so downstream Recall Node compaction and VTE verification receive query data without code changes.

5. **Validation guards with demotion:** Empty result sets and aggregate row-count anomalies demote from Deterministic to Warm, providing a recovery path without total failure.

## Alternatives Considered

### A. Bottom-up independent fixes
Fix FM-14, FM-17, FM-18 separately without shared architecture. Lower risk but doesn't address the root cause (model-driven tool selection for inherently deterministic queries). The compound tool surface (group_by, count_by, filter_where, top_n, query_builder) remains confusing for the 4B model.

### B. Single-shot query executor (full determinism)
Collapse the entire query phase into deterministic steps with no thought chain fallback. Eliminates variance completely but loses flexibility for queries the intent extractor can't express (multi-join, subquery, exploratory analysis).

## Consequences

- **FM-14 eliminated:** Derived tables always materialized; compound SQL avoids intermediate tables on the Deterministic path.
- **FM-18 mitigated:** DAG structure variance is irrelevant when the Analyze Node takes the Deterministic path.
- **FM-16 mitigated:** Deterministic path guarantees data injection into synthesis context.
- **FM-17 mitigated:** Regex-first extraction avoids the unreliable binary gate for known filter patterns.
- **Two code paths to maintain:** Deterministic and Thought Chain. The Warm band bridges them by injecting partial intent as hints.
- **Calibration dependency:** The 0.70 threshold needs benchmark validation and may need tuning.

## References

- Design spec: `docs/superpowers/specs/2026-08-10-deterministic-query-path-design.md`
- ADR-0050: Analyze Node
- ADR-0074: Structured Query Composition
- ADR-0075: Neural Embedding Sidecar
