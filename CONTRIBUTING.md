# Contributing to tzro

Thank you for your interest in contributing to `tzro`! We welcome contributions from developers, researchers, and agent practitioners of all backgrounds.

`tzro` is designed as a zero-dependency, local-first context engine and token shield. Whether you want to add support for a new programming language, build a specialized log compactor, expand agent client integrations, or refine documentation, this guide will help you get up and running quickly.

---

## 1. Project Overview

`tzro` is implemented in modern Go with targeted native C/C++ acceleration where microsecond latency is critical. The codebase maintains high testing standards:
* **~27,000 SLOC Go** across core packages and CLI commands.
* **~22,000 SLOC Go tests**, maintaining an approximate **1:1 code-to-test ratio**.
* Zero external cloud dependencies required for core CLI operations, AST parsing, and local SQLite caching.

### Package Map

| Package / Directory | Purpose |
| :--- | :--- |
| `cmd/tzro` | Primary CLI entry point and subcommands (`probe`, `context`, `impact`, `skeleton`, `expand`, `compact`, `session`, `ingest`, `query`, `start`, `doctor`, `bench`). |
| `cmd/jev-score` | Standalone C++17 JEV scoring worker linking against `libllama` / `llama.cpp` for native local decision ranking. |
| `pkg/ast` | Tree-sitter Concrete Syntax Tree (CST) parsing, signature extraction, and cryptographic function body elision. |
| `pkg/benchmark` | Comparative benchmarking harness, Signal Density Metric (SDM) evaluator, and token cost profiling. |
| `pkg/compactor` | Evidence-preserving log, test output, and structural data compaction with exit-code guarantees. |
| `pkg/context` | Task-aware context pack assembler, relevance ranking, token budget capping, and change-impact analysis. |
| `pkg/decision` | Adaptive decision engine, client adapters, and native C++ JEV scoring integration. |
| `pkg/dlp` | Zero-cloud Data Loss Prevention engine (`.tzro/privacy.json`) for on-device secret redaction. |
| `pkg/doctor` | Health check diagnostics for loopback proxying, provider routes, SQLite FTS5, and client hooks. |
| `pkg/evidence` | Typed evidence provenance envelopes tagging context with source kind, anchor coordinates, and staleness markers. |
| `pkg/executor` | Subprocess execution runners with exit-code tracking, buffered streams, and compaction pipelines. |
| `pkg/extractor` | Agent transcript parsers, tool invocation decoders, and multi-turn interaction analyzers. |
| `pkg/hooks` | Native client configuration and prompt hook setup for Claude Code, Gemini Code Assist, Cursor, Codex, OpenCode, and Aider. |
| `pkg/inspector` | Offline explainability engine tracing ranking decisions and omitted stages (`tzro inspect explain`). |
| `pkg/kvlock` | KV-cache prefix lock guard and prompt normalizer maximizing cloud cache read hit rates. |
| `pkg/probe` | Sub-millisecond ripgrep + Tree-sitter symbol, syntax, and file discovery engine. |
| `pkg/proxy` | Transparent HTTP/HTTPS loopback reverse proxy intercepting LLM egress requests. |
| `pkg/search` | Unified FTS5 multi-source evidence search across code, design specs, ADRs, documentation, artifacts, and logs. |
| `pkg/session` | Git-aware agent session manifests (`tzro session save/load/status`) with freshness validation. |
| `pkg/store` | Embedded SQLite WAL mode + FTS5 database management, schema migrations, and content-hash blob storage. |
| `pkg/tokenizer` | Local token counting and estimation for strict context budget enforcement. |

---

## 2. Development Setup

### Prerequisites
* **Go**: Version `1.26` or later.
* **CGO**: `CGO_ENABLED=1` is required for Tree-sitter bindings and SQLite FTS5.
* **C/C++ Compiler**: `clang` (macOS/Linux) or `gcc` (Linux).
* *(Optional)* **llama.cpp**: If building the native C++ decision worker (`cmd/jev-score`).

### Building tzro
Clone your fork and compile the binary:
```bash
git clone https://github.com/<your-username>/tzro.git
cd tzro

# Build the main tzro executable into bin/
go build -o bin/tzro ./cmd/tzro

# Verify the build
./bin/tzro doctor
```

