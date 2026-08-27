# PRD: Tzro v2 — The Local Token Shield & Context Optimization Engine

**Status**: `ready-for-agent`  
**Author**: Antigravity / DeepMind Pair Programmer  
**Date**: 2026-08-26  
**Target Milestone**: Tzro v2 Core Launch  

---

## 1. Problem Statement

Autonomous AI coding agents (Antigravity, Claude Code, Cursor, Aider, Cline) consume massive volumes of tokens during multi-turn developer interactions. In long-running agent workflows:
- **60% to 90% of all tokens consumed** belong to transient tool outputs: multi-megabyte directory listings, raw source files dumped for inspection, verbose build logs, repetitive stack traces, and JSON API payloads.
- **Context Rot & Degradation**: As contexts exceed 100k+ tokens, model reasoning degrades significantly (~2% instruction-following loss per 100k tokens), leading to hallucinated APIs and lost system constraints.
- **The KV-Cache Invalidation Penalty**: Major providers (Anthropic, OpenAI) offer a 90% discount on cached prompt prefixes ($P_{\text{read}} = 0.10 \times P_{\text{base}}$), but penalize cache misses by charging a 25% premium for cache writes ($P_{\text{write}} = 1.25 \times P_{\text{base}}$). A single unaligned byte or reordered tool schema invalidates the cache, making subsequent turns **12.5× more expensive**.
- **Heavy ML Sidecar Bloat**: Existing context compression tools (e.g. Headroom) rely on Python runtimes and PyTorch transformer models consuming 4.8 GB+ RAM with 60-second cold starts, rendering them unusable on resource-constrained developer hardware (8GB–16GB laptops, VDI instances, CI/CD runners).

Developers on resource-constrained machines urgently need a lightweight, single-binary context optimization plane that deterministically prevents rate limits, locks KV cache prefixes, and eliminates redundant discovery loops without requiring heavy GPUs or cloud planning overhead.

---

## 2. Solution

**Tzro v2 ("The Local Token Shield")** is an ultra-lean, compiled native Go binary (<50 MB RAM, zero Python/PyTorch dependencies) that operates across two synchronized planes:

1. **The Passive Plane (Transparent Proxy & Lifecycle Hooks)**:
   - Intercepts outgoing LLM calls (Anthropic, OpenAI, Gemini) and Antigravity tool lifecycle hooks (`hooks.json`).
   - Applies **KV-Cache Prefix Locking** to guarantee >90% cache hits.
   - Skeletons source code using native **Tree-sitter** grammars (70%–90% token reduction), replacing method bodies with cryptographic SHA-256 hash markers stored in a local SQLite FTS5 database.
   - Deterministically compacts build logs, test outputs, and JSON payloads.
   - Enforces **Zero-Cloud Data Loss Prevention (DLP)**, masking secrets and proprietary symbols before network egress.

2. **The Active Plane (`tzro probe` & `tzro expand`)**:
   - A deterministic, sub-millisecond local discovery engine that scans codebases with embedded ripgrep + Tree-sitter AST queries.
   - Turns 10-turn cloud discovery loops (250k tokens) into single on-device queries (500 tokens).
   - Allows models to expand specific method bodies on demand via `tzro expand <hash>` or injected tool calls.

---

## 3. User Stories

### A. Rate Limit & Cost Reduction
1. As a developer using AI coding agents on tight API rate limits, I want Tzro to skeletonize large source code files before sending them to the cloud LLM, so that I can inspect module interfaces without exhausting my daily token quota.
2. As a developer paying for Anthropic/OpenAI API usage, I want Tzro to lock and normalize prompt prefixes across turns, so that I receive the 90% prompt cache read discount and avoid the 12.5x cache miss penalty.
3. As a developer running test suites in agent sessions, I want Tzro to collapse 3,000-line test outputs down to only the failing assertions and root cause traces, so that transient test logs do not pollute the context window.
4. As a developer receiving large database or API query results in agent sessions, I want Tzro's Smart JSON Crusher to flatten repeated key schemas into compact tabular formats, reducing JSON token burn by up to 80%.

