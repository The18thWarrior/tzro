# ADR-0082: Deterministic Harness Scaffolding for Small-Model Parity

**Status**: Accepted  
**Date**: 2026-08-18  
**Deciders**: JP  
**Context**: Benchmark Run 38 failure mode diagnosis and 4B local model quality optimization

---

## Context

Benchmark Run 38 evaluated the cooperative execution engine against 25 tasks across four distinct operational domains (Codegen, Docgen/Probes, Data Analysis, and Web Research) using a 35B MoE remote inference backend. 

Analysis of the results and comparison against prior 4B model runs (Runs 35–37) revealed a critical architectural insight:
1. **Model Parameter Scaling Bottleneck**: Moving from 4B to 35B improved raw code generation (4.26 / 5.0) and high-volume probe traversal stability, but overall quality plateaued at 3.57 / 5.0.
2. **Harness As The Quality Ceiling**: The persistent failure modes across Data Analysis (2.80) and Web Research (2.82) were not caused by model reasoning capacity, but by structural deficiencies in the Go execution harness:
   - *Data Analysis*: Arithmetic in-context errors, missing ratio calculations (`% of total`), unhandled NULL groupings, and single-aggregate query limitations.
   - *Web Research*: Lack of inline per-claim attribution (`[N]`), unverified numerical assertions, and missing bibliography anchoring.
   - *Exploration*: Indiscriminate broad directory crawls wasting token budget on surgical/targeted tasks.
   - *Codegen*: Missing import declarations (e.g. `import "encoding/json"`) causing syntax gate failures despite sound algorithm structure.

To elevate on-device 4B models to $\ge 4.25$ average quality across all domains without paying cloud inference costs, the execution harness must substitute deterministic Go scaffolding for small-model cognitive weaknesses.

---

## Decision

### 1. Relevance-Scored Exploration Queue (Top-$K$ Neural Pruning)
For **Probe Nodes** running in the **Phase Runner**, the `orient` phase generates candidate file paths from directory traversal and symbol search, which are then scored against the probe goal embedding using the **Embedding Sidecar** (ADR-0081).
- Discovered paths are ranked by vector cosine similarity and pruned to top-$K$ ($K=3\text{--}5$ for targeted tasks, $K=15$ for wide scans).
- Prevents full-repository crawls on surgical search tasks while preserving depth for architecture overviews.

### 2. Neural Semantic QueryIntent for Deterministic Data Analysis
Upgrade the **Deterministic Query Path** ([deterministic_query.go](file:///Users/jp/Desktop/Repos/tzro/internal/executor/deterministic_query.go)) in **Analyze Nodes**:
- **Semantic Intent Matching**: Use vector similarity against operation prototype embeddings (filters, group-by, compound aggregates, ratio/percentage window functions, and order signals) rather than brittle regex strings.
- **Compound Aggregations**: Support multi-aggregate operations in a single query (e.g. `COUNT(*) AS lead_count, COUNT(DISTINCT company) AS company_count`).
- **Deterministic Ratio Calculations**: Compile requests for percentages and proportions into SQLite window functions (`COUNT(*) * 100.0 / SUM(COUNT(*)) OVER() AS percentage`).
- **NULL-Safe Grouping**: Automatically apply `COALESCE(col, 'Unspecified')` on categorical grouping keys.

### 3. Numbered Citation Preamble & Pre-Flight Citation Assertion
In **Research Nodes**:
- **Citation Preamble**: The harness deterministically formats visited **Evidence Cards** into an indexed bibliography (`[1] URL - Title`, `[2] URL - Title`) injected into the synthesis prompt.
- **Prompt Contract**: The local model is constrained to cite specific numbered tags `[N]` for all factual and quantitative statements, writing `"Not reported in sources"` when data is absent.
- **Stage 2 Citation Assertion**: A deterministic check in **Pre-Flight Validation** validates that all `[N]` citations resolve to valid crawled evidence before the synthesis is accepted.

### 4. Language-Agnostic Tree-Sitter AST Import Validator & Pluggable Linter Interface
In **Codegen** tasks:
- **In-Memory AST Namespace Check**: Use tree-sitter across Go, TypeScript, Python, and Rust to inspect member call roots (e.g. `json.Marshal`, `path.join`, `os.environ`) against declared imports.
- **1-Turn Reflection Repair**: If an unimported namespace is referenced, trigger a 1-turn targeted local self-repair prompt before committing the file.
- **Pluggable `LanguageLinter` Interface**: Provide an extensible interface in `internal/codegen` allowing custom language compilers, linters, and typecheckers to register into the compilation gate.

---

## Consequences

- **Positive**: Eliminates in-context arithmetic errors by executing all data aggregations and percentage ratios in SQLite.
- **Positive**: Guarantees source attribution and prevents hallucinated metrics on research deliverables.
- **Positive**: Prevents directory scan thrashing on targeted exploration tasks.
- **Positive**: Fixes trivial missing-import syntax failures in code generation across all supported languages.
- **Positive**: Bridges the quality gap, enabling 4B on-device models to achieve 4.25+ benchmark parity with frontier cloud models at zero cloud token cost.
- **Trade-off**: Requires running the local Neural Embedding Sidecar for semantic intent and top-$K$ relevance ranking.

---

## Related

- ADR-0050: Analyze Node
- ADR-0074: Structured Query Composition
- ADR-0075: Neural Embedding Sidecar
- ADR-0076: Deterministic Query Path
- ADR-0078: Model/Scaffolding Split — Deterministic Walkers
- ADR-0080: High-Density Research Pipeline and Structured Synthesis
- ADR-0081: Neural Embedding Policy and Heuristic Deprecation
