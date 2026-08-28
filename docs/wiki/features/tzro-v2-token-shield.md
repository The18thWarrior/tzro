# Feature: Tzro v2 — The Local Token Shield

**Status**: In Progress  
**PRD Reference**: [PRD_TZRO_V2.md](../../PRD_TZRO_V2.md) / [.scratch/tzro-v2-token-shield/PRD.md](../../../.scratch/tzro-v2-token-shield/PRD.md)  
**Last Updated**: 2026-08-26  

---

## Overview

Tzro v2 ("The Local Token Shield") is a complete ground-up rewrite of the Tzro execution platform. It transitions Tzro from a complex, multi-model DAG orchestrator and MCP server into an ultra-lean, compiled native Go binary (<50 MB RAM, zero Python/PyTorch dependencies) designed specifically to eliminate cloud API rate limits, avoid KV-cache miss penalties, and slash token waste on resource-constrained hardware.

---

## Core Capabilities

1. **Transparent Reverse Proxy & SSE Engine (`pkg/proxy`)**:
   - Intercepts OpenAI (`/v1/chat/completions`), Anthropic (`/v1/messages`), and Gemini/Vertex API calls on `http://127.0.0.1:7878`.
   - Streaming SSE tokens pass through with sub-millisecond overhead.
2. **KV-Cache Prefix Lock Guard (`pkg/kvlock`)**:
   - Pins system instructions and tool definitions; isolates dynamic timestamps to trailing messages.
   - Benchmarked at 70–99% prompt cache hit rate across 8 LLM providers, avoiding the 12.5x cache miss penalty.
3. **Tree-Sitter AST Skeletonizer (`pkg/ast`)**:
   - Native C Tree-sitter bindings for 10 languages (Go, TS, JS, Python, Rust, Java, C/C++, Ruby, PHP, C#).
   - Replaces method bodies with cryptographic SHA-256 hashes (`// [body elided: #a8f19c]`), achieving 70%–90% token reduction.
4. **Local Content-Hash Store & Index (`pkg/store`)**:
   - Embedded SQLite engine in WAL mode with FTS5 for instant symbol indexing and content retrieval.
5. **Deterministic Discovery Engine (`tzro probe`)**:
   - Sub-millisecond code exploration combining ripgrep and Tree-sitter AST queries.
   - Eliminates 10-turn (250k token) discovery loops in favor of 500-token targeted results.
6. **Deterministic Log & JSON Compactor (`pkg/compactor`)**:
   - Smart JSON Crusher (tabular schemas) and Stack Trace Elider (removes runtime internals from test/build failures).
7. **Zero-Cloud DLP & Secret Masking (`pkg/dlp`)**:
   - On-device regex and entropy scanner masking credentials and private IP addresses before network egress.
8. **Antigravity Lifecycle Hooks (`pkg/hooks`)**:
   - Native integration with Antigravity `hooks.json` (`PreToolUse`, `PostToolUse`, `PreInvocation`).
