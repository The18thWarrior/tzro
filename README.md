# TZRO: The Local Context Engine

<p align="center">
  <img src="website/logo.png" alt="TZRO Logo" width="120" />
</p>

<p align="center">
  <strong>Find code, assemble context, and compact tool output locally.</strong>
</p>

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License: Apache 2.0" /></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.26+-00ADD8.svg" alt="Go Version" /></a>
  <a href="#benchmark"><img src="https://img.shields.io/badge/Memory%20Footprint-%3C50MB%20RAM-success.svg" alt="Memory" /></a>
  <a href="#benchmark"><img src="https://img.shields.io/badge/Code%20Read%20Reduction-70%25--90%25-purple.svg" alt="Code read reduction" /></a>
  <a href="https://github.com/The18thWarrior/tzro/actions/workflows/test.yml"><img src="https://github.com/The18thWarrior/tzro/actions/workflows/test.yml/badge.svg" alt="Tests" /></a>
</p>

Tzro is a native Go CLI for developers and coding agents. It returns relevant code, compact source files, and build diagnostics that fit a context budget.

The CLI Toolkit runs without a proxy, API key, Python, PyTorch, or GPU. The optional Proxy Shield adds request normalization and secret masking for cloud API traffic.

[Quickstart](#quickstart) · [CLI Toolkit](#cli-toolkit) · [Agent integrations](#agent-integrations) · [Proxy Shield](#proxy-shield) · [Benchmarks](#benchmark) · [Feature maturity](#feature-maturity)

## Quickstart

### 1. Install

Install the native binary and automatically configure detected coding agents:

```bash
curl -sSL https://get.tzro.ai | sh
```

*(Optional: To install only the CLI without agent configuration, run `curl -sSL https://get.tzro.ai | sh -s -- --cli-only`).*

Alternatively, build from source (requires Go 1.26+):
```bash
git clone https://github.com/The18thWarrior/tzro.git
cd tzro
CGO_ENABLED=0 go build -o ./bin/tzro ./cmd/tzro
TZRO_SOURCE_BIN=./bin/tzro sh ./install.sh
export PATH="$HOME/.tzro/bin:$PATH"
```

The installer automatically configures supported detected agents. Some clients require a restart or native approval. See [installation details](docs/installation.md).

The CLI uses pure-Go SQLite and Tree-sitter packages. It does not require a C compiler or system SQLite headers.

### 2. Try it

From a repository directory, search for a symbol or phrase:

```bash
tzro probe "auth middleware"
```

The result identifies matching files and source locations. If your repository has no authentication code, use a symbol from your project.

For this repository, try:

```bash
tzro probe "NormalizeOpenAI"
```

### 3. Add it to your workflow

From the tzro repository, run these examples:

```bash
# Read signatures and imports, with function bodies stored for later expansion
tzro skeleton ./pkg/kvlock/kvlock.go

# Compact test output and retain the command exit status
tzro compact --run "go test ./pkg/kvlock"

# Assemble relevant code within a token budget
tzro context "normalize prompt prefixes" --budget 2000
```

For another repository, replace the file, test command, and task description with your own.

## How it fits

```text
                      Developer / coding agent
                         /                \
                        v                  v
          +-------------------------+  +-------------------------+
          | CLI Toolkit             |  | Proxy Shield (optional) |
          | probe / context / impact|  | KV-cache prefix lock    |
          | skeleton / compact      |  | DLP / secret masking    |
          | expand / search         |  | tzro start              |
          +------------+------------+  +------------+------------+
                       |                            |
                       v                            v
          Local files / Content-Hash Store    Cloud API provider
          No API keys or interception         Opt-in API routing
          70-90% smaller code reads [1]       +4.1 cache points [2]
```

[1] Code-read reduction depends on the source file. [2] The earlier MiniMax run shows a 4.1-percentage-point difference in warm-cache hit ratio across different tool sequences. [Measurement details](#benchmark).

## CLI Toolkit

### Local discovery and context

`tzro probe` finds source locations and symbol declarations in your workspace. `tzro context` assembles relevant evidence within a token budget. `tzro impact` identifies affected callers, consumers, and tests.

These commands run locally and do not send your code to an LLM provider. Your agent controls which results enter its context.

### Tree-Sitter AST Skeletonizer

The AST Skeletonizer preserves imports, types, signatures, and docstrings. It replaces function bodies with content hashes, such as `// [body elided: #a8f19c]`.

It supports Go, TypeScript, JavaScript, Python, Rust, Java, C/C++, Ruby, PHP, and C#. Code-read token reductions vary with the source file, with reported reductions of 70–90%.

To retrieve a body, run `tzro expand` with its hash:

```bash
tzro expand <hash>
```

### Smart JSON Crusher and Stack Trace Elider

The Smart JSON Crusher converts uniform JSON arrays into compact Markdown tables. The Stack Trace Elider removes runtime frames while retaining application diagnostics.

`tzro compact --run` captures the command exit status. Its Compaction Evidence Contract retains supported failure diagnostics and links overflow output to stored artifacts.

For tabular analysis, `tzro ingest` imports CSV, TSV, or JSON data into SQLite. `tzro query` returns only the rows your SQL query selects.

### Content-Hash Store

The Content-Hash Store keeps code bodies, symbol indices, session manifests, and artifacts on your machine. SQLite provides full-text search and workspace-scoped storage.

`tzro expand` retrieves stored content. `tzro session` saves and restores task state, with checks for stale evidence.

### CLI reference

```bash
# Fast local codebase exploration (0 cloud tokens)
tzro probe "auth middleware jwt"

# Task Context Assembly: assemble ranked, token-budgeted context packs
tzro context "implement rate limiting" --budget 2000

# Symbol-anchored context assembly
tzro context --symbol ValidateToken --file pkg/auth/jwt.go

# Pre-edit blast radius: compute direct callers, consumers, and test coverage
tzro impact pkg/context/context.go
tzro impact                       # analyze uncommitted git changes
tzro impact --staged --format tree # ANSI tree rendering of staged changes

# Predictive test selection: run only affected tests
tzro test --impact --staged       # tests affected by staged changes
tzro test --impact --dry-run      # list affected tests without running

# Unified local evidence search across code, docs, ADRs, logs, and artifacts
tzro search "token bucket algorithm"

# Generate AST skeleton for a source file (eliding bodies to hashes)
tzro skeleton ./pkg/kvlock/kvlock.go

# Retrieve original full code body or stored artifact (with optional line ranges)
tzro expand aa179288
tzro expand art_9503e3ba620ad4bf --lines 1-50

# Execute command with evidence contract compaction (10-line inline cap)
tzro compact --run "go test ./..."
go test ./... 2>&1 | tzro compact

# Agent session continuity: save, load, and inspect handoffs
tzro session save --objective "add ratelimit" --constraints "no third-party deps"
tzro session status
tzro session load session_manifest.json

# Session pause/resume with drift detection
tzro pause "finishing rate limit implementation"
tzro resume                       # resume most recent session
tzro resume <id> --format json    # resume specific session with JSON output

# Shell integration: capture terminal commands for agent context
tzro shell init zsh               # install shell hooks
tzro shell status                 # check capture status
tzro shell clear                  # purge command history

# Git hook management: advisory pre-commit impact analysis
tzro hook install pre-commit      # install advisory hook
tzro hook status                  # check hook health
tzro hook uninstall pre-commit    # remove and restore backup

# Offline context assembly explainability (0 cloud tokens)
tzro inspect explain <trace_id> <file_path>

# Run synthetic health checks, provider route diagnostics, and hook probes
tzro doctor

# Benchmark signal density per token with spending limit guardrails
tzro bench signal-density --max-cost 1.50

# Import tabular data and query with SQL
tzro ingest data.csv --name my_table
cat report.json | tzro ingest -
tzro query my_table "SELECT col, COUNT(*) FROM my_table GROUP BY col"
```

---

## Agent integrations

Standard installation configures detected Antigravity, Claude Code, Hermes, GitHub Copilot CLI, Pi-Coder, and Codex clients. It installs skills, supported hooks, and native MCP configuration. These integrations work without the proxy or experimental decision runtime.

To configure an agent installed after tzro, or repair its configuration:

```bash
tzro init --hooks auto
# Select a client explicitly
tzro init --hooks codex
# Configure every supported client
tzro init --hooks all
```

Use `--workspace` for client configurations that support project scope. Copilot CLI MCP requires user scope. Hermes requires the selected profile home; see the [capability matrix](docs/installation.md#agent-capabilities).

Setup preserves unrelated configuration and reports conflicts or failures. Codex and Hermes can require native hook approval. Written configuration does not prove that a running client loaded it.

Hook capabilities differ by client. Claude Code, Copilot CLI, and Pi can replace tool output. Other clients use the skill and MCP tools for explicit compaction. Pi MCP is not automatically registered. See the [capability matrix and validation limits](docs/installation.md#agent-capabilities).

## Proxy Shield

The Proxy Shield is optional. It suits workflows that need stable prompt prefixes or secret masking before requests reach a cloud provider.

### Enable the proxy

In a separate terminal, run:

```bash
tzro start --port 7878
```

The proxy runs in the foreground at `http://127.0.0.1:7878`.

In the terminal that starts your agent, set the base URL for its provider:

```bash
# Anthropic-compatible clients
export ANTHROPIC_BASE_URL=http://127.0.0.1:7878

# OpenAI-compatible clients
export OPENAI_BASE_URL=http://127.0.0.1:7878/v1
```

For clients with a base-URL configuration field, use the same address there. Keep your existing provider credentials in the client.

To inspect proxy metrics or route health, run:

```bash
tzro status
tzro doctor
```

### KV-Cache Prefix Lock Guard

The KV-Cache Prefix Lock Guard normalizes prompts and tool definitions into a stable order. Stable prefixes let providers reuse eligible cached input.

Earlier benchmark reports cite 70–85% cache hit rates in agent workflows and up to 99% under controlled conditions. These rates include native provider caching. The [proxy E2E tests](pkg/proxy/proxy_e2e_test.go) and [KV-cache benchmarks](pkg/hooks/kvcache_e2e_bench_test.go) compare direct and proxied calls.

The saved MiniMax run records warm-cache hit ratios of 85.80% direct and 89.88% proxied, a difference of 4.08 percentage points. The runs used different tool sequences, so this does not isolate the proxy effect. [Saved results](pkg/hooks/testdata/kvcache_e2e_benchmark_results.json).

Cache pricing varies by provider, model, and cache duration. At example multipliers of 1.25× for writes and 0.10× for reads, a write costs 12.5× a read. This ratio is an illustration, not a universal provider price. Local compaction and discovery provide value independently of cache pricing.

### Zero-Cloud Data Loss Prevention (DLP)

The on-device scanner uses patterns and entropy checks to detect credentials and private IPs. It masks detected secrets before proxy egress and restores mapped values in responses.

The Workspace Privacy Policy also applies to context assembly and artifact storage. Policy configuration lives in `.tzro/privacy.json`.

### Security model

- **Network scope**: `tzro start` binds to `127.0.0.1`, on port 7878 by default.
- **Provider traffic**: The proxy forwards requests and authentication headers to the configured provider. Only clients routed through the proxy receive its protection.
- **Local storage**: The Content-Hash Store, session manifests, and artifacts remain on your machine. Requests still send their permitted content to the provider.
- **No external telemetry**: Tzro keeps its usage metrics local.
- **DLP before egress**: The proxy masks detected secrets before it sends a request. Detection coverage depends on the patterns and workspace policy.
- **Auditable source**: Proxy, DLP, and storage code are available in this repository.

The [security policy](SECURITY.md) documents the threat model, credential handling, and DLP configuration.

---

<a id="benchmark"></a>

## Performance and benchmark evidence

### Developer Workflows (Installation Profiles)

`tzro bench workflows` evaluates real coding tasks using native client agent loops across three installation profiles: **Baseline**, **Tzro Standard** (default installer with CLI, skills, and hooks), and **Tzro Full** (Standard + loopback Proxy Shield, JEV decision engine, and GLiNER span extractor).

The [Baseline/Standard validation](docs/benchmarks/workflows-20260928-validation-v5.md) passed all 42 task checks across three repetitions.
Standard saved 18.3% of total tokens and 17.4% of estimated cost overall, with 185 model requests versus Baseline's 190.
Per-repetition token savings were 31.8%, 6.8%, and 3.1%. None reaches the current 33% gate; release remains blocked.
No task invoked graph execution. Product-source checks, native traces, and token accounting passed verification.

The later [v7 diagnostic](docs/benchmarks/workflows-20260929-diagnostic-v7.md) passed all 14 checks but used 27.2% more Standard tokens.
One task naturally batched six SQL queries into a successful local graph and saved 43.2% tokens.
That task-level gain does not satisfy the complete-battery release gate.

The [diagnosis](docs/wiki/bugs/workflow-benchmark-savings-20260928.md) explains why older prepared-context results are not comparable.
Earlier validation also exposed a billing-fixture deadlock missed by helper-only checks. The corrected run includes invoice-level grading.
Paid validation and Make targets now default to Baseline versus Standard; Full remains an explicit option.

The current release gate requires at least 33% lower Standard total tokens in each of three complete repetitions, lower estimated cost, and no paired quality regression.
Task quality, tool choices, recovery turns, and cached tokens all count.
Use the [installation-profile recipe](docs/benchmark-workflows.md) to run and verify the evidence.
Render an existing report with `make benchmark-publish REPORT_JSON=path/to/run.json`.

### Supporting Component Measurements

| Measurement | Scope | Evidence |
| --- | --- | --- |
| 70–90% code-read token reduction | Reported AST skeletonization range. Varies by file. | [Skeletonizer tests](pkg/ast/skeleton_test.go) |
| <50 MB RAM | Reported baseline for core CLI/proxy tools. Excludes optional model runtimes. | [Core design targets](SOLUTION_APPROACH.md) |
| 70–99% cache hit rate | Earlier reported workflow and controlled-run range. Includes native caching. | [Proxy tests](pkg/proxy/proxy_e2e_test.go), [KV-cache tests](pkg/hooks/kvcache_e2e_bench_test.go) |
| 85.80% → 89.88% warm-cache hit ratio | Direct → proxied MiniMax M3, September 23, 2026 | [Saved run](pkg/hooks/testdata/kvcache_e2e_benchmark_results.json) |

The [installation-profile recipe](docs/benchmark-workflows.md) documents native Pi integration, runtime preparation, timing, and result fields. The [E2E instructions](#e2e-integration-tests) describe how to repeat provider comparisons. Provider calls incur costs.


---

## v3.1.0 highlights

- **Multi-Language Context Packs**: `tzro context` now resolves references across Go, TypeScript/JavaScript, Python, and Rust using Tree-sitter AST adapters. TypeScript barrel re-exports, Python relative imports, and Rust `crate::`/`super::` paths are fully traced.
- **Symbol-Anchored Context** (`tzro context --symbol`): Assembles context around a symbol declaration. The `--file` flag selects its source file.
- **Predictive Test Selection** (`tzro test --impact`): Identifies tests affected by staged or unstaged changes across Go, Jest/Vitest, and pytest. It runs those tests with compacted output.
- **Session Pause/Resume** (`tzro pause` / `tzro resume`): Saves agent work with drift detection. The resumption dashboard shows git divergence, modified files, stale evidence, and shifted symbol lines.
- **Shell Integration** (`tzro shell init`): Captures development commands from `zsh` or `bash` into SQLite. Hooks apply credential redaction and an allowlist.
- **Git Hook Manager** (`tzro hook install pre-commit`): Shows staged impact analysis without blocking commits. It preserves existing hooks through backups and chaining.
- **Exact BPE Tokenizer**: Thread-safe `cl100k_base` and `o200k_base` token counting via `tiktoken-go` with UTF-8-safe truncation for precise budget enforcement.
- **Repository configuration** (`.tzro/context.yaml`): Repository defaults for token budgets, traversal depth, tokenizer selection, and language priorities.
- **Enhanced Impact Analysis**: `--staged`/`--unstaged`/`--all` scopes, `--format tree|json|markdown` output, comment-only hunk filtering, and ANSI tree rendering.
- **MCP Protocol v2025-06-18**: Added `tzro_get_context_pack` and `tzro_get_impact_report` tools with structured output schemas.

---

## Optional execution runtime

The CLI Toolkit also supplies context to the System 1 Graph Executor. The JEV-style Qwen 0.8B Decision Runtime and GLiNER Span Extractor are experimental components with separate model and runtime requirements.

They are not required for the quickstart. Their memory requirements are separate from the core CLI baseline. [Decision runtime migration](docs/adr/0096-migration-to-jev-style-and-libllama-decision-engine.md).

---

## ⚖️ Development & Testing

### Unit Tests

The [Tests workflow](.github/workflows/test.yml) runs the full suite with race detection on Ubuntu for each push and pull request. It uses the Go version in `go.mod` and requires no provider credentials.

CI checks latency and memory limits separately, without race instrumentation. Both checks must pass.

```bash
# Run all unit tests (no API keys or external services needed)
CGO_ENABLED=1 go test -race -count=1 ./...

# Check latency and memory limits without race instrumentation
CGO_ENABLED=1 go test -count=1 ./cmd/tzro -run '^TestLatency_'

# Format source files
go fmt ./...

# Build the CLI binary
./build.sh
```

### E2E Integration Tests

E2E integration tests run real agent loops against live LLM providers. They compare direct and proxied API calls with billing checks for each turn.

```bash
# Requires OPENROUTER_API_KEY in .env at repo root
# Runs real API calls — costs apply (tests include cost guards)

# Full proxy E2E: direct vs proxied agent loop with billing comparison
CGO_ENABLED=1 go test -tags=integration -v -run TestProxyE2E ./pkg/proxy/

# PiCoder extension hook harness (scaffolded multi-package workspace)
CGO_ENABLED=1 go test -tags=integration -v -run TestPiCoder ./pkg/hooks/

# KV-cache prefix stability benchmarks (per-turn cache hit ratio tracking)
CGO_ENABLED=1 go test -tags=integration -v -run TestKVCache ./pkg/hooks/

# Cross-language scenario tests (Go + TypeScript monorepo workspaces)
CGO_ENABLED=1 go test -tags=integration -v -run TestScenario ./pkg/hooks/
```

---

<a id="feature-maturity"></a>

## 📋 Feature Maturity

| Feature | Status | Test Coverage |
|---------|--------|---------------|
| KV-Cache Prefix Lock Guard | **Stable** | Unit + E2E with real providers |
| AST Skeletonizer | **Stable** | Unit tests, 10 languages |
| `tzro probe` (local discovery) | **Stable** | Unit + integration |
| `tzro context` (context packs) | **Stable** | Unit + E2E (Go, TS, Python, Rust) |
| `tzro impact` (change analysis) | **Stable** | Unit + E2E |
| `tzro compact` (log compaction) | **Stable** | Unit + E2E (PiCoder harness) |
| `tzro session` (continuity) | **Stable** | Unit |
| DLP / secret masking | **Stable** | Unit |
| `tzro ingest` / `tzro query` | **Stable** | Unit + E2E |
| System 1 Graph Executor | **Beta** | Unit |
| JEV-style Decision Runtime | **Experimental** | Integration |
| GLiNER Span Extractor | **Experimental** | Integration |
| MCP Server | **Beta** | Unit |

> **Project status**: Actively maintained. The project supports daily use across multiple coding agents. A contributor guide is planned.

---

## 📄 License

Licensed under the [Apache 2.0 License](LICENSE).