### B. Codebase Exploration (`tzro probe` & `tzro expand`)
5. As an AI agent or developer, I want to run `tzro probe "<query>"` from the CLI or via tool calls, so that I instantly locate symbols, enclosing functions, and line numbers in <5ms without multi-turn LLM reasoning.
6. As an AI agent that received an AST skeleton, I want to call `tzro expand <hash>` to fetch only the specific function body I need to edit, avoiding loading the entire file into context.
7. As a developer, I want `tzro probe` to return exact line ranges and syntax-aware symbol scopes rather than raw grep matches, so that the context returned is immediately actionable.

### C. Low-Resource Hardware & Native Operation
8. As a developer working on an 8GB or 16GB RAM laptop, I want Tzro to run as a single static binary consuming under 50 MB of RAM, so that my system remains responsive without background memory pressure.
9. As a developer, I want Tzro to start instantly with zero cold start delays and zero Python/PyTorch dependencies, so that it can run unobtrusively in the background.
10. As a developer in a CI/CD runner or containerized environment, I want Tzro to run without a GPU or external LLM sidecar, enabling deterministic token optimization in lightweight pipelines.

### D. Antigravity & Agent Tool Interception
11. As an Antigravity user, I want Tzro to provide a native `hooks.json` configuration, so that `PreToolUse` and `PostToolUse` lifecycle events automatically compress tool arguments and outputs without requiring network proxy configuration.
12. As a Cursor / Claude Code / Aider user, I want to export `ANTHROPIC_BASE_URL=http://localhost:7878` or `OPENAI_BASE_URL=http://localhost:7878/v1`, so that all outgoing agent traffic is transparently optimized with zero changes to my agent configuration.
13. As an interactive agent user, I want Server-Sent Events (SSE) streaming tokens to pass through the proxy with sub-millisecond latency, so that interactive typing speed remains fluid.

### E. Enterprise DLP & Security
14. As an enterprise security engineer, I want Tzro to detect and redact API keys, credentials, and private IP addresses on-device before requests egress to cloud providers, preventing accidental secret leakage.
15. As an enterprise developer working with proprietary intellectual property, I want Tzro to support bidirectional symbol obfuscation, so that proprietary internal class and method names are masked before cloud transmission and re-hydrated locally.

---

## 4. Implementation Decisions

### 4.1 Monolithic Architecture Cleanup (Scorched Earth)
- **Scrap Legacy Subsystems**: Delete legacy MCP orchestration, Kahn DAG compilation, relational memory/KG neighborhoods, and heavy 4B GGUF sidecar management code.
- **Single Native Binary**: Rebuild Tzro entirely in **Go** as a single binary containing CLI tools, the embedded SQLite engine, Tree-sitter C bindings, and the HTTP loopback proxy.

### 4.2 Core Subsystem Architecture

#### Subsystem 1: Transparent Reverse Proxy & SSE Engine (`pkg/proxy`)
- Implements an HTTP/HTTPS loopback server listening on `127.0.0.1:7878`.
- Supports OpenAI (`/v1/chat/completions`), Anthropic (`/v1/messages`), and Gemini/Vertex compatibility routes.
- Full streaming pass-through for SSE chunks (`text/event-stream`).

#### Subsystem 2: KV-Cache Prefix Lock Guard (`pkg/kvlock`)
- Normalizes outgoing message arrays:
  1. Top-pins static system instructions, repository conventions, and tool definitions in a deterministic byte order.
  2. Isolates dynamic variables (timestamps, ephemeral session identifiers) and places them in trailing message objects.
  3. Ensures byte-for-byte prefix reproducibility across sequential conversation turns.

