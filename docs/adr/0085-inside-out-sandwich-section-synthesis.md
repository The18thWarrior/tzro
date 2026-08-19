# ADR-0085: Inside-Out (Sandwich) Section Synthesis & 10K-Token Context Partitioning

## Context & Problem Statement

ADR-0084 introduced Generalized Sectioned Map-Reduce Synthesis for DocGen and Research workloads. While this avoided generation cap truncation (by generating sections in multiple passes), benchmark runs (`results-docgen-5`) revealed two key issues:

1. **Context Overload via Monolithic Broadcasting**:
   Instead of partitioning codebase context by section, `ExecuteDocGenSectionedSynthesis` broadcast the entire 32K-character `refinedCtx` (and all AST symbols) to *every* section call. For large modules (e.g. `internal/inference` with 11+ files, >150KB), this caused prompt context saturation, high latency, and RoPE attention degradation on 4B models.

2. **Sequential Out-of-Sync Bookends**:
   Generating Section 1 (*"Architecture Overview / Executive Summary"*) *before* body sections forced the model to invent or speculate on details before drafting them. This caused severe divergence between the overview and the subsequent body text.

3. **Goal Misalignment in Outline Planning**:
   The outline planning system prompt hardcoded an architectural whitepaper structure ("module architecture, core layers, key types, routing mechanics..."), forcing non-architectural goals (such as exhaustive function indexes or API references) into inappropriate narrative molds.

## Decision

We upgrade Sectioned Map-Reduce with an **Inside-Out (Sandwich) Execution Pipeline** and **Deterministic Context Partitioning**:

### 1. 10,000-Token Context Cap per Section
- Context selection per body section is capped at **10,000 tokens (~40,000 characters)**, providing ample room for multi-file code context while staying well below model context limits.
- Output generation per section is bounded at **1,200 tokens**.

### 2. Deterministic Semantic / Lexical Context Partitioning
- Discovered code files and AST symbols are partitioned per section via Go-native scoring (lexical token matching + BM25 / Cosine embeddings against `sec.Heading + " " + sec.Objective`).
- AST symbols with matching `FilePath` or referenced names are extracted per section.
- **Zero LLM String Routing**: The local model never attempts to output verbatim JSON arrays of file paths.

### 3. Inside-Out (Sandwich) Execution Flow
Document synthesis proceeds in 3 ordered phases:
1. **Body Sections (Pass 1)**: All body sections ($S_1 \dots S_{N-2}$) are synthesized first against their 10K-token partitioned contexts.
2. **Initial Section / Executive Overview (Pass 2)**: Section $S_0$ is synthesized consuming the **drafted body text** as authoritative context, guaranteeing 100% factual and structural alignment.
3. **Terminal Section / API Index (Pass 3)**: Section $S_{N-1}$ is synthesized with the verified AST symbol catalog and body references.
4. **Assembly (Pass 4)**: Sections are stitched in original outline order: `[S_0, S_1, ..., S_{N-1}]`.

### 4. Goal-Aware Outline Planning & Safety Floor
- Outline planning system prompts adapt dynamically to the user's explicit intent (e.g. function index vs. architecture blueprint).
- Dedicated safety floor branches support function/symbol indexes (Types $\to$ Functions $\to$ Methods $\to$ Reference Table).

## Consequences

### Positive
- **Grounded Introductions**: Overviews and Executive Summaries perfectly match the body content because they are written *after* the body is established.
- **High Signal-to-Noise**: Body sections receive up to 10K tokens of relevant code files rather than being diluted by irrelevant modules.
- **Robust 4B Operation**: 100% deterministic partitioning avoids LLM file path hallucination.
