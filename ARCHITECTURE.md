# Tzro v2 Architecture: The Local Token Shield

Tzro v2 is an ultra-lightweight, compiled native Go system (<50 MB RAM) built to eliminate API rate limits and token bloat on resource-constrained hardware. It replaces legacy heavy ML sidecars and static DAG compilers with high-speed deterministic context pruning, KV-cache locking, and sub-millisecond local discovery.

---

## 1. System Topology

```
┌─────────────────────────────────────────────────────────────┐
│  Developer / Agent (Cursor, Claude Code, Antigravity, CLI)  │
└──────────────────────────────┬──────────────────────────────┘
                               │ (Loopback Reverse Proxy / CLI)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                 TZRO v2 LOCAL TOKEN SHIELD                  │
│                                                             │
│  [pkg/proxy]   Transparent Loopback Proxy (port 7878)       │
│  [pkg/kvlock]  KV-Cache Prefix Lock Guard (>90% hit rate)   │
│  [pkg/ast]     Native Tree-Sitter AST Skeletonizer (CST)    │
│  [pkg/probe]   Sub-Millisecond Codebase Discovery (ripgrep) │
│  [pkg/store]   SQLite Content-Hash Store (WAL + FTS5)       │
│  [pkg/compactor] Smart JSON Crusher & Stack Trace Elider    │
│  [pkg/dlp]     Zero-Cloud Secret Redaction & Rehydration    │
│  [pkg/hooks]   Antigravity Lifecycle Hook Adapter           │
└──────────────────────────────┬──────────────────────────────┘
                               │ (Dense, High-Signal, Cache-Locked Payload)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│           Cloud LLM Provider (Anthropic / OpenAI)           │
│           ~80% Token Reduction / Zero Rate Limits           │
└─────────────────────────────────────────────────────────────┘
```

---

## 2. Component Directory Structure

```
tzro/
├── cmd/
│   └── tzro/          # Main CLI entrypoint (start, probe, skeleton, expand, compact, hook, status)
├── pkg/
│   ├── ast/           # Tree-sitter Concrete Syntax Tree (CST) parsing and body elision
│   ├── compactor/     # Smart JSON Crusher & Stack Trace Elider
│   ├── dlp/           # Zero-Cloud Data Loss Prevention & Secret Redaction
│   ├── hooks/         # Antigravity Lifecycle Hook Handlers (PreToolUse, PostToolUse, PreInvocation)
│   ├── kvlock/        # KV-Cache Prefix Lock Guard & Message Normalizer
│   ├── probe/         # Local high-speed ripgrep + Tree-sitter discovery engine
│   ├── proxy/         # Reverse HTTP/HTTPS proxy with Server-Sent Events (SSE) streaming pass-through
│   └── store/         # Embedded SQLite WAL mode + FTS5 content-addressed store
└── website/           # Documentation and landing page
```

---

## 3. Data Flow & Subsystem Deep-Dive

### A. The Passive Ingress Plane (`pkg/proxy`)
1. **Loopback Interception**: The agent sends standard OpenAI (`/v1/chat/completions`) or Anthropic (`/v1/messages`) payloads to `http://127.0.0.1:7878`.
2. **DLP Redaction (`pkg/dlp`)**: The request payload is scanned for API keys, private credentials, and private IPs. Matched secrets are replaced with `[REDACTED_...]` placeholders, and an in-memory mapping is retained.
3. **KV-Cache Prefix Locking (`pkg/kvlock`)**: System prompts and tool definitions are sorted and pinned at the top of the message array. Volatile timestamps and ephemeral IDs are isolated to trailing turns. Guarantees >90% prefix cache reuse, avoiding Anthropic/OpenAI's 12.5x cache miss penalty.
4. **SSE Pass-Through**: The request is forwarded upstream, and response tokens stream back via Server-Sent Events with <1ms latency.

### B. The Code Pruning Plane (`pkg/ast` & `pkg/store`)
1. **Tree-Sitter Parsing**: Source files are parsed into syntax trees across 10 languages (Go, TypeScript, Python, Rust, Java, C/C++, etc.).
2. **Body Elision**: Exported signatures, types, structs, interfaces, and docstrings are retained. Function bodies are replaced with `// [body elided: #hash]`.
3. **Content-Hash Storage**: The full uncompressed function bodies are stored in local SQLite (`~/.tzro/token_shield.db`).
4. **On-Demand Expansion (`tzro expand`)**: When the model needs to edit a specific method, it retrieves only that specific block via hash lookup.

### C. The Active Discovery Plane (`pkg/probe`)
1. **Single-Shot Search**: When an agent needs to locate code, `tzro probe "<query>"` searches the workspace using ripgrep with `.gitignore` awareness.
2. **AST Scope Isolation**: For matching files, Tree-sitter isolates the enclosing symbol boundaries and exact line numbers.
3. **High-Density Output**: Returns a ~500-token summary in <5ms, replacing 10-turn (250k token) conversational discovery loops.

---

## 4. Hardware & Resource Budget

- **Language**: Pure Go with compiled Tree-sitter C bindings and pure-Go SQLite (`modernc.org/sqlite`).
- **Memory**: Idle RSS < 15 MB, Peak RSS < 50 MB.
- **Cold Start**: < 10 ms.
- **Hardware Target**: 8GB–16GB developer laptops, VDI instances, edge devices, and CI/CD runners.