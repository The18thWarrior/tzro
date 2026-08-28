# ADR-0082: Sectioned Map-Reduce Synthesis for Research & Comparative Workflows

## Status
Accepted

## Date
2026-08-17

## Context
When local models (4B–7B parameter workers) synthesize extensive research documents, comparison matrices, and architectural guides from high-density discovery context (EvidenceCards), monolithic single-pass generation exhibits severe failure modes:
1. **Token Exhaustion & Attention Fatigue**: Generating an introduction, deep architectural breakdowns, a full markdown comparison matrix, cost arbitrage calculations, and recommendations in a single 2,048-token context window leads to premature cutoffs, truncated tables, or omitted sections.
2. **Table Density Penalty**: Multi-column comparison tables require high token concentration per row. In a monolithic prompt, the model allocates tokens to introductory prose and runs out of budget before completing the table rows.
3. **All-or-Nothing Failure**: If any section suffers from quality degradation, the entire document fails the verification gate.

## Decision
We introduce **Sectioned Map-Reduce Synthesis** for research and comparative recall nodes:

1. **Section Decomposition (Map Phase)**:
   When a research goal requires comprehensive multi-dimensional analysis, the reduce phase decomposes synthesis into discrete, focused sub-goals:
   - **Section 1 (Core Architecture & Mechanics)**: In-depth technical explanation of concepts, patterns, and language SDKs.
   - **Section 2 (Comparative Analysis Matrix)**: Dedicated markdown table generation with verified metrics, dimensions, and inline citations.
   - **Section 3 (Cost Arbitrage & Hardware Economics)**: Quantitative pricing analysis, TCO estimates, and infrastructure tradeoffs.
   - **Section 4 (Decision Guide & Recommendations)**: Concrete guidance, workload-based criteria, and trade-offs.

2. **Isolated Generation Slots with Full Token Budgets**:
   Each section worker executes in an isolated inference context with a dedicated token budget (e.g. 1,000–1,500 tokens per section) grounded on the shared refined discovery context. The comparison matrix section spends 100% of its generation capacity solely on complete table structures.

3. **Deterministic Assembly (Reduce Phase)**:
   The assembler concatenates the completed sections under a unified document structure (`# Title`, `## Sections...`), injects the `## Verified Research Evidence & Sources` block with verified URL citations, and submits the composite artifact to the VTE Verification Gate.

4. **Zero Cloud Cost & Parallelism**:
   Sections can be dispatched concurrently across local inference sidecar slots, maintaining zero cloud token cost while accelerating total wall-clock synthesis time.

## Consequences

### Positive
- **Complete Comparison Tables**: Dedicated token budgets eliminate truncated rows and half-finished markdown tables.
- **Deeper Coverage**: Each section achieves deep, technical rigor rather than superficial single-paragraph summaries.
- **Section-Level Isolation**: If a single section is incomplete, only that section is retried without re-generating the entire document.

### Negative
- **Minor Context Repetition**: The shared refined context is evaluated per section across inference slots.
