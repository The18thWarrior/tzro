# MCP Singleton Guard

**PRD**: [`.scratch/mcp-singleton-guard/PRD.md`](../../.scratch/mcp-singleton-guard/PRD.md)
**Status**: Ready for agent
**Last Updated**: 2026-06-09

## Summary

Prevents duplicate `tzro-mcp` processes per workspace. When multiple IDE language server instances each spawn their own `tzro-mcp` child, the second instance silently exits after detecting the first via a `flock(2)`-based PID lockfile at `<workspace>/.tzro/mcp.lock`.

## Motivation

Observed failure: two Antigravity IDE language server processes (one standard, one LSP-enabled) each spawned a `tzro-mcp` child. Both opened `tzro.db` in WAL mode simultaneously, causing `SQLITE_BUSY` errors, duplicate **Background Agent** firings (**Observer** and **Sentinel**), doubled RSS, and conflicting **Durable Notification** writes.

## Key Decisions

- **Locking mechanism**: `flock(2)` (kernel-released on crash/SIGKILL), not advisory `fcntl` or port binding (no TCP port exists for stdio-based MCP).
- **Exit behavior**: Second instance exits with code 0 to prevent IDE retry loops.
- **Scope**: Workspace-local (`config.ResolvePath`), not machine-global.
- **No proxy/forward**: The second instance does not attempt to proxy stdio to the first. IDE handles reconnection natively.

## Modules

| Module | Role |
|---|---|
| `internal/pidlock` | Deep module: `Acquire`, `IsHeld`, stale PID reclaim |
| `cmd/tzro-mcp/main.go` | Lock acquisition at startup, pre-bootstrap |

## Related

- [MCP Server Mode](../../CONTEXT.md) — Domain glossary entry for MCP Server Mode
- [ADR-0007: MCP Dynamic Proxy](../adr/0007-mcp-dynamic-proxy.md) — MCP Host stdio process model
- [ADR-0022: Background Agent Abstraction](../adr/0022-background-agent-abstraction-and-observer-refactor.md) — Observer/Sentinel lifecycle
