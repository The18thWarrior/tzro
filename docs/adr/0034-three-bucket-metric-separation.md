# ADR-0034: Three-Bucket Metric Separation via cloud_dag_raw Benchmark Condition

The benchmark suite conflates three independent savings mechanisms into a single "98% token reduction" headline that can't survive scrutiny. We separate them into three independently measurable buckets by adding a fifth benchmark condition, `cloud_dag_raw`, which runs the full DAG execution pipeline but bypasses the 5-Layer Compaction Pipeline (`cache.Process()`).

**Considered alternatives:**

- **Inline instrumentation** — add byte counters inside `compact()` and compute pipeline savings per tool call within existing conditions. Rejected because DAG structural savings would still need to be derived by subtraction, and the signal is cleaner when each bucket has its own direct measurement.
- **`cloud_dag_no_compaction` naming** — rejected because Probe Nodes still run their own rolling compaction (`compactThoughtChain`), which is intrinsic to probe execution and not a "savings mechanism." The name `cloud_dag_raw` avoids the ambiguity.

**The three buckets and how they're computed:**

| Bucket | Formula |
|---|---|
| DAG structural savings | `cloud_react tokens − cloud_dag_raw tokens` |
| Pipeline compaction savings | `cloud_dag_raw tokens − cloud_dag tokens` |
| Local offloading savings | `cloud_dag tokens − cooperative tokens` |

Bucket attribution is computed post-hoc in the report generator, not embedded in per-result structs, since the numbers are cross-condition deltas.

**Implementation:** The bypass is a context flag (`compaction_disabled`) set only by the comparison harness — the MCP server never sets it, keeping benchmark-only code paths out of production surface area.
