# Astra Context Capabilities

> Five high-impact additions to Tzro v2's deterministic context layer.

**PRD:** [`.scratch/astra-context-capabilities/PRD.md`](../../../.scratch/astra-context-capabilities/PRD.md)  
**Source Specs:** [`docs/working-specs/astra-changes-plan`](../../working-specs/astra-changes-plan), [`docs/working-specs/astra-changes-architecture.md`](../../working-specs/astra-changes-architecture.md)  
**Handoff:** [`docs/working-specs/astra-changes-handoff`](../../working-specs/astra-changes-handoff)  
**Status:** ready-for-agent  
**Date:** 2026-09-08  

---

## Summary

Five capabilities that deepen Tzro's local-first context layer, ordered by dependency:

1. **Compatibility-Aware Automatic Shielding** — Fix proxy payload fidelity (unknown field preservation, tool-call IDs, provider adapters, doctor/inspect). Foundation prerequisite.
2. **Enforceable Local Privacy Policies** — Structured workspace allow/deny/redact/block policies, custom detectors, preview, audit. Foundation prerequisite.
3. **Task-Aware Context Packs** — Token-budgeted ranked retrieval with FTS5, structural relationships, and explainable inclusion. First visible feature.
4. **Reversible Compaction and Artifact Retrieval** — Immutable artifact store, full-original retention before compaction, range/query expansion.
5. **Portable Session Handoffs** — Versioned JSON manifest for agent session state with freshness-aware import.

## Key Modules

| Module | Package | Role |
|--------|---------|------|
| Context Pack Assembler | `pkg/context` | Ranked token-budgeted retrieval |
| Artifact Store | `pkg/store` (ext) | Immutable versioned artifact persistence |
| Reversible Compaction | `pkg/compactor` (ext) | Full-original storage + compact view |
| Session Manifest | `pkg/session` | Portable agent state export/import |
| Privacy Policy Engine | `pkg/dlp` (ext) | Workspace policies + audit |
| Provider Fidelity Layer | `pkg/kvlock` + `pkg/proxy` (ext) | Unknown-field preservation |
| Provider Adapters | `pkg/proxy/adapters/` | OpenAI Responses, Gemini, local |

## Domain Terms Used

- Token Shield, KV-Cache Prefix Lock Guard, Tree-Sitter AST Skeletonizer, Content-Hash Store, Discovery Engine, Zero-Cloud DLP — all per [CONTEXT.md](../../../CONTEXT.md).

## Implementation Issues

The PRD has been decomposed into 16 vertical tracer-bullet issues under [`.scratch/astra-context-capabilities/issues/`](../../../.scratch/astra-context-capabilities/issues/):

1. [`01-provider-fidelity-unknown-fields.md`](../../../.scratch/astra-context-capabilities/issues/01-provider-fidelity-unknown-fields.md) — Provider Fidelity: Unknown-Field Preservation (AFK, unblocked)
2. [`02-provider-fidelity-fixture-tests.md`](../../../.scratch/astra-context-capabilities/issues/02-provider-fidelity-fixture-tests.md) — Provider Fidelity: Protocol Fixture Test Matrix (AFK, blocked by #01)
3. [`03-privacy-policy-workspace-enforcement.md`](../../../.scratch/astra-context-capabilities/issues/03-privacy-policy-workspace-enforcement.md) — Privacy Policy Engine: Workspace Policy Schema & Enforcement (AFK, unblocked)
4. [`04-privacy-policy-detectors-audit.md`](../../../.scratch/astra-context-capabilities/issues/04-privacy-policy-detectors-audit.md) — Privacy Policy Engine: Custom Detectors, Preview & Audit (AFK, blocked by #03)
5. [`05-store-fts5-symbol-index.md`](../../../.scratch/astra-context-capabilities/issues/05-store-fts5-symbol-index.md) — Store: FTS5 Symbol Index Migration (AFK, unblocked)
6. [`06-context-pack-ranked-retrieval.md`](../../../.scratch/astra-context-capabilities/issues/06-context-pack-ranked-retrieval.md) — Context Pack Assembler: Ranked Retrieval with Budget (AFK, blocked by #05)
7. [`07-context-pack-incremental-freshness.md`](../../../.scratch/astra-context-capabilities/issues/07-context-pack-incremental-freshness.md) — Context Pack Assembler: Incremental Indexing & Freshness (AFK, blocked by #06)
8. [`08-context-pack-ts-js-import-graph.md`](../../../.scratch/astra-context-capabilities/issues/08-context-pack-ts-js-import-graph.md) — Context Pack Assembler: TypeScript/JS Import Graph (AFK, blocked by #06)
9. [`09-context-pack-holdout-evaluation.md`](../../../.scratch/astra-context-capabilities/issues/09-context-pack-holdout-evaluation.md) — Context Pack Assembler: Holdout Quality Evaluation (AFK, blocked by #06)
10. [`10-artifact-store-immutable-persistence.md`](../../../.scratch/astra-context-capabilities/issues/10-artifact-store-immutable-persistence.md) — Artifact Store: Immutable Persistence with Collision-Resistant Identity (AFK, blocked by #03)
11. [`11-reversible-compaction-retention.md`](../../../.scratch/astra-context-capabilities/issues/11-reversible-compaction-retention.md) — Reversible Compaction: Full-Original Retention & Artifact IDs (AFK, blocked by #10)
12. [`12-reversible-compaction-range-sql.md`](../../../.scratch/astra-context-capabilities/issues/12-reversible-compaction-range-sql.md) — Reversible Compaction: Range Expansion & SQL Queries (AFK, blocked by #11)
13. [`13-session-manifest-save-export.md`](../../../.scratch/astra-context-capabilities/issues/13-session-manifest-save-export.md) — Session Manifest: Save & Export (AFK, blocked by #10)
14. [`14-session-manifest-import-security.md`](../../../.scratch/astra-context-capabilities/issues/14-session-manifest-import-security.md) — Session Manifest: Import, Freshness Detection & Security (AFK, blocked by #13)
15. [`15-provider-adapters-expanded.md`](../../../.scratch/astra-context-capabilities/issues/15-provider-adapters-expanded.md) — Provider Adapters: OpenAI Responses, Gemini-Native & Local Endpoints (AFK, blocked by #01, #03)
16. [`16-doctor-inspect-measured-metrics.md`](../../../.scratch/astra-context-capabilities/issues/16-doctor-inspect-measured-metrics.md) — Doctor/Inspect & Measured Token Metrics (AFK, blocked by #15)
