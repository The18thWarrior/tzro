# TZRO: The Local Token Shield & Context Optimization Engine

<p align="center">
  <img src="website/logo.png" alt="TZRO Logo" width="120" />
</p>

<p align="center">
  <strong>Eliminate cloud API rate limits, lock KV-cache prompt prefixes, and slash agentic token waste on resource-constrained hardware.</strong>
</p>

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License: Apache 2.0" /></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go Version" /></a>
  <a href="#benchmark"><img src="https://img.shields.io/badge/Memory%20Footprint-%3C50MB%20RAM-success.svg" alt="Memory" /></a>
  <a href="#benchmark"><img src="https://img.shields.io/badge/Token%20Savings-70%25--90%25-purple.svg" alt="Token Savings" /></a>
</p>

---

## 🛑 The Problem: Quadratic Context Explosion & The 12.5x Cache Miss Penalty

Autonomous coding agents (Claude Code, Cursor, Antigravity, Aider, Cline) consume massive volumes of tokens during multi-turn developer interactions:

1. **Transient Tool Bloat**: Directory listings, raw source files dumped for inspection, verbose build logs, repetitive stack traces, and JSON API payloads account for **60% to 90% of all tokens consumed**.
2. **Context Rot**: As contexts exceed 100k+ tokens, model reasoning degrades (~2% instruction-following loss per 100k tokens), leading to hallucinated APIs and lost system constraints.
3. **The 12.5x KV-Cache Penalty**: Major providers (Anthropic, OpenAI) offer a 90% discount on cached prompt prefixes ($P_{\text{read}} = 0.10 \times P_{\text{base}}$), but penalize cache misses with a 25% surcharge for cache writes ($P_{\text{write}} = 1.25 \times P_{\text{base}}$). A single unaligned byte or reordered tool schema invalidates the cache, making subsequent turns **12.5× more expensive**.
4. **Heavy ML Sidecar Bloat**: Traditional context compression tools (e.g. Headroom) rely on Python runtimes and PyTorch models consuming 4.8 GB+ RAM with 60-second cold starts, rendering them unusable on standard developer hardware (8GB–16GB laptops, VDI instances, CI/CD runners).

---

## 🛡️ The Solution: Tzro v2 ("The Local Token Shield")

**Tzro v2** is an ultra-lightweight, compiled native Go binary (<50 MB RAM, zero Python/PyTorch dependencies) that operates across two synchronized planes:

```
┌─────────────────────────────────────────────────────────────┐
│  Developer / Agent (Cursor, Claude Code, Antigravity, CLI)  │
└──────────────────────────────┬──────────────────────────────┘
                               │ (Transparent Loopback Proxy / CLI)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                 TZRO v2 LOCAL TOKEN SHIELD                  │
│                                                             │
│  1. KV-Cache Prefix Lock Guard (70-99% Cache Read Hit Rate)  │
│  2. Tree-Sitter AST Skeletonizer (70-90% Token Reduction)   │
│  3. Sub-Millisecond Local Discovery (`tzro probe`)          │
│  4. Local SQLite FTS5 Content-Hash Store (`tzro expand`)    │
│  5. Smart JSON Crusher & Stack Trace Elider                 │
│  6. Zero-Cloud DLP / Secret Masking                         │
└──────────────────────────────┬──────────────────────────────┘
                               │ (Dense, High-Signal, Cache-Locked Payload)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│           Cloud LLM Provider (Anthropic / OpenAI)           │
│           ~80% Token Reduction / Zero Rate Limits           │
└─────────────────────────────────────────────────────────────┘
```

---

## ⚡ Quickstart

### 1. Install Tzro
```bash
curl -fsSL https://get.tzro.ai | sh
```
*Or build directly from source:*
```bash
go install ./cmd/tzro
```

### 2. Start the Token Shield Daemon
```bash
tzro start
```
*The shield starts listening on `http://127.0.0.1:7878`.*

### 3. Connect Your AI Coding Agents

#### Claude Code / Anthropic
```bash
export ANTHROPIC_BASE_URL=http://localhost:7878
```

#### Cursor / OpenAI / Aider
```bash
export OPENAI_BASE_URL=http://localhost:7878/v1
```

#### Antigravity Native Hooks
Add to `.agents/hooks.json` or `~/.gemini/config/hooks.json`:
```json
{
  "tzro-token-shield": {
    "enabled": true,
    "PostToolUse": [
      {
        "matcher": "run_command",
        "hooks": [{ "type": "command", "command": "tzro hook compact" }]
      }
    ]
  }
}
```