*(Optional)* To compile the native C++ scoring worker:
```bash
./scripts/build_jev_score.sh
```

### Running Tests

#### Unit Tests
Run the standard test suite:
```bash
go test ./...
```

#### Race Detection (CI Standard)
Run tests with the Go race detector enabled:
```bash
go test -race -count=1 ./...
```

#### Performance Checks
Run uninstrumented latency checks to verify performance thresholds:
```bash
go test -count=1 ./cmd/tzro -run '^TestLatency_'
```

#### Integration & E2E Benchmarks
Run end-to-end and integration benchmarks:
```bash
# E2E mini-benchmarks
go test -tags e2e ./tests/e2e/...

# KV-cache live proxy benchmark (requires OPENROUTER_API_KEY in environment)
KVCACHE_BENCH_MAX_COST=5.00 go test -tags integration -run TestKVCacheE2E -v ./pkg/hooks/
```

---

## 3. Code Style & Testing Standards

* **Formatting**: All code must be formatted using standard Go tooling:
  ```bash
  go fmt ./...
  go vet ./...
  ```
* **Table-Driven Tests**: Write test cases as slices of structs with descriptive subtests using `t.Run(tc.name, func(t *testing.T) { ... })`.
* **Hermetic Testing**: Unit tests must not require internet access. Use in-memory SQLite instances (`:memory:`) or `t.TempDir()` for filesystem fixtures.
* **Test Coverage**: Maintain the project's ~1:1 code-to-test ratio. Any new feature, CLI command, or bug fix must include corresponding unit tests in sibling `*_test.go` files.
* **Keep Allocations Minimal**: Critical CLI paths (`probe`, `context`, `compact`, `skeleton`) are optimized for sub-millisecond execution and minimal heap allocations. Benchmark performance-sensitive code where appropriate (`go test -bench=.`).

---

## 4. How to Contribute

1. **Find or Open an Issue**:
   - Check open issues on GitHub or inspect the local `.scratch/` issue tracker (see [Issue Tracker Conventions](docs/agents/issue-tracker.md)).
   - For major changes or architectural additions, open a discussion issue first to agree on the design.
2. **Branch**:
   - Create a branch off `main` (or the active release branch, e.g., `feature/my-feature` or `fix/relevance-scoring`).
3. **Develop & Test**:
   - Implement your changes following the architectural conventions in [ARCHITECTURE.md](ARCHITECTURE.md) and [CONTEXT.md](CONTEXT.md).
   - Ensure all tests pass with `-race`: `go test -race -count=1 ./...`.
4. **Submit a Pull Request**:
   - Include a concise PR description explaining the problem, the solution, and verification steps.
   - Reference any related issues or tickets.

---

## 5. Good First Issues & High-Impact Areas

If you are looking for places to start contributing, consider these high-impact areas:

* **Tree-Sitter Language Grammars (`pkg/ast`)**:
  - Add grammar parsers and body elision rules for languages not yet supported (e.g., Elixir, Zig, Scala, Kotlin, Swift).
* **Compactor Format Families (`pkg/compactor`)**:
  - Add parsers and compactors for test runners and build systems (e.g., Maven, Gradle, Rust cargo test, pytest, Vitest).
* **Agent Hooks & IDE Adapters (`pkg/hooks`)**:
  - Add or refine native client configurations and instructions for emerging coding assistants and terminal agents.
* **Documentation & Proactive QA Specs**:
  - Add intent-based use case specifications under `tests/llm/proactive-qa/`.
  - Clarify CLI documentation and developer guides.

---

## 6. Architecture Pointers

Before diving into complex changes, review our foundational architectural documentation:

* [ARCHITECTURE.md](ARCHITECTURE.md) — System topology, ingress proxy, AST pruning, and active discovery plane.
* [CONTEXT.md](CONTEXT.md) — The ubiquitous language, glossary, and core domain definitions.
* [docs/adr/](docs/adr/) — Architectural Decision Records explaining key design trade-offs.
* [docs/wiki/](docs/wiki/) — Persistent local wiki covering internal workflows, benchmark results, and system evolution.
