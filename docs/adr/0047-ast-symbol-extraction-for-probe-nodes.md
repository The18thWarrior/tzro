# AST-Based Symbol Extraction for Probe Nodes

The docgen benchmark (2026-07-15) revealed that cooperative mode produces 67% hallucinated type names in documentation tasks because the local 4B model lacks sufficient context to identify real public symbols. The LLM judge scored these outputs 4.25/5.0 on average; manual audit scored them 2.65/5.0 — a 38% inflation rate caused by the judge rewarding structural formatting without verifying factual accuracy.

We introduce a deterministic **Symbol Extractor** that runs a pure-Go tree-sitter AST parse (`odvcencio/gotreesitter`) on every file read during a Probe Node's Thought Chain, emitting `{name, kind, signature, file, line}` tuples into a side-channel **Symbol Index** in SQLite. The Recall Node receives this index as the authoritative symbol inventory for synthesis, and a post-synthesis **Symbol Anchor Check** flags any unanchored identifiers for targeted correction.

## Considered Options

- **Regex heuristics per language**: Zero dependencies, but misses multi-line signatures and language-specific visibility rules (C++ namespaces, Rust `pub`, C# access modifiers). Insufficient for the 10+ language target.
- **CGO-based tree-sitter** (`smacker/go-tree-sitter`): Mature but requires a C toolchain, breaks `go install` for downstream users, and complicates cross-compilation.
- **LLM-based extraction with GBNF constraints**: Handles any language but burns local inference tokens and introduces the exact hallucination risk we're trying to eliminate.
- **Pure-Go tree-sitter** (`odvcencio/gotreesitter`): No CGO, 206 grammars, cross-compiles to any GOOS/GOARCH including wasip1. Accepted.

## Consequences

- The existing heuristic **Code Skeleton** remains unchanged as the fast path for general compaction. The Symbol Extractor runs *alongside* it, not as a replacement — the Extractor produces an index, the Skeleton produces compacted code.
- The `_Avoid_: AST extraction` label on Code Skeleton is narrowed: AST-based compaction is still avoided (heuristic is sufficient for body-stripping); AST-based symbol extraction is a separate concern.
- ~12 grammar blobs are embedded in the tzro binary (Go, TypeScript, JavaScript, Python, Java, Rust, Dart, C++, C#, Kotlin, Swift, Ruby). Additional grammars are lazy-loaded from `~/.tzro/grammars/` on first use. Estimated binary size impact: 3-5MB.
- A new SQLite table stores the Symbol Index per Probe Node execution. The Probe's context window carries only a count ("N symbols extracted"), not the full index.
- The Symbol Anchor Check adds a lightweight in-memory diff step after Recall synthesis. Only triggers a ~500-token correction pass when >20% of referenced symbols are unanchored (excluding dot-qualified external references like `context.Context`).
