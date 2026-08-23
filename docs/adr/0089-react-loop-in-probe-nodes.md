# ADR-0089: Native ReAct Loop within Probe and Research Nodes

## Status
Accepted

## Context
In full-suite benchmarking across 25 tasks (`results-full-local-dag-3` vs `results-full-local-react-2`), empirical results showed:
1. **DAG Superpower**: The DAG execution engine achieved a **92.0% reduction in local tokens** (739k vs 9.22M tokens) and outperformed ReAct on code generation (`3.83` vs `3.79`) and complex architectural synthesis (`2.75` vs `1.30`) where ReAct suffered catastrophic context explosion.
2. **ReAct Superpower**: In open-ended exploratory tasks and broad codebase sweeps, ReAct's dynamic OODA loop allowed the model to iterate across tools naturally without being blocked by static pre-compilation assumptions.
3. **Probe Node Bottlenecks**: Previous attempts to handle open-ended exploration inside DAGs suffered from either rigid GBNF Thought Chains (ADR-0019, context degradation at turn 10), fragile neural edge mutations (ADR-0024, false convergence/halting), or single-shot mega-prompt direct synthesis (ADR-0086, cognitive overload on 4B models).

Rather than forcing open-ended exploration into rigid pre-compiled sub-graphs or abandoning the durable DAG architecture, this ADR adopts a hybrid model: **the outer substrate remains a Durable DAG, while exploratory Probe and Research nodes execute an internal, native ReAct loop.**

## Decisions

1. **Native Go ReAct Loop in `internal/executor` (`RunReActLoop`)**:
   - Probe and Research nodes execute an internal ReAct loop written natively in Go, using tzro's existing tool registry (`tools.Registry`).
   - Connects directly to the local worker sidecar (`llama-server`) via standard OpenAI-compatible `/v1/chat/completions` structured tool calling.
   - Eliminates external CLI or Node/npm dependencies.

2. **Error Handling as Tool Observations**:
   - When a tool fails (e.g. file not found, permission error), the error is returned to the model as a `role: "tool"` message, allowing the agent to self-correct on the subsequent turn rather than aborting the node.

3. **Repetition Guard & Loop Convergence**:
   - Employs `RepetitionGuard` to detect and break identical repeat tool calls.
   - Bounded by a tiered step budget:
     - **Default Budget**: 15 steps (covers >90% of exploration tasks).
     - **Tier 5 / Repository Sweeps**: 25 steps (for whole-codebase audits).
   - When the step budget is exhausted, a forced final synthesis turn is executed with `tools: []`.

4. **Sliding Window Context Protection**:
   - If prompt tokens exceed 12,000 tokens during multi-turn exploration, the harness preserves the immutable System and Goal messages and drops the oldest intermediate tool turn pairs, ensuring constant-time prefill and preventing context window overflow.

5. **Clean Return Payload Contract**:
   - The Probe node returns only the final assistant text message as its output payload (`output.text` / `output.summary`) to downstream DAG nodes (such as Recall nodes, Semantic Validators, or Terminal Tool Sinks).
   - Intermediate tool execution traces are recorded in SQLite `thought_chain` for telemetry, auditing, and UI streaming without polluting downstream DAG context.

## Consequences

### Positive
- **Optimal Topology Mapping**: Combines the unconstrained exploratory power of ReAct with the token efficiency, determinism, and durability of DAG pipelines.
- **Self-Contained & Fast**: Pure Go implementation with zero external runtime dependencies and native KV-cache reuse.
- **Context Hygiene**: Downstream DAG nodes receive only clean, consolidated deliverables without multi-turn conversation debris.
- **Robust Failure Recovery**: Tool errors become learning signals for the agent rather than workflow-terminating exceptions.

### Negative
- Local token consumption on exploration nodes will be higher than single-shot direct synthesis, though strictly bounded by the 15/25 turn cap.
- Multi-turn tool execution increases single-node wall-clock time compared to pure static pre-indexed single-shot prompts.
