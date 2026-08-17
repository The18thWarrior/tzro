# ADR-0079: Milestone Verification and Dependency-Gated Recall

**Status**: Accepted  
**Date**: 2026-08-14  
**Deciders**: JP  
**Context**: Benchmark results-full-31 analysis & Multi-Probe DAG profiling

---

## Context

In Verified Task Execution (ADR-0067), the Verification Gate originally evaluated all nodes against the global task goal (`graph.GoalPrompt`) using a terminal 4-dimension rubric (`goalAlignment`, `factualGrounding`, `coherence`, `completeness >= 0.6`).

Profiling benchmark runs on multi-layer tasks (such as `inference_module_docs` and `adr_summary`) revealed two severe architectural issues:

1. **Global Goal Mismatch on Intermediate Probes**: When a task decomposed into multiple sequential probes (e.g. `explore_core`, `explore_local`, `explore_routing`, `explore_support`), the compiler injected an intermediate Recall node and Verification Gate after *every* probe. Evaluating a single-layer probe against the global 4-layer goal caused 100% false-alarm rejections on `completeness`, triggering redundant and expensive cloud re-synthesis on intermediate stepping stones.
2. **The $N \times \text{Recall}$ Latency Multiplier**: In multi-probe exploration DAGs, injecting $N$ intermediate Recall nodes caused $N$ sequential local 4B context-prefill loops (processing 6,000–12,000 prompt tokens per step), inflating task wall-clock time from ~4 minutes to over 14 minutes.

We need a systematic way to verify mid-flight execution soundness without penalizing intermediate nodes for incomplete global deliverables, while eliminating redundant intermediate synthesis loops.

---

## Decision

### 1. Dual-Mode Verification Gate
`VerifyTaskOutput` operates in two distinct modes:

- **Milestone Verification Gate (Mid-Flight Nodes)**:
  - **Input**: Evaluated against the specific step contract (`node.Instructions`) and local gathered evidence.
  - **Milestone Rubric**: Scored on `{stepAlignment, factualGrounding, downstreamViability, reason, reExplore, reExploreHint}`.
  - **Threshold**: Accepted if `stepAlignment >= 0.60`, `factualGrounding >= 0.60`, and `downstreamViability >= 0.60`. Drops global `completeness` entirely.
- **Terminal Verification Gate (Final Deliverable)**:
  - **Input**: Evaluated against the global goal (`graph.GoalPrompt`) and quality rubrics.
  - **Verification Rubric**: Scored on the full 4-dimension schema (`goalAlignment`, `factualGrounding`, `coherence`, `completeness >= 0.60`).

### 2. Sink-Aware Re-Synthesis
When an intermediate milestone is rejected (`accepted == false`):
- **If `reExplore == true`**: Triggers in-place re-exploration with `reExploreHint` (budgeted $\le 1$ per task).
- **If `reExplore == false` (Synthesis Quality Failure)**:
  - If the node has outgoing edges to a **Tool Sink** (e.g. `write_file`, `save_memory`, `db_insert`), execute **Cloud Re-Synthesis** so the persisted external artifact is 100% sound.
  - If the node only feeds downstream reasoning or terminal synthesis, **skip cloud re-synthesis**. Forward raw node outputs and defer final re-synthesis to the Terminal Verification Gate.

### 3. Dependency-Gated Recall Injection
The Kahn Compiler replaces blind 1-to-1 Probe-to-Recall injection with **Dependency-Gated Recall Injection**:
- Intermediate Recall nodes (+ Milestone VTE) are injected after a Probe/Analyze node **only** when the node's output directly feeds an intermediate Tool Sink or a dynamic branching condition.
- Pure multi-probe exploration fan-outs bypass intermediate Recall nodes, routing raw evidence directly into a single consolidated terminal join node.

---

## Consequences

- **Positive**: Eliminates false-rejection cascades on intermediate multi-layer probes (e.g., T4/T5 research and docgen tasks).
- **Positive**: Cuts wall-clock execution time on multi-probe tasks by ~60% (from ~14 minutes to ~4.5–5 minutes) by eliminating redundant local 4B context-prefill loops.
- **Positive**: Guarantees artifact safety: file-writes (`write_file`) always receive verified, high-fidelity content via Proactive Splice.
- **Trade-off**: Requires the compiler to track tool sink dependencies during graph expansion.

---

## Related

- [ADR-0067: Verified Task Execution](0067-verified-task-execution.md)
- [ADR-0071: Pre-Flight Validation and 4B Failure Mode Mitigations](0071-pre-flight-validation-and-4b-failure-mode-mitigations.md)
- [ADR-0072: Mandatory Recall Injection for Single-Probe DAGs](0072-mandatory-recall-injection-for-single-probe-dags.md)
- [ADR-0077: VTE Re-Explore Outcome](0077-vte-re-explore.md)
- [ADR-0078: Model-Scaffolding Split and Deterministic Walkers](0078-model-scaffolding-split-deterministic-walkers.md)
