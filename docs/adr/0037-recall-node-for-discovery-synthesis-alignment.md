# ADR-0037: Recall Node for Discovery-Synthesis Alignment

## Status
Accepted

## Context
In cooperative execution mode (local + cloud), we observed a "Synthesis Cliff" where Probe nodes (ADR-0019) would successfully discover massive amounts of information but fail to synthesize it into a cohesive final response. This was due to the local model (4B) being cognitively overloaded by the dual responsibility of exploration (tool-heavy reasoning) and synthesis (summarization of large context).

Benchmarks (results-docgen-2) showed that even when the Probe node gathered 100% of the required data, its final output was often an empty placeholder or a generic "success" message, causing downstream DAG nodes to fail or hallucinate missing data.

## Decision
We will introduce a specialized node type called the **Recall Node**.

1.  **Functional Decoupling**: The responsibility of synthesis is moved out of the Probe node and into a downstream Recall node.
2.  **SCT Compiler Injection**: The `ExpandToSCTGraph` phase in `internal/compiler/sct_compiler.go` will automatically inject a `Recall` node after every `Probe` node.
3.  **Metacognitive Traversal**: Unlike standard nodes, the Recall node has access to a specialized discovery manifest of its upstream dependencies. It runs an internal execution loop allowing it to selectively "recall" (fetch) detailed tool outputs from the `thought_chain` table.
4.  **Information Alignment**: The Recall node is provided with a "Clean Room" context—it only sees the tool outputs from the probe, not the model's intermediate internal reasoning (the "sludge"), reducing context window pollution and improving synthesis quality.

## Implementation Details

### Node Specification
- **Type**: `recall`
- **Logic**: 
    - Generate a metadata-only manifest of discovery steps.
    - Loop up to 8 steps using a local agent loop.
    - Tool `fetch_details(node_id, step_index)` retrieves full raw outputs.
    - Final synthesis uses GBNF-constrained grammar to ensure structured output (Tier 1 resolution).

### Executor Integration
Added `RunRecall` to the `ExecutionEngine` in `internal/executor/recall.go`. The handler in `executor.go` manages the identification of upstream probe nodes and pipes their discovery history into the recall agent.

## Consequences

### Positive
- **Quality**: Significantly higher synthesis quality by offloading cognitive load from the explorer model to a synthesizer model.
- **Efficiency**: Reduces the need for "Corrective Spawning" in the ReadyQueue, as the Recall node produces higher-confidence data initially.
- **Observability**: Discovery manifests provide a clear audit trail of what the system "remembers" before it synthesizes.

### Negative
- **Latency**: Adds one additional node execution per probe (approx. 10-30s local inference time).
- **Complexity**: Adds a new first-class node type to the compiler and executor layers.

## References
- ADR-0019: Probe Node and Thought Chain Execution
- ADR-0024: Edge Thought and Activation Threshold
- [benchmark_analysis_results_docgen_2.md](file:///Users/jp/.gemini/antigravity-ide/brain/964f079a-5770-40e4-805b-4fc9f56fbb08/benchmark_analysis_results_docgen_2.md)
