# ADR-0092: Lossless Context Prefill Optimization and Prefix-Slot Architecture

Status: accepted.

## Context

During task execution on local models (1B–7B parameter class), prompt evaluation ("prefill") dominates time-to-first-token (TTFT) and memory bandwidth on Apple Silicon / Metal architectures. In repetitive multi-pass operations—such as List Node file extraction (evaluating 10–50 candidate files sequentially), Sectioned Map-Reduce Synthesis (synthesizing 3–6 outline sections), and Codegen Edit Loops (3–15 hunk iterations)—the local engine repeatedly evaluates large prompt contexts.

Profiling revealed that:
1. **Dynamic Prompt Header Mutation**: Formulations like `fmt.Sprintf("...synthesizing Section %d...", sectionIndex)` in `section_synthesis.go` and ad-hoc concatenation in `list_extract.go` change prompt prefixes from token 0, invalidating `llama-server`'s `--cache-reuse` KV prefix matching on every single call.
2. **Context Path Redundancy**: Multi-file contexts and AST evidence tables repeat identical file paths and import namespaces dozens of times, consuming thousands of redundant prompt tokens.
3. **Monolithic Sibling Context**: Passing unpruned 500+ line sibling files into codegen edit contexts forces the local model to prefill large method bodies that are irrelevant to the target type interface.
4. **Uniform KV Quantization Over-Allocation**: Setting `q8_0` uniformly across both Worker (4B/7B) and Router (1B) sidecars wastes unified memory bandwidth for the router's lightweight 1-shot GBNF classification duties.

## Design Decisions

1. **Static Prefix Slotting (Invariant 3-Turn Base + Dynamic Tail)**:
   - All multi-pass operations standardize on a 4-turn message structure:
     - **Turn 1 (`system`)**: Static invariant base prompt template.
     - **Turn 2 (`user`)**: The Immutable Goal Prompt (ADR-0088) and static repository metadata.
     - **Turn 3 (`assistant`)**: Synthetic turn boundary (`"Ready for candidate file/section context."`).
     - **Turn 4 (`user`)**: The volatile dynamic tail (candidate file content, section slice, or patch hunk).
   - Guarantees that `llama-server`'s `--cache-reuse` reuses KV state through Turns 1–3, computing only the dynamic tail on passes 2 through $N$.

2. **Symbolic In-Context Dictionary Encoding (Meta-Tokens)**:
   - A deterministic Go harness pass in `internal/compactor` discovers frequent long substrings ($\ge 18$ chars, frequency $\ge 3$) in contexts $>4\text{KB}$.
   - Replaces path prefixes and import namespaces with compact meta-tokens (`§0`, `§1`) and prepends a compact dictionary legend.
   - Decodes generated output deterministically back to full strings in Go before reaching Section Assembly, Pre-Flight Validation, or Tool Sinks.

3. **Context-Role Aware 2-Tier AST Stubbing & Section-Scoped $k$-NN Ceiling**:
   - In Codegen (`internal/codegen`), sibling reference files have method/function bodies replaced with `{ /* ... */ }` stubs via tree-sitter AST parsing (`internal/symbols`), preserving all type signatures and contracts. The active target file retains full content for hunk matching.
   - In Sectioned Synthesis, enforce a strict sparse context ceiling ($K=4$ embedding snippets, cosine similarity $\ge 0.55$) per section.

4. **Role-Differentiated KV Cache Quantization**:
   - The 1B Router Sidecar (`m.Role == "router"`) always launches with `--cache-type-k q4_0 --cache-type-v q4_0`.
   - The Worker Sidecar (`m.Role == "worker"`) uses `q8_0` in local mode and `q4_0` in cooperative mode.

## Consequences

- List Node file evaluations (files 2–50) and Sectioned Synthesis passes (sections 2–8) achieve near-zero prefill computation on prompt headers.
- Multi-file prompt token payloads shrink by 40%–60% without loss of grounding or semantic distortion.
- Sibling context overhead in codegen tasks drops from ~12K tokens to ~1.2K tokens.
- Metal memory bus contention on Apple Silicon is reduced during concurrent router/worker execution.
