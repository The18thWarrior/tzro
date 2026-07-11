# Single-Slot MCTS Evaluation

Multi-branch Edge Thought evaluation (MCTS) must operate within the existing single-slot llama-server sidecar (`--parallel 1`) and must not increase the minimum hardware specification. This constrains the evaluation to a two-tier design: a single inference call generates K ranked candidates as a JSON array, then a zero-inference heuristic Value Function validates and re-ranks without additional model calls. Full rollout evaluation (Tier 2, with real tool execution) is reserved for planner-designated critical nodes only.

## Status

Accepted.

## Considered Options

- **Multi-slot parallelism (`--parallel K`).** Rejected — each additional slot duplicates the KV cache in VRAM. With a 32K context window and Q4_0 KV quantization on a 4B model, each slot costs ~2-4GB. Raising `--parallel` from 1 to 3 would increase minimum VRAM by 4-8GB, violating the hardware floor policy. Future KV cache compression advances (e.g., next-generation TurboQuant) may revisit this.

- **`n=K` batch completions in a single slot.** Rejected — with `--parallel 1`, the `n` parameter serializes completions against a single prefill. No latency benefit over sequential calls, and the API contract is unreliable across llama-server versions.

- **Cloud Model for MCTS evaluation.** Rejected — MCTS is part of the execution substrate, which must be local-default per ADR-0010. Cloud round-trips per rollout candidate would add 500ms-2s per candidate, defeating the latency budget.

## Consequences

- **Single inference call per decision point.** The Local Model generates all K candidates in one completion by outputting a JSON array of ranked action proposals. The prompt includes the Edge Thought context and asks for K alternative approaches with self-assessed scores.

- **Heuristic-first scoring.** The `HeuristicValueFunction` (key term coverage, output length, error markers, progress guard) runs with zero inference cost. The `LLMValueFunction` is only invoked for Tier 2 critical nodes where the planner sets `MCTSBranches > 0`.

- **MCTS prompt budget.** Multi-branch evaluation prompts are capped at `mctsMaxPromptTokens` (default 4096) to leave headroom in the 32K window for the KV cache prefix reuse (`--cache-reuse 2048`) to be effective across the generation call and subsequent node executions.
