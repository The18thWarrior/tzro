# ADR-0086: Repository Pre-Index, Dual-Plane Indexing, and Context Budget Packing

A persistent local workspace index in SQLite (`.tzro/index.db`) combining an AST **Code Plane** and a chunked **Document Plane** with hybrid retrieval (FTS5 BM25 + Embedding Sidecar Vector Cosine) and dynamic **Context Budget Packing**, bypassing multi-turn Probe Node exploration loops.

## Status

Accepted

## Context

Probe Nodes (ADR-0019) perform goal-directed repository exploration via bounded Thought Chains on the Local Model. While effective, multi-turn exploration loops incur significant wall-clock latency (8s–22s per probe) because ~95% of the time is spent on autoregressive token generation for routine directory traversal and file reading.

Two naive approaches were evaluated and found flawed:
1. **AST / Symbol-Only Index**: Extremely fast to parse (<150ms), but completely ignores non-code files (Markdown ADRs, PRDs, PDFs, Word docs, text specifications), leading to 0% recall on architectural or conceptual requirements.
2. **Global Vector Store for All Files**: Embedding every line of code across a large codebase takes several minutes, consumes excessive memory, and performs poorly on exact code symbol lookups compared to deterministic keyword/AST matchers. Furthermore, keyword search alone (BM25) fails on unstructured documentation when queries use synonyms not explicitly present in the text (vocabulary mismatch problem).

## Decision

Introduce the **Repository Pre-Index** (`.tzro/index.db`), adopting a **Dual-Plane Indexing** architecture and **Context Budget Packing** for Probe Nodes.

### 1. Dual-Plane Indexing Model

The indexer categorizes repository files into two distinct processing planes:

* **Code Plane (`.go`, `.ts`, `.py`, `.rs`, etc.)**:
  - Parsed via pure-Go tree-sitter (`gotreesitter`) into declarations (functions, structs, interfaces, methods) and call edges.
  - Stored in relational SQLite tables (`symbols`, `call_edges`).
  - Indexed via SQLite FTS5 for exact keyword/symbol search.
  - Top-level docstrings and package descriptions are vectorized via the **Embedding Sidecar** (All-MiniLM-L6-v2, ADR-0075); raw internal function bodies are excluded from vectorization to keep indexing fast (<1s).

* **Document Plane (`.md`, `.txt`, `.pdf`, `.docx`)**:
  - Markdown parsed into structural sections along `# H1`, `## H2`, `### H3` heading hierarchies.
  - Plain text, PDFs, and Word documents parsed into position-aware page/paragraph chunks (~250–500 words).
  - Cross-linked to Code Plane symbols via regex symbol extraction on backticked identifiers.
  - Dual-indexed via **both** SQLite FTS5 (BM25) and the **Embedding Sidecar** (vector embeddings).

### 2. Lifecycle & Invalidation

* **Eager Daemon Startup**: The `tzro` daemon builds or verifies the SQLite index on startup.
* **Incremental Invalidation**: File system events (`fsnotify`) trigger targeted `<10ms` delta updates on save/commit.

### 3. Hybrid Retrieval with Reciprocal Rank Fusion (RRF)

At runtime, Probe queries execute concurrent searches:
* **BM25 Keyword Search** via SQLite FTS5 (exact identifiers, symbol names, file paths).
* **Vector Cosine Similarity** via `sqlite-vec` / embedded vectors (conceptual matching, synonyms).
* Results are merged using Reciprocal Rank Fusion: $\text{RRF\_Score} = \frac{1}{60 + \text{Rank}_{\text{FTS}}} + \frac{1}{60 + \text{Rank}_{\text{Vec}}}$.

### 4. Context Budget Packer

Instead of a fixed Top-K limit, the engine uses a **Context Budget Packer**:
* Discards noise below a strict confidence floor (e.g. cosine $< 0.65$ or low RRF score).
* Partitions the local model's token window using a **Reserve Ratio** (default: 70% Context, 15% System/Goal Prompt, 15% Generation Room).
* Greedily packs high-ranking AST signatures, doc sections, and text chunks up to the token budget.
* Runs a single-shot Direct Synthesis pass on the packed buffer, dropping Probe latency from ~15s to <2.5s total.

### 5. Transparent Hybrid Fallback

If index retrieval confidence is below threshold (< 0.50) or targets unindexed ephemeral paths, the Probe Node transparently demotes to the multi-turn Thought Chain / Deterministic Walker (ADR-0019, ADR-0078).

## Consequences

### Positive
- **Dramatic Latency Reduction**: Typical Probe Node discovery drops from 8–22s down to <15ms retrieval + 2s single-shot synthesis.
- **Zero Vocabulary Mismatch**: Conceptual questions on PDFs and docs hit semantic vectors, while code queries retain 100% exact symbol precision.
- **High Information Density**: Context Budget Packer feeds up to 15–30 relevant chunks simultaneously instead of fragmented turn-by-turn reads.

### Negative / Neutral
- Startup indexing requires ~1–3s initial background compute per project.
- Requires `sqlite-vec` or in-memory vector cosine support alongside existing SQLite database files.
