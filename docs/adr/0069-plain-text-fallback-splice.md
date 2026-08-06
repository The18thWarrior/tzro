# Promote Plain-Text Fallback to Splice-Eligible

The Proactive Binding Splice (ADR-0030) previously excluded `plain_text_fallback` bindings from splicing because the tier was considered low-confidence — "the entire output is the value" felt heuristic. This caused content fidelity loss in probe→write pipelines: the Semantic Validator received multi-KB probe output in its context and was asked to extract it as a `content` parameter, but LLMs naturally summarize large content rather than passing it through verbatim. In Benchmark Run 17, this caused 3 of 6 failures (`inference_module_docs`, `comprehensive_readme`, `lead_target_account_analysis`).

We promote `plain_text_fallback` to splice-eligible when the source node is a Probe Node, Recall Node, or synthesis node (the same set checked by `isPlainTextNodeType()`). The splice bypasses validator inference for these bindings — the full resolved text is injected directly into the tool arguments. The validator still extracts other parameters (`path`, `dbId`, etc.) normally.

## Considered Options

- **Action allowlist (`write_file` only)**: Initially considered, then rejected when `lead_target_account_analysis` showed the same failure on `local_db_insert`. Allowlists grow over time and break silently when new tools are added.
- **Size-guarded splice**: Rejected — `write_file` has no length limit, and the splice is a pass-through, not an amplifier. The worst case of splicing (too much data) is recoverable; the worst case of not splicing (no data) is unrecoverable.

## Consequences

- Non-probe/recall/synthesis nodes are unaffected — their outputs resolve via higher tiers (`recursive_key`, `fuzzy_key`) and never hit `plain_text_fallback`.
- The source-node-type check (`isPlainTextNodeType()`) is the safety scope. If a new node type is added that produces structured JSON but is incorrectly classified as plain-text, splicing could inject raw JSON as a content blob. Mitigated by the existing node type registry.
