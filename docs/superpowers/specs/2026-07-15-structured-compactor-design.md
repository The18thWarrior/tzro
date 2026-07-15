# Structured Compactor — Content-Aware Compaction Module

**Date**: 2026-07-15  
**Status**: Draft — Awaiting Review

## Problem

The docgen-9 and docgen-10 benchmark runs exposed a fundamental flaw in tzro's compaction pipeline: **the compaction process destroys the discoveries it's meant to preserve**.

### Evidence

- **Docgen-9**: The 1B router model generated 4096 tokens of hallucinated filler per Recall step because compaction fed it unparsed content it couldn't handle (19 min wasted).
- **Docgen-10**: After routing synthesis to the 4B worker, outputs still contained hallucinated types (`InferenceEngine`, `ModelConfig` — none exist in the codebase). The Probe read the correct files, but compaction compressed the tool outputs into "the probe explored the cache package" — losing all function signatures and type definitions.

### Root Cause

Four separate compaction paths exist, each with different strategies and no content awareness:

| Call Site | Strategy | Problem |
|-----------|----------|---------|
| `compactThoughtChain` in probe.go | LLM "compress these steps" | Dumps code + reasoning into one prompt. LLM hallucinates types. |
| `compactRefinedContext` in recall.go | LLM "merge related facts" | Same problem — no content classification. |
| `TruncateToolOutput` in truncation.go | Deterministic (code/tabular/text) | Good classification, but only used for accumulated context truncation. |
| `cache.Process` in cache.go | Deterministic 5-layer pipeline | Handles JSON/HTML/Base64, ignores code structure. |

The existing `truncation.go` already has the *right idea* (content classification, code skeleton extraction) but it's only used in one path. The LLM compaction paths ignore content type entirely.

---

## Design

### Core Principle

> **Code is NEVER LLM-compressed. LLM only compacts the model's own reasoning text.**

