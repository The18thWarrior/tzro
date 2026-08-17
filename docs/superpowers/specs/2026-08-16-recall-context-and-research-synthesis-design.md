# Recall Context Safety and GBNF Markdown Research Synthesis Design

**Date:** 2026-08-16  
**Status:** Approved  
**Target:** Benchmark Run 35 Failure Modes Remediation  

---

## 1. Executive Summary & Problem Statement

Benchmark Run 35 demonstrated strong performance across Code Generation (average QS: 4.14) and Data Analysis (average QS: 3.45), but exposed two critical execution failure modes in Codebase Exploration and Web Research:

1. **Recall Context Overflow (`comprehensive_readme` — QS: 0.00 / Error)**:
   - **Failure**: Multi-probe exploration tasks accumulate massive uncompacted `state.RawOutput` strings across upstream probes. In `internal/executor/recall.go`, the manifest builder appended full raw outputs, expanding the prompt to **98,250 tokens** and causing `llama-server` to return `HTTP 400 Bad Request: request exceeds the available context size (32768 tokens)`.
   - **Root Cause**: Lack of dynamic semantic pruning and token budget constraints on upstream `RawOutput` within the Recall Node discovery manifest.

2. **Web Research Synthesis Deficiencies (`compare_llm_frameworks` [2.00], `security_advisory_lookup` [1.50], `market_analysis_local_ai` [2.50])**:
   - **Failure**: Web research outputs were penalised by evaluation rubrics for missing Markdown comparison tables, lacking verbatim source URLs, and omitting concrete metrics/CVSS details.
   - **Root Cause**: Probes executing web tasks were configured with the generic codebase exploration persona, tool outputs from `web_search`/`web_browse` were truncated in Edge Entries before reaching synthesis, and free-form synthesis prompts did not enforce structured table and citation invariants.

This design introduces a two-part architectural improvement:
- **Semantic Chunking + Cosine Similarity + KNN / Neighbor Expansion** for upstream `RawOutput` in Recall Nodes with a pre-flight token clamp.
- **Direct Markdown GBNF Grammars + High-Fidelity Web Evidence Buffers** for Research Probes.

---

## 2. Component Architecture

```
                               ┌──────────────────────────────────────────────┐
                               │             UPSTREAM PROBE OUTPUTS           │
                               └──────────────────────┬───────────────────────┘
                                                      │
                       ┌──────────────────────────────┴──────────────────────────────┐
                       ▼                                                             ▼
         ┌───────────────────────────┐                                 ┌───────────────────────────┐
         │      CODE EXPLORATION     │                                 │       WEB RESEARCH        │
         └─────────────┬─────────────┘                                 └─────────────┬─────────────┘
                       │                                                             │
                       ▼                                                             ▼
         ┌───────────────────────────┐                                 ┌───────────────────────────┐
         │ Semantic Chunking & KNN   │                                 │ High-Fidelity SQLite      │
         │ - Chunks (600-800 chars)  │                                 │ Evidence Buffer           │
         │ - Goal Cosine Ranking     │                                 │ - Full URLs & Snippets    │
         │ - Adjacent Span Expansion │                                 │ - CVSS / Pricing / Specs  │
         └─────────────┬─────────────┘                                 └─────────────┬─────────────┘
                       │                                                             │
                       ▼                                                             ▼
         ┌───────────────────────────┐                                 ┌───────────────────────────┐
         │ Bounded Discovery         │                                 │ Direct Markdown GBNF      │
         │ Manifest (< 4K tokens)    │                                 │ Grammar Constraints       │
         └─────────────┬─────────────┘                                 │ - Enforced Tables         │
                       │                                               │ - Enforced Citations List │
                       ▼                                               └─────────────┬─────────────┘
         ┌───────────────────────────┐                                               │
         │ Hard Pre-Flight Token     │                                               ▼
         │ Clamp (< 20K tokens)      │                                 ┌───────────────────────────┐
         └─────────────┬─────────────┘                                 │ Clean, Structured         │
                       │                                               │ Markdown Synthesis Output │
                       ▼                                               └───────────────────────────┘
         ┌───────────────────────────┐
         │ Local Model Inference     │
         │ (HTTP 200 / Safe Context) │
         └───────────────────────────┘
```

---

## 3. Detailed Technical Specification

### 3.1 Recall Semantic Pruning (`internal/executor/recall.go` & `recall_pruner.go`)

When assembling the discovery manifest for upstream nodes in `RunRecall`:

1. **Threshold Check**: If an upstream node's `state.RawOutput` length is $\le 2,000$ characters, keep it as-is.
2. **Chunking**: If `len(RawOutput) > 2000`, divide the text into overlapping sequential chunks:
   - `ChunkSize`: 700 characters
   - `Overlap`: 100 characters
   - Each chunk retains its sequential index `i`.
3. **Cosine Scoring**:
   - Compute text embedding for the target `goal` using `internal/memory`.
   - Compute text embeddings for each chunk.
   - Calculate cosine similarity scores: $\text{sim}(c_i, \text{goal})$.
   - Select top $K$ chunks (default $K = 4$).
4. **Neighbor Expansion (KNN / Window Expansion)**:
   - For each selected chunk index $i$, include neighbor indices $i-1$ and $i+1$ (if within bounds).
   - Merge overlapping or adjacent ranges to preserve full paragraph coherence and code block continuity.
