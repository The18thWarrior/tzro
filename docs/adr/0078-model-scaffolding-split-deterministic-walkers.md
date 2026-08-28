# ADR-0078: Model/Scaffolding Split — Deterministic Walkers & Defensive Re-Synthesis

**Status**: Accepted
**Date**: 2026-08-14
**Deciders**: JP
**Context**: Benchmark runs 20–30 analysis, handoff-model-scaffolding-split.md

## Context

Empirical analysis across 11 benchmark runs (runs 20–30) established that the on-device 4B Local Model produces valid step-level tool routing decisions (tool choice, parameter extraction) only ~10% of the time. The previous PhaseRunner attempted to run a two-pass step loop (Pass 1 Worker reasoning + Pass 2 Router GBNF extraction) on every step, burning dozens of inference calls per node that were almost entirely discarded and overridden by scaffolding hooks (`ExplorationQueue`, `ToolFixup`).

Conversely, the 4B model excels at **completion and structured summarization from clear context and specs** (quality 4.5–5.0).

Additionally, Run 30 suffered a regression when the Verification Gate's `reExplore: true` signal returned the rejected local synthesis without executing re-exploration or falling back to Cloud Re-Synthesis.

## Decision

1. **Eliminate Step-Level LLM Inference in Tool Execution**:
   - Delete `two_pass.go` and remove step-level Pass 1/Pass 2 inference from `PhaseRunner`.
   - Tool execution within `probe`, `research`, and `analyze` phases is owned by **Deterministic Walkers** (`DeterministicQueueDriver`).
   - LLM inference is strictly reserved for:
     - Macro Planning / Template Mutation (Task intake)
     - Upfront Query Decomposition (1-shot Worker call for web research)
     - Codegen from Spec (`tzro_code` / Edit Loop)
     - Terminal Synthesis (Recall Node / Phase Summary)

2. **Web Research Pipeline**:
   - Stage 1: `QueryGenerator` runs a single 1-shot Worker call with GBNF array grammar to generate 2–3 distinct search queries (with deterministic regex fallback).
   - Stage 2: `DeterministicQueueDriver` executes `web_search` for each query.
   - Stage 3: `ToolPostProcess` extracts and deduplicates URLs into a `DiscoveredURLs` queue; top $K$ URLs are browsed via `web_browse`.
   - Stage 4: 1-shot Worker synthesis produces the final research summary.

3. **VTE Recovery & Defensive Re-Synthesis Invariant**:
   - `VerifyTaskOutput` enforces that on rejection, a valid **Cloud Re-Synthesis** is always captured as a safety baseline.
   - When `vResult.ReExplore == true` and re-explore budget remains ($\le 1$ attempt per task), the `RecallNode` executes an in-place re-exploration pass with `reExploreHint` prepended to the queue.
   - If re-exploration fails or budget is exhausted, the engine falls back to `vResult.ReSynthesis`. The engine **never** returns rejected local synthesis.

## Consequences

- Saves ~100–150 local LLM calls per benchmark run (~95% latency reduction in exploration phases).
- Eliminates empty parameter hallucinations (`web_search` with empty query, `read_file` with empty path).
- Guaranteed termination: queues have bounded lengths and fail gracefully on tool errors.
- Guarantees high-quality VTE outputs by preventing broken local synthesis from bypassing cloud re-synthesis.

## Related

- ADR-0048: Plan Template Registry
- ADR-0067: Verified Task Execution
- ADR-0074: Structured Query Composition
- ADR-0076: Deterministic Query Path
- ADR-0077: VTE Re-Explore Outcome
