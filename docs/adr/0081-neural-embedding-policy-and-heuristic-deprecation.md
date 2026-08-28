# ADR-0081: Neural Embedding Policy and Heuristic Deprecation

**Status**: Accepted  
**Date**: 2026-08-17  
**Deciders**: JP  
**Context**: Red-team research benchmark evaluations and anti-fragile execution quality

---

## Context

Across iterative red-team benchmark evaluations (notably within the `research`, `datanal`, and `docgen` suites), heuristic, regex-based, and hardcoded string-slicing mechanisms repeatedly introduced fragile failure modes:

1. **Catastrophic Query Mangling**: Heuristics such as 100-character string truncations, hardcoded keyword prefix strippers (`"Search for "`), and naive conjunction splitting on `" and "` broke multi-clause goals (e.g., transforming *"Search the web and browse at least 3 pages to gather detailed architectural insights on Temporal, Restate, and Inngest"* into a search for literal `"Search the web"`).
2. **Brittle Fact & Evidence Extraction**: Regex density scoring (`cveRe`, `metricRe`, `keywordRe`) missed critical tabular comparisons, structured benchmarks, pricing models, and domain entities that did not match hardcoded regular expressions.
3. **Rigid Routing & Deduplication**: Hand-tuned string checks produced false-positive rejections (e.g., mistaking valid markdown comparison tables like `| Yes | Yes |` for degenerate text repetition).

Since the introduction of the on-device **Neural Embedding Sidecar** (ADR-0075, All-MiniLM-L6-v2, ~23MB GGUF, ~100MB RAM), tzro has access to microsecond-latency, high-dimensional vector embeddings with zero cloud API token cost.

---

## Decision

### 1. Deprecation of Regex and String Heuristics for Semantic Tasks
Prohibit the use of regex parsers, string slicing, and hardcoded keyword lists for semantic routing, intent extraction, query segmentation, and evidence scoring across all execution nodes:
- **Prohibited**: Regex entity density counters, substring length clipping for query formulation, and hardcoded keyword classification trees.
- **Allowed**: Standard syntax-level structural normalization (e.g., markdown table line recognition, AST token parsing).

### 2. Mandatory Neural Embedding & $k$-NN Retrieval
Mandate using the **Neural Embedding Sidecar** (`inference.GlobalEmbeddingSidecar` / `embeddings.DefaultEngine`) with vector cosine similarity and $k$-Nearest Neighbors ($k$-NN) ranking across all core execution pipelines:
- **Evidence Extraction (`extractEvidenceCardFromPage`)**: Chunk browsed content into semantic paragraphs and structured table blocks, batch-embed chunks via `EmbedBatch`, compute cosine similarity against the goal vector $v_{\text{goal}}$, and select the top-$k$ ($k=6\text{--}8$) nearest neighbors.
- **Query Decomposition (`extractSearchQueryVariantsFromGoal`)**: Segment compound goals into candidate clauses, batch-embed with meta-instruction negative prototypes, and rank informative domain clauses using vector cosine similarity.
- **Intent & Column Selection (`ExtractQueryIntent`, `ResolveSelectColumns`)**: Use vector cosine distances against candidate schemas and operations rather than heuristic rule sets.

### 3. Graceful Mathematical Fallback Hierarchy
When the Neural Embedding Sidecar is starting, adopted, or temporarily unavailable:
1. **Primary**: On-device Neural Embedding Sidecar (`All-MiniLM-L6-v2`) via HTTP vector endpoints with hot/cold SQLite cache.
2. **Secondary Fallback**: Pure Go vector space (`PureGoEmbeddingEngine` / `embeddings.CosineSimilarity`) using bag-of-words cosine similarity.
3. **Strict Policy**: Never fall back to ad-hoc regex heuristics or arbitrary string-length truncation.

---

## Consequences

- **Positive**: Eliminates brittle query truncation and keyword mismatches across varied prompt phrasings.
- **Positive**: Drastically improves factual density and extraction of structured markdown tables and comparative metrics from web sources.
- **Positive**: Zero cloud token expenditure — embedding operations run completely on-device in <10ms.
- **Trade-off**: Requires the neural embedding sidecar (`llama-server` on `.llama-embed.port`) to be running or auto-downloaded on boot.

---

## Related

- ADR-0074: Structured Query Composition
- ADR-0075: Neural Embedding Sidecar for Schema-Aware Column Selection
- ADR-0078: Model/Scaffolding Split — Deterministic Walkers
- ADR-0080: High-Density Research Pipeline and Structured Synthesis
