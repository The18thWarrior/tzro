# Solution Design & Engineering Approaches: Tzro v2

This document codifies the core philosophies, architectural principles, solution design rules of thumb, and anti-patterns established in **Tzro v2 ("The Local Token Shield")**.

---

## 1. Core Philosophy

**Tzro v2** is built on the principle of **Zero-Bloat Context Optimization**: solving the quadratic token explosion and API rate limits of autonomous coding agents using lightweight, deterministic systems engineering (<50 MB RAM, zero Python/PyTorch dependencies).

Our solution hierarchy:
1. **Deterministic Systems Scaffolding**: Fast Go networking, Tree-sitter syntax parsing, embedded SQLite FTS5, and ripgrep scanning.
2. **KV-Cache Prefix Normalization**: Pinning invariant system prompts and tool schemas to guarantee >90% prompt cache read hits ($0.10 \times P_{\text{base}}$).
3. **AST Structural Skeletons**: Preserving module interfaces while hashing method bodies into cryptographic markers.
4. **On-Demand Context Expansion**: Non-destructive retrieval of exact method bodies via SQLite content hashes.

---

## 2. Fundamental Design Principles

### Principle 1: Zero ML Sidecars by Default (Eliminate PyTorch Bloat)
- **Anti-Pattern**: Running 4.8 GB PyTorch transformer models or multi-gigabyte GGUF LLMs in background sidecars just to compress logs or parse code.
- **Mandated Solution**:
  - Use compiled C Tree-sitter grammars and regex/tabular extractors in Go.
  - Keep idle memory under 15 MB RAM and peak memory under 50 MB RAM.
  - Zero cold starts (<10ms startup).

### Principle 2: Respect the 12.5x KV-Cache Economic Multiplier
- **Anti-Pattern**: Changing system prompt ordering, reordering tools, or inserting volatile timestamps into the beginning of chat messages across conversational turns.
- **The Economic Insight**:
  $$\text{Cache Miss Cost} = 1.25 \times P_{\text{base}}, \quad \text{Cache Read Cost} = 0.10 \times P_{\text{base}} \implies \text{Penalty} = 12.5\times$$
- **Mandated Solution**:
  - `pkg/kvlock` locks message prefixes byte-for-byte.
  - Volatile timestamps, session identifiers, and dynamic IDs are isolated to trailing messages.

### Principle 3: AST-Aware Structural Skeletons (Interface Preservation)
- **Anti-Pattern**: Arbitrarily truncating files at line 100 or using lossy generic summarization that hallucinates function parameters.
- **Mandated Solution**:
  - `pkg/ast` parses Concrete Syntax Trees (CSTs) across 10 languages.
  - Preserves imports, types, structs, interfaces, exported signatures, and docstrings.
  - Elides function bodies into SHA-256 hashes (`// [body elided: #hash]`) and stores full bodies in SQLite FTS5 for on-demand expansion.

### Principle 4: Deterministic Sub-Millisecond Discovery (`tzro probe`)
- **Anti-Pattern**: AI agents running 15 sequential `list_dir`, `grep_search`, and `view_file` calls across 15 conversational turns (burning 250,000+ tokens).
- **Mandated Solution**:
  - `tzro probe "<query>"` executes embedded ripgrep + Tree-sitter AST scope resolution on-device.
  - Returns exact line numbers and symbol signatures in <5ms (<500 tokens).

### Principle 5: Zero-Cloud DLP & Secret Redaction
- **Anti-Pattern**: Transmitting raw source files containing API keys, private tokens, passwords, and private IPs to commercial cloud LLMs.
- **Mandated Solution**:
  - `pkg/dlp` masks credentials and private IPs on-device before requests egress to cloud providers.
  - Maintains in-memory restoration mappings to rehydrate returned code edits locally.
