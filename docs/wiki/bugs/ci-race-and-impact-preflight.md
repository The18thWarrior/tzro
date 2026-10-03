# CI preflight: timing limits and missing test references

## Symptom

The first full race-enabled run for [CI pipeline](../../../.scratch/adoption-readiness/issues/03-ci-pipeline.md) failed in two existing tests.

- Compaction took about 292–305 ms against a 100 ms limit. The same test passed three times without race instrumentation.
- Symbol-only impact analysis omitted `greeter_test.go`. The failure repeated three times with race instrumentation and also occurred without it.

## Diagnosis

The compaction test applied an uninstrumented timing target to race-instrumented code. Go documents the associated runtime and memory overhead in its [race-detector guide](https://go.dev/doc/articles/race_detector#Runtime_Overhead).

`ImpactAnalyzer.AnalyzeSymbol` passed only the symbol name to reference discovery. The ripgrep adapter needs the declaration path to infer its paired test file. Discovery found the declaration, but that entry point did not use it.

## Resolution

The race-enabled suite still runs the latency workloads. Build-tag constants disable only their timing and memory assertions during that run. A separate CI step enforces all original limits without race instrumentation.

The symbol-only entry point now uses the existing symbol-resolution path. Multiple declarations receive the same ambiguity handling as `AnalyzeSymbolWithFile`.

An initial correction parsed every source file during declaration discovery. The existing 5,000-file test caught the added cost: impact analysis took about 5.96 seconds against its two-second limit. A source-text filter now skips AST parsing when the requested name is absent. Declaration names are extracted verbatim, so the filter preserves matches.

## Regression prevention

The existing `TestImpactAnalyzer_SymbolReferences` exercises the missing-reference behavior through the public API. The latency suite enforces the original performance limits. Both targeted suites passed after the corrections.

The [Tests workflow](../../../.github/workflows/test.yml) runs the full race-enabled suite and the separate performance checks. Its hosted result remains distinct from local validation.
