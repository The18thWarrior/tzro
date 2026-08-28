# ADR-0093: Benchmark Harness Hardening and Architecture Invariant Enforcement

## Status

Accepted

## Date

2026-08-26

## Context

The benchmark harness (`internal/comparison/`) had three reliability issues:

1. **Silent 0.00 quality scores**: When the LLM judge API failed (HTTP 500, rate limit, timeout), the `QualityScore` field defaulted to its zero value (0.00). This was indistinguishable from genuinely low scores, corrupting benchmark analysis.

2. **Task-specific pre-compilation hacks**: Three bespoke `if t.ID == "..."` blocks in `conditions.go` pre-compiled ADR files, extracted package architectures, and assembled project maps before specific tasks executed. These hacks bypassed the general execution pipeline and masked whether the engine could handle these tasks unassisted.

3. **No architectural regression gate**: Nothing prevented future contributors from re-introducing bespoke task-specific hacks or probe strategy registrations.

## Decision

### 1. Judge Retry with Exponential Backoff

Add `JudgeOutputDetailedWithRetry` wrapping `JudgeOutputDetailed` with 3 retries and exponential backoff (2s, 4s, 8s). On terminal failure:
- Set `ComparisonResult.JudgeError = "ERR_JUDGE_UNAVAILABLE"`
- Set `ComparisonResult.QualityScore = -1` (sentinel, distinct from 0.00)

### 2. ERR State in Reports

- Report rendering shows `ERR` for any result with `QualityScore < 0` or `JudgeError != ""`
- `avgQualityScore` excludes results with `JudgeError != ""` or `QualityScore < 0`
- `ERR` results are visually distinct in the markdown table

### 3. Rejudge CLI Subcommand

Add `tzro compare rejudge --input <file> --output <dir>` to re-run the judge on failed entries only. Valid entries are preserved verbatim.

### 4. Purge Bespoke Task Hacks

Delete the 3 task-specific pre-compilation blocks from `conditions.go`:
- `if t.ID == "adr_summary"` (ADR file concatenation)
- `if t.ID == "internal_architecture"` (call graph extraction)
- `if t.ID == "comprehensive_readme"` (project map assembly)

All tasks now execute through the unassisted general runtime pipeline.

### 5. Architecture Invariant Linter

Add `TestArchitectureInvariants` in `internal/executor/` using `go/ast` to enforce:
- No bespoke task-ID string comparisons in `conditions.go`
- No probe strategy registration in `wireStrategies`

This test runs in CI and prevents regression.

## Consequences

### Positive
- Judge API transient failures no longer corrupt benchmark scores
- `ERR` state is explicitly visible, not hidden as 0.00
- Failed entries can be re-judged without re-running the full suite
- Benchmark results reflect true unassisted engine capability
- Architecture invariants are enforced at compile/test time

### Negative
- Tasks that previously relied on pre-compiled data may score lower initially
- Rejudge command adds ~120 LOC of CLI/library code

### Phase 3 Discovery: Deep Interlinking

Investigation during the dead code deletion phase revealed that probe utility functions (`truncate`, `extractCacheIdFromText`, `detectPreloadPaths`, `isAnalyzeConfig`, `parseActionFromResponse`, `hybridSynthesisThreshold`) are deeply referenced by active strategies (List, Recall, Analyze). Similarly, semantic_validator and scatter_assembly are referenced in edge_thought.go, envelope.go, and recall_strategy.go.

Clean deletion requires a dedicated refactoring session to:
1. Extract shared utility functions into standalone files
2. Decouple edge traversal logic from strategy types
3. Restructure envelope output extraction

This is tracked separately and not blocked by this ADR.

## References

- Wayfinder MAP: `.scratch/framework-pivots-and-simplification/MAP.md`
- Ticket 01 (Benchmark Harness): `.scratch/framework-pivots-and-simplification/issues/01-benchmark-harness-integrity.md`
- Ticket 04 (Cognitive Boundary): `.scratch/framework-pivots-and-simplification/issues/04-cognitive-boundary-invariants.md`
