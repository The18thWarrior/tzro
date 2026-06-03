# Feature: Durable DAG Benchmarking Suite

## Problem & Solution

- **Context**: tzro is a local-first agentic execution engine that runs compiled DAGs rather than standard sequential chat loops. Conventional tool-calling benchmarks evaluate single or multi-turn conversational agents, which do not align perfectly with tzro's architecture.
- **Value**: Establishes an offline-ready, dynamic benchmarking suite to measure tzro's Planning Accuracy (generating optimal dependency DAGs) and Local GBNF Parameter Accuracy (mapping node parameters under strict grammar constraints).

## Technical Design Summary

- **Core Modules**:
  - `internal/benchmark/`: Parsers for BFCL and ComplexFuncBench samples, dynamic mock tool registry adapter, and execution simulation engine.
  - `internal/cli/`: Cobras-based subcommand `tzro benchmark` exposing run metrics directly to terminal developers.
- **Data Models / APIs**:
  - Offline representative samples embedded inside `internal/benchmark/testdata/`.
  - Dynamic schemas mock-registered dynamically via `tools.Register`.

## References

- **PRD**: [.scratch/benchmarking-suite/PRD.md](../../.scratch/benchmarking-suite/PRD.md)
- **Log Entry**: [Log Link](../log.md#2026-05-24t222500z-analysis--prd-durable-dag-benchmarking-suite)
