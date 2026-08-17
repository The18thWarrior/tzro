# ADR-0080: High-Density Research Pipeline and Structured Synthesis

**Status**: Accepted  
**Date**: 2026-08-17  
**Deciders**: JP  
**Context**: Benchmark runs 30–36 failure analysis & Research task quality optimization

---

## Context

Across benchmark runs 30–36, the Research task category consistently plateaued at an average quality score of ~2.50 / 5.00 (`security_advisory_lookup`: 2.00, `technical_deep_dive_gguf`: 2.30, `compare_llm_frameworks`: 2.50, `market_analysis_local_ai`: 2.50).

Detailed log analysis and LLM-as-judge evaluations diagnosed four interconnected failure mechanisms:

1. **Shallow EvidenceCard Extraction**: In `extractEvidenceCardFromPage`, naive line-prefix filtering (`- `, `* `) and hardcoded keyword checks extracted only 0–1 generic tagline per scraped web page, starving downstream synthesis of concrete technical facts, CVE IDs, versions, and benchmark metrics.
2. **Local 4B Repetition Degeneration**: The local 4B model defaulted to unpenalized inference during long synthesis passes, entering n-gram attractor loops (e.g. repeating `- **Severity:** High (7.5) -` or `Ollama (Yes), llama.cpp (Yes)` 4+ times) that failed the Pre-Flight Structural Pre-Check (ADR-0060, ADR-0071).
3. **Missing Citations & Hallucinated Specifics**: Lacking factual evidence in the prompt, the local model hallucinated future CVEs and omitted inline citations (`[Source](URL)`), failing LLM-as-judge verification rubrics.
4. **Shallow Search Query Decomposition**: Initial search queries remained high-level without targeting second-order technical sources (e.g. changelogs, CVE feeds, benchmark reports).

---

## Decision

### 1. Deterministic High-Density Evidence Extraction (Zero LLM Inference)
Upgrade `extractEvidenceCardFromPage` to deterministically parse markdown headers, key-value definitions (`**Key:** Value`), table rows, and entity-dense sentences (scoring numerical, version, and technical entity density). 
- Maintains strict compliance with ADR-0078: zero step-level LLM calls during the `deep_read` phase.
- Extracts up to 6 high-signal facts per visited URL.

### 2. Standardized DRY Sampling & Presence Penalty on Synthesis Surfaces
Inject `DRYSamplingConfig{Multiplier: 0.8, Base: 1.75, AllowedLength: 2}` and `PresencePenaltyKey: 0.2` on all **Research Node** and **Recall Node** synthesis and reduction passes in `internal/executor/recall.go` and `internal/executor/research_phases.go`.
- Dynamically penalizes sequence-level repetitions without degrading markdown table structure or code formatting.

### 3. GBNF-Enforced Structural Markdown Grammar for Research Nodes
Attach `buildResearchMarkdownGrammar` to the `synthesize` phase of `Research Node` pipelines.
- Enforces structural sections: `# Title`, `## Analysis`, `## Comparative Overview` (Markdown table), and `## Sources & Citations` (bulleted list of source URLs).
- Leaves paragraph and list item generation flexible within section boundaries.

### 4. Two-Stage Dynamic Search Refinement
Extend the Research Node PhaseRunner to support dynamic query refinement:
- Dispatches initial macro search queries.
- Inspects returned snippet metadata to spawn targeted secondary queries (`site:`, specific entity filters) before draining the browse queue.

---

## Consequences

- **Positive**: Eliminates 4B repetition degeneration loops on research synthesis passes.
- **Positive**: Guarantees structured comparison tables and verified citation lists across all research deliverables.
- **Positive**: Drastically increases factual information density without adding GPU/LLM latency or violating ADR-0078.
- **Trade-off**: Requires GBNF grammar compilation on the local sidecar for research synthesis phases.

---

## Related

- ADR-0048: Plan Template Registry
- ADR-0060: Generation Guard on Inference Backend
- ADR-0067: Verified Task Execution
- ADR-0071: Pre-Flight Validation and 4B Failure Mode Mitigations
- ADR-0078: Model/Scaffolding Split — Deterministic Walkers
