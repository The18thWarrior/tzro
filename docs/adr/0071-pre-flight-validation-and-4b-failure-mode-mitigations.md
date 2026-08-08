# Pre-Flight Validation and 4B Failure Mode Mitigations

Benchmark analysis across 75 executions (runs 18-20) revealed five failure modes in the 4B Local Model's output: meta-response framing (FM1, 13%), data parsing failures (FM2, 47% of data tasks), hallucinated sources (FM3, 9%), codegen logic defects (FM4, 6%), and synthesis incompleteness (FM5, 4%). The governing principle — constrain decoding strictly, offload computation to deterministic tools, and validate using fast traditional software rather than LLM reflection — drives all mitigations. At 4B scale, the model lacks capacity for reliable self-correction; deterministic checks outperform LLM-as-judge.

We introduce **Pre-Flight Validation** as a new Stage 2 in Verified Task Execution, wrapping the existing Structural Pre-Check with two additional deterministic check layers: coverage verification and content validation. We also add **assistant prefilling** to the inference sidecar and **schema enrichment** to the cache bridge for Analyze Nodes. For synthesis incompleteness, we adopt a reactive **Item-Level Scatter** pattern (coverage failure → spawn targeted Probes via MutationBudget) rather than proactive plan-time decomposition.

## Considered Options

### FM5 incompleteness: Outline-first vs. Item-Level Scatter

1. **Outline-first single generation** — prompt the model to plan before generating. Rejected: still asks the 4B model for a long generation pass, fighting against the 400-token attention fatigue threshold where EOS probability spikes.

2. **Proactive plan-time scatter** — Strategic Planner detects N-item tasks and emits a scatter-gather Plan Template. Rejected: the planner cannot predict whether the model will cover all items in one pass. Over-scattering wastes N parallel Probes on tasks the model handles fine.

3. **Reactive Item-Level Scatter** — let the model try first; on coverage failure, spawn targeted follow-up Probes for missing items via MutationBudget. Accepted: reuses existing MutationBudget infrastructure, only pays the cost of additional Probes when coverage actually fails (4% of tasks), and keeps each follow-up generation under the attention fatigue threshold.

### FM1 framing: GBNF-from-token-0 vs. assistant prefilling

1. **GBNF grammar from token 0** — constrain output format via logit masking from the first token. Rejected: creates a "format tax" at 4B scale where the model's chat-tuned logits want conversational filler but the grammar blocks those tokens, producing gibberish.

2. **Assistant prefilling** — inject starting tokens into the assistant turn before generation begins. Accepted: zero latency overhead, mathematically bypasses the chatty phase by starting generation from the desired content prefix. Complemented by regex-based meta-response detection in the Structural Pre-Check as a fallback.

### FM3 hallucination verification: LLM self-review vs. deterministic checks

1. **Dual-pass LLM review** (generate + critique with same model). Rejected: at 4B scale, the same parametric biases that caused the hallucination cause the verifier to confirm it.

2. **Deterministic verification** — HTTP HEAD for URL liveness, substring matching for quote verification against source context. Accepted: zero LLM cost, no confirmation bias, bounded to 2s via context deadline.

## Consequences

- **Verified Task Execution** Stage 2 is now **Pre-Flight Validation**, which orchestrates: (1) Structural Pre-Check (<1ms), (2) coverage verification (<10ms, advisory), (3) content validation (≤2s, advisory). Structural Pre-Check retains its existing definition as one component.
- Coverage and content issues are **advisory only** — logged and fed to the Verification Gate prompt as evidence, but do not independently block. The cloud's `completeness` and `factualGrounding` scores in the Verification Rubric own judgment.
- **Assistant prefilling** is available on `StructuredInferenceRequest.OutputPrefix` but **not yet wired** to any call site. Wiring requires empirical validation of which prefixes are effective per task type. This is on the roadmap.
- **Schema enrichment** is wired into `enrichCacheBridgeContext` — Analyze Nodes automatically receive per-column cardinality, non-null counts, and top values alongside the existing schema introspection.
- **Item-Level Scatter** is defined as an architectural pattern but **not yet implemented**. Implementation requires: (1) wiring CheckCoverage results into the MutationBudget spawn path, (2) a deterministic assembly function for follow-up Probe outputs, (3) integration with the Recall Node to produce a unified refinedContext from original + follow-up outputs. This is on the roadmap.
- The two meta-pattern detectors (`validateSynthesisOutput` for completion-state phrases, `detectMetaResponse` for helpful-assistant framing) remain separate functions — they use different threshold strategies (single-hit vs. ratio-based dominance).
