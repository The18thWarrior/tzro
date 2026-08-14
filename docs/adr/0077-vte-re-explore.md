# ADR-0077: VTE Re-Explore Outcome

**Status**: Accepted
**Date**: 2025-08-14
**Deciders**: JP
**Context**: Benchmark results-full-29 analysis

## Context

The Verified Task Execution (ADR-0067) pipeline has three VTE outcomes:
1. **Accepted** — local synthesis passes, returned as-is
2. **Rejected + Re-Synthesis** — cloud rewrites from the same `refinedContext`
3. **Rejected + Scatter** — goal decomposed into missing sub-items (ADR-0071)

Benchmark analysis revealed a failure mode none of these cover: **insufficient upstream data collection**. When a web research probe is misrouted to filesystem tools (Fix 4/Fix 6 from the same batch), the `refinedContext` contains filesystem exploration logs instead of web research. Cloud Re-Synthesis cannot fix this — it receives the same empty context and produces the same hallucinated output.

## Decision

Add a fourth VTE outcome: **Re-Explore**.

### Schema Changes

`VerificationResult` gains two fields:
- `reExplore bool` — signals the exploration phase needs re-running
- `reExploreHint string` — cloud-generated guidance for the re-exploration

### Detection

The cloud Verification Gate prompt includes a RE-EXPLORE DETECTION instruction:
> If the output reports tool failures, query errors, or explores the local filesystem instead of answering the research question, set `reExplore` to true with a `reExploreHint`.

### Consumption

When `vResult.ReExplore == true`:
1. The re-explore signal is returned immediately (no re-synthesis attempted)
2. The consuming strategy re-runs the exploration node with the `reExploreHint`
3. Budget-gated: max 1 re-explore per task (prevents infinite loops)

### Outcome Priority

The VTE pipeline evaluates outcomes in this order:
1. Scatter (coverage gaps with decomposable goal items)
2. Re-Explore (data collection failure — context is insufficient)
3. Re-Synthesis (writing quality failure — context is sufficient)

Re-Explore takes priority over Re-Synthesis because re-synthesizing from empty/wrong context produces the same bad output.

## Consequences

- Cloud model must understand the distinction between "poorly written" (re-synthesize) and "insufficient data" (re-explore)
- Re-explore adds one additional exploration round, roughly doubling probe cost for the affected task
- Fixes the class of failures where web probes were misrouted to filesystem exploration
- The consuming strategy must handle the new `ReExplore` field — existing callers that don't check it will fall through to the existing re-synthesis path (safe degradation)

## Related

- ADR-0067: Verified Task Execution
- ADR-0071: Item-Level Scatter
- Fix 4: Web-specific probe phase template (same batch)
- Fix 6: `filterTools` safety fix (same batch)
