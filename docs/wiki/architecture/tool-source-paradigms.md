# Tool Source Paradigms: WASM vs MCP vs OpenAPI vs Builtin

Analysis of the four tool source paradigms in tzro and their architectural rationale.

## Summary

tzro supports four distinct tool sources — **Builtin**, **WASM**, **OpenAPI**, and **MCP** — all unified behind a single `Tool` interface in `internal/tools/tools.go`. Each paradigm occupies a distinct niche on the trust/weight/capability spectrum. They are **complementary, not competing**.

---

## Capability Spectrum

```
Builtin (Go)  →  WASM (sandboxed)  →  OpenAPI (HTTP dispatch)  →  MCP (stdio subprocess)  →  Containerized MCP (Docker)
     ↑                 ↑                      ↑                          ↑                            ↑
  In-process       Zero authority        Schema-driven HTTP        Full process authority       Docker-isolated
  Max performance  Fast, safe             Declarative integrations Heavyweight, powerful        Most isolated
```

---

## Side-by-Side Comparison

| Dimension | Builtin | WASM (Sandboxed Micro-Skill) | OpenAPI | MCP Host |
|---|---|---|---|---|
| **Domain term** | *(none)* | Sandboxed Micro-Skill | *(none)* | MCP Host / Containerized MCP Host |
| **Runtime** | In-process Go function | In-process wazero sandbox | HTTP client dispatch | Out-of-process child process |
| **Protocol** | Direct function call | JSON via stdin/stdout (WASI) | HTTP REST | JSON-RPC 2.0 over stdio |
| **Sandbox** | None (trusted code) | Hermetic — no FS, no network, no env vars | N/A (calls external APIs) | None by default; Docker optional |
| **State** | In-process | Stateless — fresh per call | Stateless (HTTP) | Stateful — long-lived process |
| **Tool discovery** | Static registration in `Init()` | Scan `.tzro/wasm/` at init | Parsed from OpenAPI specs in SQLite | Dynamic `tools/list` JSON-RPC |
| **Tools per source** | 1 per registration | 1 per `.wasm` binary | N per OpenAPI spec | N per MCP server |
| **Startup cost** | Zero | Microseconds (compile + instantiate) | Zero (HTTP client) | Seconds (process spawn + handshake) |
| **I/O access** | Full | None | Outbound HTTP only | Full (subprocess permissions) |
| **Auto-recovery** | N/A | N/A (ephemeral) | N/A | Yes — restart on EOF/crash, single retry |
| **Result format** | `ToolResult` envelope with meta | Raw string (stdout) | Raw string (HTTP body) | Raw string (JSON-RPC content) |
| **Source tag** | `"builtin"` | `"wasm"` | `"openapi"` | `"mcp"` |

---

## Why Each Paradigm Exists

### Builtin — Trusted platform primitives
Go functions registered directly. Used for core platform tools: `list_tools`, `web_search`, `search_knowledge_base`, `local_db_*`, etc. Maximum performance, zero overhead, full trust.

### WASM — Pure compute inside the trust boundary
Deterministic, sandboxed logic that runs locally with zero ambient authority. The module literally cannot read files, make network calls, or see environment variables. Ideal for:
- Data transformations and validators
- Custom scoring functions
- Agent-synthesized tools (compile Go → WASM → register at runtime)
- Any logic where **security isolation** is more important than I/O access

### OpenAPI — Declarative HTTP integrations
Parsed from OpenAPI specs stored in SQLite. Each operation becomes a tool with schema-driven path/query/body parameter construction and auth header injection. No custom code needed — just register a spec. Ideal for:
- REST API integrations with well-defined schemas
- Services that already publish OpenAPI/Swagger specs

### MCP — Rich external tool servers
Spawns a persistent child process speaking JSON-RPC 2.0 over stdio. Supports the entire MCP ecosystem — any compliant server works with zero code changes. Docker mode adds resource isolation via `--cpus`, `--memory`, and strict env var declaration. Ideal for:
- Database access, filesystem operations
- Complex multi-tool integration servers (Slack, GitHub, PostgreSQL)
- Tools requiring state persistence between calls
- Docker-isolated execution for untrusted servers

---

## Where They Overlap (Minimally)

The only theoretical overlap is a **simple, self-contained tool that doesn't need I/O**. This could be implemented as either WASM or a trivial MCP server. In practice, WASM is strictly better for this case:
- Sub-millisecond startup vs. seconds for process spawn
- Hermetic sandbox vs. full subprocess permissions
- No external dependencies vs. Node.js/Python/Docker runtime

The paradigms are designed to be **complementary layers**, not competing options.

---

## Unified Dispatch

All four sources share the exact same dispatch path through `tools.Registry`:

1. Agent requests tool by name
2. `tools.Call(ctx, name, args)` looks up in registry map
3. Calls `tool.Handler(ctx, args)` — same signature regardless of source
4. Returns string result

The agent never knows or cares whether it's calling a builtin, WASM, OpenAPI, or MCP tool. The Kahn Compiler treats them identically when building Abstract Graphs. The Relational Tool Graph includes all sources in its 2-hop BFS neighborhood calculations for task-to-workflow promotion.

---

## Key References

- [CONTEXT.md](../../../CONTEXT.md) — Domain terms: Sandboxed Micro-Skill (L51), MCP Host (L71), Containerized MCP Host (L75)
- [ADR-0007: MCP Dynamic Proxy](../../adr/0007-mcp-dynamic-proxy.md) — Rationale for stdio-based persistent daemons
- [internal/tools/tools.go](../../../internal/tools/tools.go) — Unified `Tool` interface and registry
- [internal/wasm/wasm.go](../../../internal/wasm/wasm.go) — WASM sandbox implementation
- [internal/mcp/mcp.go](../../../internal/mcp/mcp.go) — MCP daemon lifecycle and auto-recovery
- [internal/tools/openapi.go](../../../internal/tools/openapi.go) — OpenAPI tool dispatch
- [Task-to-Workflow Promotion](task-workflow-promotion.md) — Tool cap calculations across all sources