#### Subsystem 3: Tree-Sitter AST Skeletonizer & Code Pruner (`pkg/ast`)
- Embeds native C Tree-sitter parsers for key languages (Go, TypeScript, JavaScript, Python, Rust, Java, C/C++, Ruby, PHP, C#).
- Parses raw source code into syntax trees; preserves package declarations, imports, types, structs, interfaces, exported signatures, and docstrings.
- Replaces method/function bodies with cryptographic hash tags: `// [body elided: #a8f19c]`.
- Saves full code blocks into the local content store.

#### Subsystem 4: Local Content-Hash Store & Index (`pkg/store`)
- Embedded **SQLite** database operating with WAL mode and `FTS5` (Full-Text Search).
- Tables:
  - `content_blobs (hash TEXT PRIMARY KEY, filepath TEXT, start_line INT, end_line INT, body TEXT, created_at TIMESTAMP)`
  - `symbol_index (symbol TEXT, kind TEXT, filepath TEXT, line INT, hash TEXT)`
  - `cache_sessions (session_id TEXT, prefix_hash TEXT, last_seen TIMESTAMP)`

#### Subsystem 5: High-Speed Discovery Engine (`pkg/probe`)
- Combines embedded ripgrep regex searching with Tree-sitter AST symbol resolution.
- Given a query (e.g. `jwt expiration`), performs rapid regex filtering across the repo, identifies matching AST nodes, and extracts the enclosing function boundary and line range in <5ms.

#### Subsystem 6: Log & JSON Compactor (`pkg/compactor`)
- **Smart JSON Crusher**: Transforms arrays of uniform JSON objects into a schema header + tabular value rows.
- **Stack Trace Elider**: Trims runtime/framework stack frames (Node internals, Go runtime internals, standard library dispatchers) and retains only user-code stack frames and error messages.

#### Subsystem 7: Zero-Cloud DLP & Secret Redactor (`pkg/dlp`)
- High-speed regex scanner matching known secret patterns (OpenAI `sk-`, GitHub `ghp_`, AWS `AKIA`, private keys, JWTs).
- Replaces secrets with anonymized tokens (`[REDACTED_API_KEY_1]`).

#### Subsystem 8: Antigravity Lifecycle Hook Adapter (`pkg/hooks`)
- CLI subcommands (`tzro hook pre-command`, `tzro hook compact-output`, `tzro hook prefix-lock`) reading JSON on stdin and outputting compliant response JSON on stdout.

---

## 5. Testing Decisions

- **Deterministic Unit Testing**:
  - Test Tree-sitter AST pruning across all supported languages: verify interface preservation, body elision, and byte stability.
  - Test Smart JSON Crusher: verify 100% loss-less reversibility and compaction ratios.
  - Test Stack Trace Elider: verify that panic/error messages and user lines are preserved while runtime frames are dropped.
  - Test KV-Cache Prefix Locker: verify that sequential turns with dynamic timestamps produce identical prefix byte sequences up to the cache breakpoint.
- **End-to-End Integration Testing**:
  - Spin up mock upstream Anthropic and OpenAI HTTP servers.
  - Route test agent conversations through `127.0.0.1:7878`.
  - Validate SSE token streaming latency (<1ms added overhead) and verify that payload sizes are reduced by 70%+ on code reads.
- **Memory & Resource Profiling**:
  - Benchmark memory footprint under peak load: assert maximum resident set size (RSS) < 50 MB.
  - Cold startup time benchmark: assert < 15ms.

---

## 6. Out of Scope

- Hosting or running local 70B/13B parameter LLMs (we strictly avoid ML dependencies to remain under 50 MB RAM).
- Static Kahn DAG planning and workflow orchestration.
- Cloud database syncing or external telemetry telemetry services.
- Multi-user authentication servers (Tzro v2 is local-first, developer-owned infrastructure).

---

## 7. Further Notes

Tzro v2 transforms the product from a complex, error-prone agent orchestrator into an indispensable, lightweight infrastructure utility for every AI developer.
