# ADR-0046: Structured Content-Aware Compaction

## Context & Problem Statement

The existing compaction pipeline in tzro has four separate paths, each with different strategies and no content awareness:

1. **Probe `compactThoughtChain`** (probe.go): Dumps all step content — code, reasoning, tool outputs — into a single LLM prompt ("compress these exploration steps"). The local model treats everything as text and hallucinates when code content exceeds its understanding.

2. **Recall `compactRefinedContext`** (recall.go): Same monolithic approach for refined discovery facts.

3. **`TruncateToolOutput`** (truncation.go): Content-aware deterministic truncation — already classifies code/tabular/text. But only used for accumulated context assembly.

4. **`cache.Process`** (cache.go): 5-layer deterministic pipeline (ADR-0005) for JSON/HTML/Base64. Does not handle code structure.

### Concrete Failures

In benchmark docgen-9 and docgen-10, this caused:
- **Hallucinated types**: Probe read real source files, compaction compressed them into "the probe explored the package," synthesis generated fake type names (`InferenceEngine`, `ModelConfig` — none exist).
- **Lost function signatures**: The LLM compaction prompt discarded all structural code elements.
- **Massive latency**: The 1B router generated 4096 tokens of filler per Recall step because the unclassified content overwhelmed its context.

## Decision

We introduce a unified `internal/compactor/` package with a single content principle:

> **Code is NEVER LLM-compressed. LLM only compacts the model's own reasoning text.**

### Content Type Strategies

| Content Type | Strategy | LLM? |
|---|---|---|
| **Code** (source files) | Deterministic skeleton: preserve function signatures, type declarations, doc comments, const/var blocks. Replace function bodies with fingerprints: `// [body: N lines, calls: foo(), bar()]` | No |
| **Tabular** (CSV/TSV/JSON arrays) | Deterministic: header + N sample rows + summary line | No |
| **Text** (tool output prose) | Deterministic: middle-out truncation (head + tail, elide middle) | No |
| **Reasoning** (model's Thought field) | Chunked by sentence (~500 chars), each chunk compressed via router LLM: "Extract key conclusion" | Yes (Router 1B) |

### Budget Management

Two-stage cascade:
1. **Stage 1: Structured compaction** — Apply content-aware strategies to all segments. If within budget, done.
2. **Stage 2: Oldest-first triage** — Drop tool outputs from oldest steps first; preserve most recent N steps intact.

### Segmentation

Mixed content (markdown with fenced code blocks) is split at triple-backtick boundaries. Pure files use heuristic classification. This ensures code blocks within markdown documentation are skeleton-preserved, not middle-out truncated.

## Call Site Integration

| Call Site | Before | After |
|---|---|---|
| Probe thought chain | `engine.Infer("compress steps...")` | `compactor.CompactSteps(steps, RouterEngine)` |
| Recall refined context | `engine.Infer("compress facts...")` | `compactor.CompactFacts(facts, RouterEngine)` |
| Accumulated context | `TruncateToolOutput(output, budget)` | `compactor.CompactContent(output, budget)` |
| Synthesis context | `TruncateSynthesisContext(steps)` | Wrapper → `compactor.CompactSteps(steps, nil)` |

## Consequences

### Positive
- Function signatures survive compaction, eliminating hallucinated types in synthesis
- Content-type routing ensures the right compression strategy per segment
- Single module serves all compaction call sites (probe, recall, edge thoughts, synthesis)
- Router LLM only handles reasoning text (short, cheap, fast)
- Deterministic code handling means compaction output is reproducible

### Negative
- Skeleton extraction is heuristic (regex-based, not AST), may miss edge cases in unusual code
- Sentence chunking for reasoning text is approximate (splits on `. ` delimiter)
- All compaction call sites now depend on the `compactor` package

### Risks
- If the content classifier misidentifies code as text, function signatures would be middle-out truncated instead of skeleton-preserved. Mitigated by the conservative classifier (4+ code indicators required).

## Related
- **ADR-0005**: 5-Layer Context Compaction & Disk-Backed JQ Cache (predecessor — handles JSON/HTML, this ADR handles code/reasoning)
- **ADR-0019**: Probe Node and Thought Chain Execution (defines the compaction window)
- **ADR-0038**: Map-Reduce Recall (defines refined context compaction)
