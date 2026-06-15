# Response Resolver

**Status**: PRD published, ready for agent  
**ADR**: [ADR-0029: Response Resolver and Semantic Binding Fallback](../../docs/adr/0029-response-resolver-and-semantic-binding-fallback.md)  
**PRD**: [.scratch/response-resolver/PRD.md](../../.scratch/response-resolver/PRD.md)

## Summary

The **Response Resolver** is a transparent post-execution enhancement to the executor's `resolveDynamicBindings` function that makes tool outputs discoverable by downstream DynamicBindings references. It is the output-side counterpart to the **Semantic Validator** (input-side).

Uses a three-tier resolution cascade:
1. **Recursive key search** — parse raw JSON output and walk the tree for a matching key at any depth
2. **KV-line key search** — fall back to `key: value` per-line parsing for non-JSON outputs
3. **Semantic fallback** — invoke the Local Model with a focused prompt to semantically match the binding key to an output value

## Motivation

The 2026-06-10 benchmark (`benchmark_debug2.log`) showed ~30 `DynamicBindings` resolution failures across 10 cases. The root cause is that binding keys (e.g., `default_email_address`) don't match actual tool output keys (e.g., `email`), and the current lookup is top-level-only — missing nested values entirely.

Especially relevant for **MCP Server Mode**, where the harness agent generates the Abstract Graph and cannot know tool output schemas.

## Key Design Decisions

| Decision | Outcome |
|---|---|
| Where does output schema live? | Internal to tzro — harness doesn't know output schemas |
| New DAG node? | No — transparent step inside action node execution |
| Flatten outputs? | No — recursive key search at read-time, no new storage |
| Semantic fallback cap? | No cap — ~100 tokens per call is negligible |
| Non-JSON outputs? | Skip to semantic fallback — format-agnostic |

## Modules

1. **Recursive Key Resolver** — pure function, walks JSON recursively for key matches
2. **Semantic Binding Resolver** — Local Model inference for key name mismatch resolution
3. **Updated `resolveDynamicBindings`** — three-tier cascade integration in the executor

## Related

- [Semantic Validator](semantic-validator.md) — input-side counterpart
- [ADR-0028: Semantic Validator Seam](../../docs/adr/0028-semantic-validator-seam.md)