---

## 🎛️ The 6 Core Shield Subsystems

### 1. KV-Cache Prefix Lock Guard (The Financial Shield)
- Pins system prompts, repository instructions, and tool definitions in deterministic byte order at the start of the message array.
- Isolates dynamic variables (timestamps, ephemeral session tokens) to trailing messages.
- Benchmarked at **70–85% prompt cache hit rate** during real agent workflows and **up to 99% under controlled conditions** (8-model benchmark across OpenRouter). Protects you from the 12.5x cache miss penalty.
- E2E benchmarks show kvlock normalization improves cache effectiveness by **5–10 percentage points** over native provider caching (tested with GPT-5.6 Luna and MiniMax).

### 2. Native Tree-Sitter AST Skeletonizer
- Language-aware structural pruning across 10 programming languages (Go, TypeScript, JavaScript, Python, Rust, Java, C/C++, Ruby, PHP, C#).
- Preserves package declarations, imports, types, structs, interfaces, exported signatures, and docstrings.
- Replaces function bodies with cryptographic hash tags: `// [body elided: #a8f19c]`, achieving **70%–90% token reduction**.
- The full body is indexed in local SQLite. Models expand bodies on demand via `tzro expand <hash>`.

### 3. Sub-Millisecond Local Discovery (`tzro probe`)
- Replaces 10-turn cloud exploration loops with single on-device queries.
- Combines embedded ripgrep regex searching with Tree-sitter AST symbol resolution to locate exact line ranges and symbol scopes in <5ms.
```bash
$ tzro probe "jwt token validation"
Found 1 match for "jwt token validation":
- ValidateToken (function in auth/jwt.go:45-78) [Hash: #8f2a1c]
```

### 4. Smart JSON Crusher & Stack Trace Elider
- **Smart JSON Crusher**: Automatically detects arrays of uniform JSON objects and formats them into compact Markdown tables, reducing token burn by up to 80%.
- **Stack Trace Elider**: Strips standard framework and runtime internal frames from test failures and panics, preserving user-code error lines.

### 5. Zero-Cloud Data Loss Prevention (DLP)
- On-device regex and Shannon entropy scanner detects API keys (`sk-`, `ghp_`, `AKIA...`), private keys, passwords, and private IPs.
- Masks secrets before request egress to cloud providers; rehydrates returned code edits locally.

### 6. Local Content-Hash Store
- Embedded SQLite database in WAL mode with FTS5 full-text search.
- Stores content-addressed blobs and symbol indices locally on your machine (<10 MB disk footprint).

---

## 📊 Performance & Footprint Comparison

| Metric | PyTorch ML Sidecars (e.g. Headroom) | Unoptimized Agent Loops | Tzro v2 (Token Shield) |
| :--- | :--- | :--- | :--- |
| **System Memory (RAM)** | ~4.8 GB RAM (8 PyTorch workers) | N/A | **< 50 MB RAM** (Native Go) |
| **Cold Start Latency** | ~60 seconds (model downloads) | 0 ms | **< 10 ms** (Instant) |
| **GPU Dependency** | Required for fast inference | None | **Zero GPU required** |
| **Prompt Cache Stability** | Unstable (reordered turns) | Variable (12.5x miss penalties) | **70–99% Cache Read (benchmarked)** |
| **Code Read Token Reduction**| 0% (Full files sent) | 0% (Full files sent) | **70% – 90% Reduction** |
| **Codebase Discovery Loop** | 10 turns (~250,000 cloud tokens) | 10 turns (~250,000 tokens) | **1 turn via `tzro probe` (<500 tokens)** |

---

## 💻 CLI Reference

```bash
# Start the background proxy daemon
tzro start --port 7878

# Check real-time token shield metrics and memory footprint
tzro status

# Fast local codebase exploration (0 cloud tokens)
tzro probe "auth middleware jwt"

# Generate AST skeleton for a source file
tzro skeleton ./pkg/kvlock/kvlock.go

# Retrieve original full code body for a hash
tzro expand aa179288

# Pipe raw test logs or JSON on stdin for compaction
go test ./... 2>&1 | tzro compact
```

---

## ⚖️ Development & Testing

```bash
# Run all unit and integration tests
go test -v ./...

# Format source files
go fmt ./...

# Build single static binary
./build.sh
```

---

## 📄 License

Licensed under the [Apache 2.0 License](LICENSE).
