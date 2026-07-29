# ADR-0062: Execution Pipeline Acceleration

**Status**: Accepted
**Date**: 2026-07-29
**Deciders**: JP

## Context

Three execution pipeline bottlenecks were identified through benchmark analysis (runs 5-8):

1. **Codegen token waste**: Monolithic full-file generation for existing files regenerates unchanged code, consuming 30-50× more tokens than necessary.
2. **Sidecar underutilization**: Default batch size (512) leaves GPU idle during prompt processing; speculative decoding stripped from router; parallelism hardcoded to 1 slot.
3. **Probe over-exploration**: All probes enter the Thought Chain loop (10-15 inference calls) even for tasks where pre-computed context + single-shot synthesis would suffice.

## Decision

### Stage 1: Edit Loop (replaces BuildDiffDAG)

Introduced `RunEditLoop` — an iterative plan-then-hunk codegen strategy:

- **Plan step**: Unconstrained prose output listing discrete changes.
- **Hunk loop**: GBNF-constrained JSON per step (`{searchContent, replaceContent, done}`), applied via existing `applyOneHunk()`.
- **Budget guard**: 15 steps max, `done: true` exits early.
- **Threshold**: `autoModeDiffThreshold` lowered from 200 to 20 lines.
- **Execution**: Runs inline (no DAG, no node states). Returns patched content directly.

### Stage 2: Sidecar Acceleration

- **Batch size**: Worker args `-b 2048 -ub 512` (4× prompt processing throughput).
- **Speculative decoding**: Router re-enabled with `--spec-type ngram-simple --spec-draft-n-max 16` (~20-30% faster for structured output).
- **Memory-gated parallelism**: `--parallel` set to 2 slots on systems with ≥24GB RAM, 1 otherwise. Detection via `getSystemMemoryGB()` (sysctl on macOS, /proc/meminfo on Linux).

### Stage 3: Substrate-Aware Probes

- **classifyProbeGoal**: Keyword-based auto-detection of probe substrate mode (overview/focused/aggregate/unknown).
- **Directory Manifest**: `BuildDirectoryManifest()` — recursive tree-sitter symbol extraction + doc previews with budget control.
- **MapReduceSynthesis**: Content → chunks → N map calls + 1 reduce call. Single-chunk passthrough when content fits DS cap.
- **DirectSynthesis promotion fix**: Removed `len(fullContent) > maxChars &&` gate — now always promotes when content ≤200K chars.
- **ProbeConfig.SubstrateMode**: New field for planner-specified or auto-detected routing.

## Consequences

### Positive
- Codegen: ~30-50× token reduction for files ≥20 lines (Edit Loop vs full regeneration).
- Sidecar: 4× prompt processing throughput, ~20-30% faster structured generation.
- Probes: Overview/aggregate tasks resolve in 1 or N+1 inference calls instead of 10-15.
- DirectSynthesis always promoted when content fits, eliminating unnecessary Thought Chain fallback.

### Risks
- Edit Loop budget (15 steps) may be insufficient for very large files — monitor and adjust.
- Ngram-simple speculative decoding increases router memory; watch for OOM on 8GB machines.
- Keyword-based goal classification is not exhaustive — unknown goals correctly fall through to Thought Chain.

### Files Changed
- `internal/codegen/edit_loop.go` — NEW
- `internal/codegen/edit_loop_test.go` — NEW
- `internal/symbols/manifest.go` — NEW
- `internal/symbols/manifest_test.go` — NEW
- `internal/executor/probe_mapreduce.go` — NEW
- `internal/executor/probe_mapreduce_test.go` — NEW
- `internal/executor/probe_classify_test.go` — NEW
- `internal/executor/probe.go` — classifyProbeGoal + DS promotion fix
- `internal/inference/local_model.go` — batch size, spec decoding, memory-gated parallel
- `internal/inference/hardware_config_test.go` — memory + parallel tests
- `internal/compiler/compiler.go` — SubstrateMode field
- `cmd/tzro-mcp/tools.go` — Edit Loop routing, threshold 200→20
