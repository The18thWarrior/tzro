# Feature: Code Quality & Architectural Refactoring

## Problem & Solution

- **Context**: Core execution engine components—specifically `internal/benchmark/runner.go` and `internal/memory/memory.go`—have grown past healthy maintainability limits. They are doing too many orthogonal tasks, such as combining VFS mutations, database setups, HTTP interceptors, multi-hop traversals, and hardcoded edge cases in shared string routines.
- **Value**: Restores clean architectural boundaries, separates POSIX environment simulations and mock networks from testing logic, isolates SQL execution states from interactive memories, and deletes fragile hardcoded strings to ensure a highly maintainable, testable, and robust codebase.

## Technical Design Summary

- **Core Modules**:
  - `internal/benchmark/vfs`: Handles virtual POSIX POSIX-like in-memory folder simulations.
  - `internal/benchmark/mock`: Handles intercepting completion queries with a mock HTTP completions server.
  - `internal/benchmark/matcher`: Generic expected vs actual function parameters math and relaxation logic.
  - `internal/memory/models`: Structs for all SQLite tables, entities, and execution objects.
  - `internal/memory/graph_rag`: Semantic vector and multi-hop BFS neighborhood query paths.
  - `internal/memory/workflows`: Persistent long-running multi-task workflows state queries.
  - `internal/memory/memories`: Factual and specialized SOP skills memory persistence layers.
- **Data Models / APIs**:
  - Introduces `RelaxationPolicy` struct to dynamically configure allowed filenames, stopwords, date-time formats, and casing bounds, decoupling dataset exceptions from generic libraries.
  - Parallelizes sequential runner execution via Go's concurrent `errgroup` bounded worker pools.

## References

- **PRD**: [PRD.md](../../.scratch/code-quality-refactors/PRD.md)
- **Log Entry**: [Log Link](../log.md#2026-05-28-0810-ingest--prd-code-quality-refactoring)
