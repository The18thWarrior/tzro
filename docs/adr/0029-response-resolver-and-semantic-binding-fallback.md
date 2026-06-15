# 29. Response Resolver and Semantic Binding Fallback

Date: 2026-06-10

## Status

Proposed

## Context

The Kahn Compiler injects `DynamicBindings` into DAG nodes to pass data downstream: `paramName → "nodeId.output.propertyName"`. At execution time, `resolveDynamicBindings` looks up the property from the upstream node's raw output. This fails in two ways:

1. **Property name mismatch.** The planner (or harness agent in MCP Server Mode) guesses at property names (e.g., `default_email_address`) that don't match the tool's actual output keys (e.g., `email`). In the 2026-06-10 benchmark, ~30 bindings failed with `"Could not resolve binding"`.
2. **Nested values.** The current lookup is top-level-only. A tool returning `{"data":{"contact":{"email":"..."}}}` won't match a binding for `email`.

We considered three approaches:
- **Extend MCP tool definitions with `outputSchema`** — rejected because the harness agent shouldn't need to know tzro's internal output schemas, and some tools have dynamic/unpredictable outputs.
- **Write-time normalization (flattening)** — rejected as premature complexity. The raw output is already stored; the lookup just needs to be smarter.
- **Recursive key search + semantic fallback** — chosen. Simple, handles both known and unknown output schemas, no new storage.

## Decision

We are introducing a **Response Resolver** — a transparent post-execution step within action nodes that makes tool outputs resolvable by downstream DynamicBindings. It uses a three-tier resolution cascade:

1. **JSON recursive key search.** Parse the raw output as JSON. Recursively walk the tree for a key matching the binding's property name. If exactly one match is found, return it deterministically. If multiple matches (key collision), fall through to tier 3.
2. **KV-line key search.** If the output isn't valid JSON, attempt `key: value` per-line parsing and do a key lookup. Matches the existing `InterpolateVariables` fallback.
3. **Semantic fallback via Local Model.** When tiers 1–2 fail or produce collisions, invoke the Local Model with a focused prompt: "Given this output, which value corresponds to `{bindingPropertyName}`?" This handles unknown output schemas, non-JSON formats, and property name mismatches (e.g., `default_email_address` → `email`). No hard cap on invocations — the overhead (~100 tokens per call) is negligible relative to validator/execution node inference.

The Response Resolver is **not** a new DAG node type. It lives inside `resolveDynamicBindings` in the executor, keeping the existing binding contract (`nodeId.output.propertyName`) unchanged. The harness agent continues writing bindings as before — tzro handles the impedance mismatch internally.

## Considered Options

| Option | Why Rejected |
|---|---|
| Extend MCP with `outputSchema` | Couples harness to tzro internals; some tools have dynamic outputs |
| Write-time flattening into normalized property map | New storage format, flattening logic, collision handling — all unnecessary if read-time lookup is recursive |
| Hard cap on semantic fallback calls | 1–2 calls per task at ~100 tokens each is negligible overhead; capping would force silent failures |

## Consequences

- **Positive:** Eliminates the ~30 `"Could not resolve binding"` warnings per benchmark run without requiring output schema declarations.
- **Positive:** Handles tools with unknown/dynamic output schemas via semantic fallback — no upfront registration needed.
- **Positive:** No new storage or node types. Changes are localized to `resolveDynamicBindings` in the executor.
- **Negative:** Semantic fallback adds Local Model inference calls (typically 1–2 per task). Acceptable given the token budget.
- **Negative:** Recursive key search can produce false matches on common key names (e.g., `id`, `status`). Collisions fall through to semantic resolution.
