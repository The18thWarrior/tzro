# ADR-0042: Map-Reduce Recall and Shallow Planning for Latency Reduction

## Status
Proposed

## Context
In the `docgen-3` benchmark run, we observed critical wall clock time inflation, with single tasks taking 10–13 minutes to complete. Analysis revealed two primary bottlenecks:
1.  **Planning Bloat**: The Strategic Planner (The Strategist) received a full AST map of the repository (types, signatures) in its system prompt, resulting in 45,000+ token prompts and a ~4-minute prefill penalty.
2.  **Recall Penalty**: The Recall Node used a one-shot synthesis approach, attempting to digest massive raw discovery histories (the "Discovery Sludge") in a single prompt, leading to high latency and cognitive overload.

## Decision
We will implement a two-pronged latency reduction strategy: **Shallow Planning** and **Map-Reduce Recall**.

### 1. Shallow Planning (The "Code-Blind" Strategist)
- **Scaffolding vs. Knowledge**: The Strategic Planner will no longer receive file-level signatures or type definitions.
- **Context Injection**: The `repoMap` in the planning prompt will be replaced by a **Shallow Directory Tree** (max depth 2, directory names only).
- **Probe-First Policy**: If the Strategist requires deeper codebase knowledge to plan, it **must** delegate that discovery to a **Probe Node**.

### 2. Map-Reduce Recall (The Multi-Pass Synthesizer)
- **Non-Destructive Reduction**: We will replace the one-shot Recall synthesis with a multi-pass approach that preserves the "purity" of raw discovery data in the `thought_chain` table while reducing the context window for synthesis.
- **Phase 1: Map/Filter**: The Recall Node scans metadata of upstream discovery steps to identify "Signal" (relevant files/content) vs "Noise" (empty listings, failed searches).
- **Phase 2: Refine/Extract**: Targeted extraction is performed on "Signal" chunks to pull specific facts (signatures, logs).
- **Phase 3: Synthesis**: A final aligned response is generated from the refined findings.

## Implementation Details

### Strategic Planner Update
Modify `internal/task/task.go` and `internal/compiler/ast_mapper.go` to support a `GenerateShallowMap` function that excludes signatures and limits directory depth.

### Recall Engine Update
Refactor `internal/executor/recall.go` to implement the multi-pass loop. The agent will use a "Refinement Pass" instruction to prune irrelevant steps from its context before the final synthesis.

## Consequences

### Positive
- **Latency**: Expected 60–80% reduction in wall clock time by minimizing prefill on local models.
- **Cost**: Significant token savings for Cloud Model planning.
- **Reliability**: Reduced "Attention Bias" in the planner leads to fewer hallucinated paths.

### Negative
- **Inference Count**: Adds 2–3 small inferences to the Recall phase. However, the total duration is lower due to smaller context windows.
- **Complexity**: Requires a stateful refinement loop in the Recall agent.

## References
- ADR-0037: Recall Node for Discovery-Synthesis Alignment
- ADR-0019: Probe Node and Thought Chain Execution
- [benchmark_analysis_results_docgen_3.md](file:///Users/jp/.gemini/antigravity-ide/brain/0f0dde47-2072-456c-91fc-51e99d3e3825/benchmark_analysis_results_docgen_3.md)
