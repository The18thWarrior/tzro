# List Node — Extraction Without Synthesis

The 4B Local Model reliably identifies relevant content (pointing at line ranges) but unreliably rewrites content (synthesis corrupts signatures, drops exports, confabulates details). For extraction tasks — listing exported symbols, cataloging API endpoints, indexing constants — the Probe Node's synthesis pipeline actively degrades output quality by rewriting verbatim source through Recall compaction and Thought Chain summarization.

We introduce a **List Node**: an extraction-only node type where the model returns GBNF-constrained `[[startLine, endLine], ...]` arrays per file, and the Go harness copies verbatim source lines. The model points; the harness copies. No synthesis, no Recall, no Semantic Validator.

## Considered Options

**Option 1: Improve Probe Node synthesis for extraction tasks.** Better prompting, larger step budgets, forced file reads. Rejected because the failure is structural — the synthesis pipeline *must* rewrite content through the 4B model's generation, and generation is where fidelity loss occurs. Forensic analysis across 3 benchmark runs showed the same Probe pipeline scoring 2/19 exports (v3, v5) vs 18/19 (v4) depending on non-deterministic file selection, with even the "good" run losing accuracy through synthesis corruption.

**Option 2: Use the Inventory Extractor (existing Phase within Probe).** The Inventory Extractor derives GBNF schemas and does Map-Reduce extraction. Rejected because it still synthesizes — the model generates field values in its own words rather than copying source verbatim. Also tightly coupled to the Probe Phase Runner lifecycle.

**Option 3 (chosen): New List Node type with line-range extraction.** The model's only job is identifying *which* lines are relevant via a GBNF-constrained integer array. The Go harness handles everything else deterministically: file reading, line extraction, overlap merging, OOB clamping, chunking, and output assembly with annotated dividers.

## Design Decisions

- **File discovery is deterministic**: Orient (list_dir on PreloadPaths) → Discover (RichScoreAndSelect) runs in Go with zero LLM calls. PreloadPaths are extracted by the Kahn Compiler at compilation time with parent-directory deduplication.
- **One inference call per file**: Each file gets its own GBNF-constrained extraction call. Files exceeding ~800 lines are chunked with 50-line overlaps and results merged with dedup.
- **Routing via Kahn Compiler**: The template classifier stays at 6 topology archetypes. The Kahn Compiler uses `IsExtractionGoal()` (embedding sidecar, no keyword heuristics) to swap the explore node from `type: "probe"` to `type: "list"` within the existing `probe-and-write` template.
- **No Recall, no Semantic Validator**: The Kahn Compiler skips Recall injection and Validator wrapping for List Nodes. Output flows directly to a Deterministic write node.
- **VTE scoped to Pre-Flight coverage only**: No cloud Verification Gate. The Pre-Flight coverage check detects missed items via key-term matching. On coverage miss, a re-extraction pass re-runs inference on files that returned empty arrays with the missing items named explicitly.
- **Annotated dividers**: Output snippets include `--- file: path/to/file.go lines: 20-35 ---` headers for machine-parseability and human readability.
- **Composable**: When post-processing is needed, the frontier planner (T2) composes List → Probe/Recall → Write. The List Node stays pure extraction.

## Consequences

- A new `ListStrategy` Node Strategy must be registered in the Strategy Registry.
- The Kahn Compiler's Recall injection and Validator wrapping rules must check node type and skip for `type: "list"`.
- `IsExtractionGoal()` uses the Embedding Sidecar with extraction prototype vectors — no keyword heuristics (Principle 1).
- The `IsInventoryGoal()` keyword shortcuts in `inventory_matrix.go` should be removed in a follow-up to align with Principle 1.