5. **Reassembly & Formatting**:
   - Sort selected chunks by original file order.
   - Separate discontiguous segments with `\n[... relevant section ...]\n`.
   - Limit total pruned output per upstream node to at most 4,000 characters.
6. **Hard Safety Context Clamp**:
   - Prior to calling `engine.Infer` in the Recall Node loop, estimate or tokenize the prompt.
   - If `promptTokens > 20,000`, truncate the baseline context from the middle while preserving goal and manifest headers, ensuring prompt tokens remain strictly within safe margins of `n_ctx` (32,768).

---

### 3.2 High-Fidelity Research Evidence Buffer (`internal/executor/probe_synthesis.go`)

Similar to the SQL cache result handling in `isAnalyze`, web research probes require lossless preservation of search query hits:

1. **Detection**: Detect if `isResearch` is true (i.e. `hasWebToolsInAllowed(node.AllowedTools)` or `sourceHint == "web"`).
2. **Evidence Extraction**: Query `memory.DB.GetThoughtSteps(probeID)` for `web_search` and `web_browse` / `fetch_web_page` invocations.
3. **Structured Context Injection**:
   - Format discovered URLs, page titles, and search excerpts into a dedicated `## Verified Search Sources & Evidence` block:
     ```markdown
     ### Source 1: [Page Title](https://example.com/advisory)
     - Key Evidence: Vulnerability affects Go versions < 1.22.6; CVSS 7.5.
     ```
   - Inject into synthesis context with a dedicated budget of up to 12,288 characters, bypassing lossy edge-log truncation.

---

### 3.3 Raw Markdown GBNF Grammar Support (`internal/inference/local_model.go`)

Extend `CallLocalModel` to support raw GBNF grammar strings in addition to JSON schemas:

1. **Grammar Dispatch**:
   - In `local_model.go`, inspect `gbnfSchema`:
     - If `json.Unmarshal` succeeds: pass as `{"type": "json_object", "schema": ...}`.
     - If `strings.HasPrefix(strings.TrimSpace(gbnfSchema), "root ::=")` or contains GBNF rules: pass as `{"type": "grammar", "grammar": gbnfSchema}`.
2. **Research Markdown GBNF Grammar**:
   - Construct a research synthesis grammar that enforces:
     - Free-form `# Title` and `# Overview / Summary` paragraphs.
     - Free-form `## Detailed Analysis` sections with bullets and explanations.
     - Enforced `## Comparison Table` in valid Markdown table syntax (`| Col 1 | Col 2 |\n| --- | --- |\n| Val 1 | Val 2 |`).
     - Enforced `## Sources & Citations` with bulleted source URLs (`- [Title](URL): Description`).

#### GBNF Grammar Definition (`internal/executor/probe_research_grammar.go`):
```gbnf
root ::= overview-section analysis-section table-section sources-section

overview-section ::= "# " [^\n]+ "\n\n" paragraph "\n\n"

analysis-section ::= ("## " [^\n]+ "\n\n" (paragraph | list-item)+ "\n\n")+

table-section ::= "## Comparative Overview\n\n" table-header table-divider table-row+ "\n"
table-header  ::= "| " ([^|\n]+ " | ")+ "\n"
table-divider ::= "| " ("--- | ")+ "\n"
table-row     ::= "| " ([^|\n]+ " | ")+ "\n"

sources-section ::= "## Sources & Citations\n\n" source-item+
source-item     ::= "- " [^\n]+ "\n"

paragraph ::= [^\n]+ ("\n" [^\n]+)*
list-item ::= "- " [^\n]+ "\n"
```

---

## 4. Verification & Testing Plan

### 4.1 Unit Tests
1. **Semantic Pruner Unit Test (`internal/executor/recall_pruner_test.go`)**:
   - Verify that large mock `RawOutput` (>100KB) is chunked, ranked, neighbor-expanded, and compacted to $\le 4,000$ characters without panic or data corruption.
   - Verify that small `RawOutput` ($\le 2,000$ chars) is passed through untouched.
2. **Local Model GBNF Raw Grammar Test (`internal/inference/local_model_test.go`)**:
   - Verify that passing a string beginning with `root ::=` dispatches `{"type": "grammar", "grammar": ...}` to `llama-server`.
3. **Research Synthesis Integration Test (`internal/executor/probe_synthesis_test.go`)**:
   - Verify that a probe with `web_search` thought steps generates a `## Verified Search Sources & Evidence` block and dispatches the Markdown GBNF grammar.

### 4.2 Benchmark Verification
1. **`comprehensive_readme` Execution**:
   - Execute `tzro compare -t comprehensive_readme -c cooperative`.
   - Verify task completes with `HTTP 200`, `qualityScore >= 4.0`, and zero 400 Bad Request errors.
2. **Web Research Tasks Suite**:
   - Execute `compare_llm_frameworks`, `security_advisory_lookup`, `market_analysis_local_ai`.
   - Verify that each output contains a rendered Markdown table, clickable citation URLs, and satisfies the quality rubric.

---

## 5. Scope & Self-Review

- **Placeholder Scan**: No TBDs, TODOs, or unresolved requirements.
- **Internal Consistency**: Token budgets (4K per node manifest, 12K research evidence, 20K hard ceiling) fit within the 32,768 sidecar context size.
- **Isolation**: Changes are localized to `internal/executor/` (Recall manifest assembly, research synthesis) and `internal/inference/` (GBNF raw grammar request dispatch).
