# Tiered Codegen Repair Escalation

Benchmark run `results-full-1` showed 3/20 codegen tasks (all T3 Go) failing with 2.0/5.0 quality despite the CompilationGateHook and Edge Thought repair loop (ADR-0036) firing correctly. The 4B local model generated broken code, the compilation gate detected the failure and injected structured repair prompts, but the spawned repair node — running on the same 4B model — could not fix its own mistakes (type system errors, concurrency patterns). This mirrors how frontier agents handle codegen: even large models produce import errors and lint failures, but their ReAct loops succeed because the model doing the repair is capable enough to reason about compiler output.

We introduce tiered repair escalation: the local model gets 2 repair attempts (existing Edge Thought spawn mechanism). If the mutation budget is exhausted and code still doesn't compile, the repair inference call (not the whole task) escalates to the cloud model. The escalation payload is narrow — compiler errors, spec, broken code, and module context (~2K tokens input) — so it's cheap (~$0.005 per attempt). This preserves the local-first principle: the task stays local, only the final repair call escalates.

Cloud repair is **mode-dependent**: enabled in Direct mode (caller delegated quality responsibility), disabled in Draft/Pseudocode mode (caller provided architectural scaffolding and expects `complexity_exceeded` with the failing code + compiler errors so they can do targeted edits, not full rewrites). The existing `strict-local` Privacy Level kills all cloud calls regardless.

This partially reverses ADR-0036's rejection of cloud escalation for codegen: "Escalation would violate the design boundary." The original rejection was about cloud dependency for routine codegen. This is cloud as a safety net for the rare case where local repair exhausts its budget — the same philosophy as Confidence Tier (local-first with cloud exception), applied at the repair level.

## Considered Options

- **Full cloud routing for T3+ codegen tasks**: Route the entire task to cloud when pre-flight classification detects hard codegen. Rejected — violates ADR-0010 (local-default/cloud-exception) and turns tzro into a cloud-dependent proxy for hard tasks. Defeats the purpose of offloading to tzro.
- **Larger local model for repair**: Swap to an 8B/14B GGUF for the repair inference call only. Stays local-first but requires multi-model orchestration for a single task. Deferred — can be revisited when the model catalog supports hot-swapping.
- **Same 4B model with richer context**: Instead of a stronger model, inject stdlib documentation snippets, full module context, and exact failing test cases. The benchmark showed the 4B model makes the same class of error on retry regardless of context quality. Insufficient for T3 type system reasoning.
- **Task Envelope pre-flight gate**: Classify task type, target language, and required capabilities before planning. Rejected as YAGNI — the tiered repair loop addresses the actual gap without adding a new classification layer.

## Consequences

- `CompilationGateHook.OnEdgeTraversal` gains a cloud repair path after mutation budget exhaustion. The cloud call uses the existing `inference.CloudInfer` interface — no new infrastructure.
- Draft mode `complexity_exceeded` responses now include the failing code file and compiler errors, enabling the harness to do targeted edits instead of full rewrites.
- `tzro_run` codegen tasks route through `tzro_code` via the planner (plan template with `tzro_code` action) instead of emitting raw `source_code` nodes — eliminates pipeline duplication.
- ADR-0036's design boundary is relaxed: `tzro_code` now has a conditional cloud dependency (Direct mode only, `strict-local` still kills it).
