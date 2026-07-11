# Synthesis-Aware Context Assembly and Tiered Budgets

Splits `buildAccumulatedContext` into two policies: synthesis nodes get untruncated validator/recall content with no ceiling (quality-first), while mid-DAG nodes get tiered per-type allocation within a dynamic ceiling (latency-first). Replaces the flat per-node budget split from ADR-0043 Mechanism B.

## Status

Proposed

## Context

Benchmark analysis (results-full-3) revealed a bimodal quality distribution: 40% of tasks scored ≥4.5, but 40% scored <2.0. Root cause investigation identified two compounding failures:

1. **Synthesis output leaks execution metadata.** The `terminal_synthesis` node produces summaries of tool call logs and validation reports instead of the actual generated content. This happens because `buildAccumulatedContext` treats all upstream nodes equally — a deterministic `write_file` node's output (`"File written successfully"`) gets the same budget as a validator node's output (which contains the full generated code/documentation). The synthesis node never sees what was actually produced.

2. **Per-node truncation drops spec requirements.** ADR-0043 Mechanism B divides a fixed 16KB budget evenly across nodes (`16000 / nodeCount`). With 6 upstream nodes, each gets ~2.6KB. Recall nodes that synthesized file contents plus spec requirements are truncated below the threshold needed for downstream codegen nodes to extract complete parameters.

Both failures stem from the same architectural assumption: that all consuming nodes have identical context needs. They don't. Synthesis needs content completeness (quality-first). Mid-DAG action/deterministic nodes need parameter extractability within fast inference budgets (latency-first).

## Decision

### Mechanism A: Synthesis-Specific Context Assembly

When `buildAccumulatedContext` is called for a `synthesis` type node:

- **Validator and recall node outputs are fetched untruncated** from SQLite's `RawOutput` column, bypassing per-node budget limits.
- **Deterministic node outputs are capped at 256 chars** — preserves write-success confirmations without wasting context on zero-value messages.
- **No global ceiling** — synthesis is always the terminal DAG node, so its latency doesn't cascade. Quality is prioritized over inference speed.
- **Superseded probe skipping** remains unchanged.

### Mechanism B: Tiered Allocation for Mid-DAG Nodes

For all non-synthesis consuming nodes, replace the flat per-node split with weighted allocation by node type:

| Node Type | Weight | Rationale |
|-----------|--------|-----------|
| recall | 8 | Contains synthesized findings — highest signal density |
| validator | 6 | Contains generated content and extracted parameters |
| action | 4 | Contains tool outputs (search results, API responses) |
| probe | 2 | Raw exploration data (superseded probes already skipped) |
| deterministic | 1 | Confirmation messages, low-value for extraction |

Per-node budget = `(nodeWeight / totalWeights) * totalBudget`.

### Mechanism C: Dynamic Ceiling

Replace the fixed `accumulatedContextMaxChars = 16000` with:

```
totalBudget = min(nodeCount * 4096, 32000)
```

This scales the budget with task size (small DAGs aren't starved) while capping at 32KB to prevent prompt explosion on large DAGs. The ceiling only applies to mid-DAG nodes — synthesis nodes are exempt per Mechanism A.

Config: `accumulatedContextMaxChars` becomes the hard ceiling (default 32000). `accumulatedContextPerNodeFloor` = 4096. `maxAccumulatedContextNodes = 6` is unchanged.

## Considered Options

- **Augment `RawOutput` at tool execution time** — persist `write_file`'s `content` argument alongside its success result. Rejected: requires schema migration and changes the persistence contract. The validator node already has the content.

- **Give synthesis nodes `read_file` access** — let synthesis actively retrieve written content. Rejected: violates the synthesis node's role as a passive compiler (not an explorer). Would require making synthesis an action node.

- **Raise the flat budget to 48KB** — keeps the simple flat split, just bigger. Rejected: doesn't solve the structural problem (deterministic success messages still waste budget). Also pushes all mid-DAG nodes into the slow inference zone (>10K prompt tokens).

- **Uncapped tiered budgets with no ceiling** — let tiered weights determine the total. Rejected: a 10-node DAG with 3 recall nodes would produce 60K+ char context, collapsing local model speed for mid-DAG action nodes whose latency does cascade.

## Consequences

- Synthesis prompts may be large (20KB+) for content-heavy tasks. Accepted: synthesis is terminal, its latency doesn't multiply. The existing ConfidenceTier gate will escalate the most expensive cases to cloud.

- `buildAccumulatedContext` gains awareness of the calling node's type. This couples context assembly to the node type system, which it previously ignored. Mitigated by keeping the interface change minimal (one `callingNodeType string` parameter or a separate `buildSynthesisContext` function).

- The dynamic ceiling defaults to 32KB (2x the previous 16KB). Users with constrained hardware can lower `accumulatedContextMaxChars` to restore the old behavior.

## References

- ADR-0043: Two-Tier Context Budget (superseded Mechanism B)
- ADR-0037: Recall Node for Discovery-Synthesis Alignment
- ADR-0042: Map-Reduce Recall and Shallow Planning
- ADR-0030: Proactive Binding Splice for Deterministic Resolutions
- [Benchmark Evaluation: results-full-3](file:///Users/jp/.gemini/antigravity-ide/brain/4d24368a-4df2-42cb-a827-beffde1f9b50/benchmark_evaluation_run3.md)
