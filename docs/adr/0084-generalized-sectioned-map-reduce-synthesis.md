# ADR-0084: Generalized Sectioned Map-Reduce Synthesis for DocGen and Research

## Context & Problem Statement

In small on-device LLMs (4B parameter class), generating comprehensive long-form technical documentation (e.g. `inference_module_docs`, repository READMEs, architecture blueprints) in a single-pass inference turn consistently fails:
1. **Generation Cap Truncation**: When prompt + output exhausts the default per-call generation cap (`max_tokens: 2048`, ADR-0043), `llama-server` ceases generation mid-sentence or mid-word without closing code blocks or finishing the required sections.
2. **Attention & Symbol Dilution**: Forcing a 4B model to document 11+ source files across 4 architectural layers in a single shot leads to omitted subsystems (such as the Support layer containing `ThermalState` and `TokenTracker`).
3. **Keyword Gate Brittleness**: While ADR-0082/0083 introduced Sectioned Map-Reduce for web research comparisons, its activation was hardcoded to a keyword regex (`compare`, `framework`, `whitepaper`), completely bypassing codebase documentation tasks (`docgen`). Furthermore, its outline decomposition was static and hardcoded for comparison matrices.

## Decision

We generalize the **Sectioned Map-Reduce Synthesis** architecture to cover both DocGen and Research workloads with dynamic, model-planned outlines:

1. **Semantic & Scope Activation**:
   - Sectioned Map-Reduce is automatically activated for terminal synthesis on all `docgen` and `research` tasks where the prompt or discovered context spans multi-entity, multi-file, or multi-layer requirements.
   - Pure code generation tasks (`tzro_code`) are explicitly exempted.

2. **Dynamic Synthesis Outline with Deterministic Safety Floor**:
   - The Local Model plans a 3–6 section outline (`title`, `sections[]: {heading, objective, is_terminal}`) via GBNF schema based on the actual goal and discovered evidence.
   - If the Local Model under-decomposes a large multi-file/multi-layer context ($>4,000$ characters or $>4$ distinct packages), a deterministic safety floor partitions the outline across discovered module/layer boundaries.

3. **Map Phase with KV Cache Prefix Reuse**:
   - Each section generator receives the complete `refinedContext` and AST Symbol Index in the static prompt prefix (maximizing llama.cpp KV cache reuse across sections $2 \dots N$) alongside the **Rolling Prefix Context** (lead sentences of prior sections) and the specific section objective.
   - Each section executes with a focused 1,000–1,200 token budget.

4. **Reduce Phase (Assembly & Verification)**:
   - Sections are normalized and stitched sequentially.
   - Individual section truncation guards verify that no section ended on unclosed blocks or truncated sentences (triggering localized section retries rather than full-document regeneration).
   - The AST Symbol Anchor Check ($\ge 80\%$ anchored symbols) validates code symbol coverage before final delivery.
   - Verified codebase files and source citations are deterministically appended to the footer.

## Consequences

### Positive
- **Zero Generation Cap Truncation**: No single generation call exceeds the 2,048 token ceiling.
- **Complete Layer & Symbol Coverage**: Dedicating explicit sections to subsystems ensures trailing components (`ThermalState`, `TokenTracker`) receive thorough, uncompressed documentation.
- **Adaptive Structuring**: Outline structure dynamically matches the problem domain (code modules for DocGen, biological/scientific/technical topics for Research).
- **Fast Section Execution**: Static context broadcasting leverages local KV cache reuse.

### Negative / Trade-offs
- Multiple local inference calls per document increase total wall-clock time by $\sim 2\text{--}3\times$ compared to a single-pass call, though overall quality and completeness scale significantly.