- **Code segments**: Deterministic skeleton extraction — function signatures, type declarations, doc comments, const/var blocks. Function bodies replaced with fingerprints: `// [body: 42 lines, calls: foo(), bar()]`.
- **Text segments (tool outputs)**: Deterministic middle-out truncation. Never LLM-compressed because tool outputs are ground truth that should not be paraphrased.
- **Reasoning text (model's `Thought` field)**: Chunked by sentence into ~500-char groups, each compressed by the router LLM with prompt: `"Compress this reasoning into its key conclusion."` This preserves *decisions* while stripping *deliberation*.
- **Tabular segments**: Deterministic — header + N sample rows + summary line.

### Architecture

```
                     Raw Content (steps, tool outputs, node outputs)
                                        |
                                        v
                             +---------------------+
                             |   Segment Splitter   |
                             |  Fenced code blocks  |
                             |  classifyContent()   |
                             +---------------------+
                                        |
                        +---------------+---------------+
                        v               v               v
                  +----------+   +----------+   +----------+
                  |   Code   |   |   Text   |   |  Tabular |
                  | Segment  |   | Segment  |   | Segment  |
                  +----------+   +----------+   +----------+
                        |               |               |
                        v               v               v
             +----------------+ +---------------+ +--------------+
             | Deterministic  | | Deterministic  | | Deterministic|
             | Skeleton:      | | Middle-out     | | Header + N   |
             | sigs + types + | | truncation     | | sample rows  |
             | doc comments   | | (head + tail)  | |              |
             +----------------+ +---------------+ +--------------+
                        |               |               |
                        v               v               v
                             +---------------------+
                             |     Reassemble      |
                             |  (ordered segments)  |
                             +---------------------+
                                        |
                                        v
                                Compacted Output
```

For **reasoning text** (model's `Thought` field in `CompactSteps`):

```
              Model Reasoning Text (Thought field)
                             |
                             v
                  +---------------------+
                  | Split by sentence   |
                  | Group into ~500 char|
                  | chunks              |
                  +---------------------+
                             |
                    +--------+--------+
                    v        v        v
              +------+ +------+ +------+
              |Chunk1| |Chunk2| |Chunk3|
              | <=500| | <=500| | <=500|
              +------+ +------+ +------+
                    |        |        |
                    v        v        v
              +--------------------------+
              |  Router LLM (1B, fast)   |
              |  "Extract key conclusion"|
              +--------------------------+
                             |
                             v
                  Compressed Reasoning
```

### Segmentation Rules

1. **Fenced code blocks** (triple-backtick markers): Content between markers -> Code segment. Content outside -> Text or Tabular.
2. **Pure files** (no code fences): Use `classifyContent()` heuristic on the whole content.
3. **Mixed markdown**: Split at fence boundaries, classify each piece.

### Budget Management — Two-Stage Cascade

**Stage 1: Structured Compaction**
Apply the full pipeline: skeletonize code, truncate tabular, compress reasoning text. If within budget -> done.

**Stage 2: Oldest-First Triage**
If still over budget:
- Drop tool outputs from the **oldest** steps first (keep only compressed `Thought` summaries)
- Keep the **most recent N** steps' tool outputs intact
- Preserves the exploration trajectory while sacrificing older raw evidence

---

## Interface

New package: `internal/compactor/`

```go
package compactor

// CompactEngine abstracts LLM inference for reasoning text compression.
type CompactEngine interface {
    CompactReasoning(ctx context.Context, chunk string) (string, error)
}

// SegmentType classifies content for type-appropriate compaction.
type SegmentType int
const (
    SegmentCode    SegmentType = iota // Source code — deterministic skeleton
    SegmentText                       // Prose/logs — deterministic middle-out
    SegmentTabular                    // Structured data — header + sample rows
)

// CompactResult holds the compacted output with metrics.
type CompactResult struct {
    Output       string
    InputChars   int
    OutputChars  int
    LLMCalls     int    // Number of LLM calls for reasoning compression
}

// CompactContent compacts a single piece of content (tool output,
// node output, etc.) using deterministic content-aware strategies.
// No LLM is used — this is purely structural.
func CompactContent(content string, budget int) string

// CompactSteps compacts a series of thought chain steps.
// Tool outputs are compacted deterministically (code -> skeleton,
// text -> middle-out, tabular -> header+rows).
// Thought text is compressed via LLM (chunked, router).
// This replaces compactThoughtChain() and TruncateSynthesisContext().
func CompactSteps(ctx context.Context, steps []Step, goal string,
                  budget int, engine CompactEngine) (CompactResult, error)

// CompactFacts compacts a list of refined context facts (recall).
// Text facts are compressed via LLM. Code/data facts are preserved.
// This replaces compactRefinedContext().
func CompactFacts(ctx context.Context, facts string, goal string,
                  budget int, engine CompactEngine) (CompactResult, error)

// Step represents a single thought chain step.
type Step struct {
    Index      int
    Thought    string // Model's reasoning — LLM-compressed
    ToolName   string
    ToolArgs   string
    ToolOutput string // Classified per content type — deterministic only
}
```

### Engine Implementations

```go
// RouterEngine uses the 1B router for reasoning compression.
// Each chunk is <=500 chars, well within the router's capability.
type RouterEngine struct{}

func (r *RouterEngine) CompactReasoning(ctx context.Context, chunk string) (string, error) {
    return inference.CallRouter(ctx, messages, "")
}

// PassthroughEngine returns input unchanged (for tests/benchmarks).
type PassthroughEngine struct{}
```

---

## Call Site Integration

### 1. Probe Thought Chain — probe.go:822

**Before:**
```go
func compactThoughtChain(..., engine ProbeInferenceEngine) error {
    // Dumps ALL step content into one LLM prompt: "compress these steps"
    engine.Infer(ctx, "Compress the following exploration steps...", stepsText, "")
}
```

**After:**
```go
func compactThoughtChain(..., engine compactor.CompactEngine) error {
    steps := // load from thought_chain table
    result, err := compactor.CompactSteps(ctx, steps, goal, 0, engine)
    // Save compacted summary to thought_chain_summaries
}
```

### 2. Recall Refined Context — recall.go:150

**Before:**
```go
func compactRefinedContext(..., engine ProbeInferenceEngine) (string, error) {
    engine.Infer(ctx, "Compress the following list of facts...", context, "")
}
```

**After:**
```go
func compactRefinedContext(..., engine compactor.CompactEngine) (string, error) {
    result, err := compactor.CompactFacts(ctx, context, goal, 2000, engine)
    return result.Output, err
}
```

### 3. Accumulated Context (Edge Thoughts) — executor_context.go:245

**Before:**
```go
output = TruncateToolOutput(output, be.budget)
```

**After:**
```go
output = compactor.CompactContent(output, be.budget)
```

This is a drop-in replacement. `CompactContent` uses the same content classification but produces better code skeletons (function body fingerprints instead of depth-based elision).

### 4. Synthesis Context — truncation.go:359

**Before:**
```go
func TruncateSynthesisContext(steps []SynthesisStep) string {
    // Truncates oldest tool outputs first
}
```

**After:**
```go
func TruncateSynthesisContext(steps []SynthesisStep) string {
    compactorSteps := // convert SynthesisStep -> compactor.Step
    result, _ := compactor.CompactSteps(ctx, compactorSteps, "", maxSynthesisContextChars, nil)
    return result.Output
}
```

When `engine` is nil, `CompactSteps` skips LLM reasoning compression and does deterministic-only.

---

## Module Structure

```
internal/compactor/
  compactor.go          # Core Compact/CompactSteps/CompactFacts functions
  segment.go            # Segmentation (fenced code blocks, classifyContent)
  skeleton.go           # Code skeleton extraction (sigs, types, fingerprints)
  engine.go             # CompactEngine interface + RouterEngine implementation
  compactor_test.go     # Unit tests
  segment_test.go       # Segmentation tests
  skeleton_test.go      # Code skeleton tests
```

Absorbed from `executor/truncation.go`:
- `classifyContent()` -> `segment.go`
- `truncateCode()` -> replaced by `skeleton.go` (improved: body fingerprints)
- `truncateTabular()` -> `segment.go`
- `truncateTextMiddleOut()` -> `segment.go`
- `isCodeSignatureLine()` -> `skeleton.go`

The `TruncateToolOutput` and `TruncateSynthesisContext` functions remain in `executor/truncation.go` as thin wrappers for backward compatibility, delegating to the compactor.

---

## Code Skeleton Fingerprint Format

When a function body is removed, it's replaced with a fingerprint:

```go
func PruneColumns(ctx context.Context, tsvContent string, stepInstruction string) (string, error) {
    // [body: 42 lines, calls: PruneColumns(), LLM.Call(), strings.Split()]
}
```

The fingerprint contains:
- **Line count** of the removed body
- **Function calls** extracted via simple regex for exported and method calls
- Maximum 5 calls listed, sorted by frequency

This gives synthesis enough context to understand what the function does without seeing the implementation.

---

## Verification Plan

### Automated Tests
```bash
go test ./internal/compactor/... -count=1 -v
```

Test cases:
1. Code skeleton extraction preserves all `func`/`type`/`const`/`var` declarations
2. Fenced code block segmentation correctly splits mixed markdown
3. Reasoning text compression produces shorter output than input
4. Budget cascade: Stage 1 fits -> no triage; Stage 1 over budget -> Stage 2 drops oldest
5. Nil engine -> deterministic-only mode (no panics)
6. Integration: `CompactSteps` with real probe thought chain data

### Benchmark Validation
Re-run docgen benchmark after integration. Success criteria:
- No hallucinated types in output (function signatures preserved through compaction)
- ADR summary contains actual ADR content, not empty table
- Runtime < 30 min (eliminates 4096-token filler runs)
