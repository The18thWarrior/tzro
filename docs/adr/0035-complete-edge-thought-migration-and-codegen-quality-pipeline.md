# ADR-0035: Complete Edge Thought Migration and Codegen Quality Pipeline

The codegen pipeline averages 2.12/5.0 quality (benchmark run 9) with a 56% failure rate. Rather than patching the deprecated Probe Node system (ADR-0019), we complete the Edge Thought migration (ADR-0024) — which was fully implemented at the infrastructure layer on 2026-06-07 but never wired into production — and build codegen quality improvements on top of it.

The migration switches production from the level-based `ExecuteGraph` to the ready-queue-based `ExecuteGraphReactive`, wires a real `EdgeThoughtInference` into `GlobalEngine`, and replaces the `RunProbe` Thought Chain loop with Edge Thought node spawning using rolling compaction and auto-injected synthesis nodes.

**Considered alternatives:**

| Option | Why Rejected |
|---|---|
| Patch `ProbeConfig`/`RunProbe` with `OutputFormat` and step budget caps | Deepens investment in deprecated infrastructure. The Probe's internal loop hides tool calls as opaque internal steps, defeating the durability and debuggability guarantees that Edge Thoughts provide. |
| Phased migration (switch executor first, replace probes later) | Testing the full flow takes hours per benchmark run. With a one-line rollback path (`ExecuteGraphReactive` → `ExecuteGraph` in `task.go`), the risk of big-bang is bounded. |
| Rich context injection (full predecessor outputs into spawned nodes) | Unbounded prompt growth. Rolling compaction — lifted from the probe's existing `compactEvery` logic — keeps prompts bounded while preserving exploration context. |
| Synthesis as the target node itself | Dual responsibility (synthesize + execute action). Auto-injecting a synthesis node keeps target nodes focused on their declared action and makes synthesis explicit/debuggable. |
| Hardcoded validation commands (`go vet`, `tsc --noEmit`) | Too brittle across languages/frameworks/params. The Cloud Planner specifies the validation command per task, leveraging its world knowledge of toolchains. |
| Heuristic complexity routing for `tzro_code` (word count, keyword detection) | Semantic-unaware. Local Model classification (`simple`/`moderate`/`complex`) costs ~2s, zero cloud tokens, and weighs the spec's actual requirements against existing file content. |

## Consequences

- **Positive:** Unifies two competing execution mechanisms (Probe Thought Chain vs. Edge Thought spawning) into one. Every tool call becomes a checkpointed, crash-resumable DAG node visible in task logs.
- **Positive:** Codegen synthesis constraints (`OutputFormat`, `OutputLanguage`) apply to any node via `GraphNode` fields, not just probes. Works for both `tzro_run` cooperative mode and complex `tzro_code` tasks.
- **Positive:** Compilation validation is language-agnostic — the Cloud Planner emits the validation command, so new languages/frameworks are supported without engine changes.
- **Positive:** `tzro_code` T1–T2 tasks (single-node `reason_code`) are unaffected — zero latency/cost regression for simple codegen.
- **Negative:** Rolling compaction across spawned nodes is slightly lossier than the probe's tight context-carrying loop. Each spawned node sees a compacted summary rather than raw accumulated output.
- **Negative:** The `ExecuteGraph` level-based executor becomes dead code. Must be cleaned up in a follow-up once benchmark validation confirms no regression.
- **Negative:** Probe backward compat shim adds a code path that silently rewrites `type: "probe"` → `type: "action"` with threshold 0.8 — surprising if you don't know about it.
