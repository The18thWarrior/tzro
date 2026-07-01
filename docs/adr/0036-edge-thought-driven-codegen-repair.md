# ADR-0036: Edge Thought Driven Codegen Repair

Codegen benchmark run 3 shows an 80% local compilation failure rate, with 50% of failures being mechanically fixable (hallucinated imports, type mismatches). Rather than building a bespoke retry loop inside `tzro_code`, we use the existing Edge Thought and Activation Threshold infrastructure (ADR-0024) to drive compilation repair through node spawning. This unifies the repair mechanism with the general-purpose iterative refinement that Edge Thoughts already provide across all DAG nodes.

When a codegen synthesis node completes, the compilation gate (`RunCompilationGate`) runs as a deterministic side effect. The compilation result — pass/fail plus error output — is injected into the Edge Thought context before the Local Model generates its reasoning. If compilation failed, the Edge Thought naturally reports low confidence, and the Activation Threshold triggers a spawned repair node. The repair node receives compacted context carrying the Edge Thought's *diagnosis* of why compilation failed — not just raw compiler errors but reasoned analysis (e.g., "the model imported `zod` but no third-party packages are available in this environment"). The mutation budget is capped at 2 spawned repair nodes per task.

Both the initial generation prompt and spawned repair node instructions include **environmental context** discovered via filesystem scan (`DiscoverModuleContext`) — available packages from `go.mod`/`package.json`, module path, stdlib-only constraints. This addresses hallucinated imports (the #1 compilation failure mode at 4/8 failures) at the source.

When repair iterations exhaust the mutation budget: in Direct mode (T1), `tzro_code` returns `complexity_exceeded` — the task is beyond local capability. In Expand mode (pseudocode), it returns a structured failure with the last compilation errors — the harness provided pseudocode and needs the error details.

**Considered alternatives:**

| Option | Why Rejected |
|---|---|
| Bespoke retry loop inside `tzro_code` handler | Each iteration is memoryless — the model sees raw compiler errors but no reasoning about *why* the failure occurred. Edge Thought compaction carries forward a diagnosis, making each spawned node more informed than a blind retry. Also creates a one-off repair mechanism instead of leveraging existing infrastructure. |
| Cloud Model escalation on compilation failure | `tzro_code` explicitly has no cloud model dependency (Pseudocode Expansion PRD). Escalation would violate the design boundary. Deferred to a future re-evaluation of Confidence Tier routing (ADR-0020) for codegen. |
| Deterministic Edge Thought override (skip LLM inference, set confidence directly from compilation result) | Loses the reasoning that makes Edge Thoughts valuable. The compilation gate provides evidence; the Edge Thought provides diagnosis. The spawned repair node benefits from the diagnosis, not just the evidence. |
| Corrective Micro-Skill extraction from successful repairs | Adds complexity for marginal benefit. The Local Model's ability to meta-reason about its own failures is precisely the capability gap we're working around. Deferred to the Confidence Tier re-evaluation. |

## Consequences

- **Positive:** Codegen repair uses the same mechanism as all other iterative DAG refinement — no special-purpose code paths. Repair nodes are checkpointed, crash-resumable, and visible in task logs.
- **Positive:** Environmental context injection (`DiscoverModuleContext`) addresses hallucinated imports in both initial generation and repair, collapsing what was originally two separate work items (P0 repair loop + P1 import guard) into one.
- **Positive:** Edge Thought compaction carries forward *reasoning* about failures, not just raw compiler errors. Each spawned repair node is better-informed than the last.
- **Negative:** Compilation gate must run as a side effect before Edge Thought generation — this is a new integration point between the quality gate and the executor's edge traversal logic.
- **Negative:** `BuildRepairDAG` in `codegen_repair.go` becomes dead code. `BuildRepairPrompt` may still serve as the template for spawned repair node instructions.
