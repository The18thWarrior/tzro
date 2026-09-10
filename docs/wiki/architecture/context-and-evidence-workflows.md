# Context and Evidence Workflows

Status: implemented. All five workflows and shared infrastructure shipped in tzro v2.

The [Context and Evidence Workflows map](../../../.scratch/context-and-evidence-workflows/MAP.md) succeeded [Context Foundation Hardening](../../../.scratch/context-foundation-hardening/MAP.md).
It developed the five proposals from the [Tzro Capability Evaluation](tzro-next-capabilities-evaluation-2026-09-08.md) into agreed workflow contracts, followed by complete TDD implementation across `pkg/evidence`, `pkg/compactor`, `pkg/session`, `pkg/context`, `pkg/search`, and `pkg/inspector`.

| Use case / Workflow | Status | Primary Package | Developer Scenario & CLI Surface |
| --- | --- | --- | --- |
| [Change-Impact Context Packs](../../../.scratch/context-and-evidence-workflows/issues/01-change-impact-context-packs.md) | Release-Gating | `pkg/context` | Identify affected consumers, tests, and constraints before changes to shared code via `tzro impact`. Uses `ReferenceAdapter` interface with `GoGrepAdapter` implementation and `CoverageReport` envelope. |
| [Automatic Task Continuity across Agent Clients](../../../.scratch/context-and-evidence-workflows/issues/02-automatic-task-continuity.md) | Release-Gating | `pkg/session` | Resume an interrupted task with applicable state and stale evidence identified via `tzro session`. Supports Schema v2 with `ScopeFiles` per check, snapshot hash freshness validation, missing artifact detection, and graceful degradation. |
| [Compaction with Explicit Evidence Guarantees](../../../.scratch/context-and-evidence-workflows/issues/03-compaction-evidence-guarantees.md) | Release-Gating | `pkg/compactor` | Retain failure outcomes and retrievable source evidence in compact output via `tzro compact`. Wraps commands (`--run`) or stdin with JUnit XML, Go test JSON, GNU diagnostics, and DLP-scanned fallback, enforcing a 10-line inline cap and store-backed expansion hashes. |
| [Unified Local Evidence Search](../../../.scratch/context-and-evidence-workflows/issues/04-unified-local-evidence-search.md) | Preview | `pkg/search` | Combine code, specifications, decisions, and captured logs with clear origins via `tzro search`. Queries across 4 source categories (repo text files, pure-Go DOCX/PPTX/PDF extractors, tabular discovery markers, Store artifacts) with content-hash deduplication and silent privacy omissions. |
| [Local Context Inspector and Quality Replay](../../../.scratch/context-and-evidence-workflows/issues/05-local-context-inspector-quality-replay.md) | Preview | `pkg/inspector` | Explain context omissions and compare context configurations offline via `tzro inspect`. Features an always-on 6-stage context trace (Discovery, Filtering, Ranking, Packing, Transformation, Policy), counterfactual offline replay, 3-tier epistemological labeling, external harness outcome hooks, and 50 MB LRU quotas. |

### Shared Infrastructure

1. **Evidence Provenance Envelope (`pkg/evidence`)**:
   - Deterministic `SourceKind` enumeration: `code`, `config`, `doc`, `log`, `session`, `import`, `data`.
   - `Anchor` union: Line ranges (`StartLine`, `EndLine`) or section paths (`SectionPath`).
   - Dual staleness flags (git commit distance $\ge 100$ and calendar age $\ge 90$ days).
2. **Workspace Privacy Policy (`pkg/dlp`)**:
   - Strict workspace-scoped policy loading from `.tzro/privacy.json`.
   - Early pruning before reads or indexing; silent omission on deny/block to prevent path disclosure.
3. **Content-Hash Store Extensions (`pkg/store`)**:
   - `context_traces` and `trace_outcomes` tables with workspace isolation.
   - 50 MB default LRU quota eviction per workspace for traces.
   - Nanosecond timestamp precision for all manifests, artifacts, and traces.
4. **Session Manifest & Hybrid Capture (`pkg/session`)**:
   - Schema version 2 with `ScopeFiles` attached to `CheckExecution`.
   - `ValidateCheckFreshness` comparing against `SnapshotHash` per check.
   - Four-tier resolution: Explicit ID $\to$ Git branch match $\to$ Workspace fallback $\to$ Clean start.
5. **Compaction Evidence Contract (`pkg/compactor`)**:
   - Structured `CompactedEvidence` envelope with exit code confidence (`observed`, `inferred`, `unknown`).
   - Diagnostics with severity, test ID, location, duration, and 10-line inline output with overflow hash.
