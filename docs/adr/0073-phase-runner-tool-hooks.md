# ADR-0073: PhaseRunner Tool Hooks — ToolFixup and ToolPostProcess

**Status**: Accepted  
**Date**: 2026-08-08  
**Deciders**: JP  
**Context**: Port ADR-0058 deterministic mechanisms to PhaseRunner architecture

## Context

The PhaseRunner (ADR-0066) replaced the legacy `RunProbe()` flat Thought Chain loop with a structured 4-phase pipeline. The flat loop contained ~12 hardened remediation mechanisms that compensated for 4B model failure modes (empty arguments, duplicate calls, premature synthesis). The PhaseRunner shipped without these mechanisms, causing quality regressions on benchmarks (e.g., `cache_function_index` scoring 1/5 vs. 5/5 on the legacy path).

Three categories of mechanisms needed porting:

1. **Pre-dispatch argument repair** — fixing empty `read_file` paths, empty `web_search` queries, empty `cacheId` parameters, extracting SQL from reasoning text
2. **Post-dispatch state tracking** — URL extraction from web_search results, visited file/URL marking, evidence capture
3. **Structural enforcement** — rejecting premature synthesis when no tools have been called

## Decision

### Two symmetric hooks on PhaseRunner (not Phase)

```go
type PhaseRunner struct {
    ToolFixup       func(phaseName, toolName string, args map[string]interface{}, reasoning string) (string, map[string]interface{})
    ToolPostProcess func(phaseName, toolName string, args map[string]interface{}, output string, err error)
}
```

**Why PhaseRunner-level, not Phase-level**: Cross-phase state (discovered URLs, visited files, known cacheIds) spans phase boundaries. Placing hooks on `Phase` would force callers to manage shared state through awkward Phase-to-Phase plumbing. On `PhaseRunner`, the builder function creates one closure that captures all state for the runner's lifetime — matching how the legacy flat loop worked (one function, all state in scope).

**Why `ToolFixup` includes reasoning text**: The Analyze Node's SQL auto-extraction needs the Pass 1 reasoning text. The 4B model frequently writes SQL in its reasoning ("I should run `SELECT Sector, COUNT(*) FROM cache_1234567890`") but fails to emit it as a tool argument. Including `reasoning` in the signature makes this extraction a clean, single-site fixup.

### No-action retry in executePhase

When the model signals "synthesize" but has called 0 tools in a phase with `AllowedTools`, the synthesis is rejected with corrective text. Capped at 2 retries. This is a structural enforcement that prevents the 4B model from skipping exploration entirely.

### Mechanisms intentionally NOT ported

| Mechanism | Why not ported |
|---|---|
| **Futility detection** | Per-phase step budgets (2-8) + `OnExhaustion` strategies are the guard. The old probe had a flat 20-step budget requiring explicit futility detection; the PhaseRunner's small per-phase budgets make it redundant. |
| **Phase gate (Analyze)** | The `schema_orient` → `query_dev` transition requires `introspect_cache` to be called. The `query_dev` → `compute` transition requires `analyticalQueries >= 2`. Phase transitions structurally enforce the gate. |
| **Consecutive error tracking** | Per-phase budgets + `OnExhaustion` handle this. A phase that burns 3/3 steps on errors triggers `ExhaustionSkip` automatically. |

## Consequences

- **Positive**: The PhaseRunner core remains dumb — it dispatches tools and manages phase transitions. All node-type-specific intelligence lives in builder closures (`buildProbePhaseRunner`, `buildResearchPhaseRunner`, `buildAnalyzePhaseRunner`).
- **Positive**: New node types can wire their own fixup/post-process behavior without modifying the PhaseRunner core.
- **Negative**: The hook signatures are fixed. If a future mechanism needs a different shape (e.g., access to all prior tool calls), the signature must change and all builders must update.
- **Mitigated**: The `reasoning` parameter in `ToolFixup` was the only non-obvious addition to the signature. All other parameters are natural consequences of the dispatch lifecycle.
