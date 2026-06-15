# Segmented Multi-Turn Prompt for KV Cache Sharing

The `StructuredInferenceRequest` interface changes from flat `SystemPrompt`/`UserPrompt` strings to a `Messages []InferenceMessage` slice, enabling arbitrary n-message conversations. GBNF bridge/exec nodes use a 4-message segmented structure that maximizes KV cache prefix sharing across parallel nodes at the same Kahn topological level:

1. **Turn 1 (system):** Static base instruction — cached once per task
2. **Turn 2 (user):** Accumulated context from prior steps — cached once per Kahn level
3. **Turn 3 (assistant):** Synthetic acknowledgment — extends the shared prefix
4. **Turn 4 (user):** Per-node schema + instruction — only segment requiring fresh KV computation

With `--cache-reuse` raised from 256 to 2048 tokens, llama-server's prefix matching automatically reuses KV state through turns 1-3 for parallel nodes, eliminating ~85% of redundant prefill work on multi-node levels. The same 4-message structure is sent to the Cloud Model on surgical escalation to keep the data path consistent for Corrective Micro-Skill diff extraction (ADR-0020).

A `NewSimpleRequest(system, user, schema)` helper preserves the 2-message pattern for classification, chat, and other callers that don't need segmented prompts.

## Considered Options

- **Adding `AccumulatedContext` field to the existing struct:** Backwards-compatible but locks the interface to a specific prompt layout, preventing future execution modes from composing arbitrary message sequences.
- **Slot save/restore at Kahn level boundaries:** Uses the existing preemption API but serializes parallel execution (single slot) unless `--parallel` is increased, adding memory and complexity.
