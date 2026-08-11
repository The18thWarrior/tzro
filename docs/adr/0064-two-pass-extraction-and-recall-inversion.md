# Two-Pass Tool Extraction, Content-Aware Recall Compaction, and Recall Loop Inversion

Status: accepted. Supersedes the GBNF rescue fallback in ADR-0058 Mechanism C. Extends ADR-0059 (Edge Entry accumulation) with a new LLM summarization surface for text segments. Redefines the Recall Node role from ADR-0037.

## Problem

Benchmark runs 3–5 (research category) revealed that DAG plan shape correlated strongly with quality — but the shape was a symptom, not a cause. Three execution-layer failures in the Probe and Recall nodes were the actual drivers:

1. **Tool-call emission failure (~90% rate):** Probe Thought Chain steps must emit `<ACTION>` tags within free-text reasoning. The 4B model fails ~90% of the time, triggering a GBNF rescue pass that only sees 1500 chars of reasoning (often missing the model's actual intent). The rescue is a fallback bolted onto a primary path that almost never works.

2. **Recall fallback context blowup (69K–99K chars):** The Recall Node's `update_refined_context` tool-calling loop fails 89% of the time (the model signals `SYNTHESIZE_READY` immediately because the manifest already contains the upstream synthesis). The enriched fallback dumps all raw ThoughtSteps uncapped, producing 69K–99K char contexts that overwhelm synthesis.

3. **Recall loop skipping:** The Recall loop's agentic `fetch_details` → `update_refined_context` mechanism requires the 4B model to play a multi-step tool-calling agent with no GBNF rescue — inside a prompt that already contains the answer. The model rationally short-circuits.

## Decision

Three interconnected mechanisms, motivated by the same root cause analysis.

### Mechanism A: Two-Pass Tool Extraction

Every Thought Chain step (Probe and Recall loops) executes two inference passes:

- **Pass 1 (Worker, unconstrained):** Generate free-text reasoning about the current state and next action. The prompt encourages `<ACTION>` tags as hints but does not depend on them.
- **Pass 2 (Router, GBNF-constrained):** Extract the structured action from the reasoning. Schema: `{"action": "tool_call" | "synthesize", "tool": string, "arguments": object}`. Always runs — doubles as a validation layer.

Input to Pass 2:
- If Pass 1 reasoning contains complete `<ACTION>...</ACTION>` tags: targeted extraction (tag content + surrounding context).
- Otherwise: full reasoning output. The router context window matches the worker, so no truncation is needed.

This replaces: the `<ACTION>` tag string-search primary path, the `gbnfRescueAction` fallback, the 1500-char truncation, and the `<SYNTHESIZE_READY>` string-search detection (now handled by the `"action": "synthesize"` enum in the GBNF schema).

### Mechanism B: Content-Aware Recall Compaction

When the Recall loop's `refinedContext` requires enrichment, ThoughtStep FullResults are compacted using the existing Compactor segmentation infrastructure with a new LLM summarization surface for text segments:

| Segment Type | Compaction Strategy |
|:---|:---|
| Code (`SegmentCode`) | `ExtractSkeleton` — deterministic, no LLM |
| Tabular (`SegmentTabular`) | `TruncateTabular` — header + sample rows, no LLM |
| Text (`SegmentText`) | **Router fact-extraction** — "Extract all factual claims, statistics, names, comparisons, and URLs. Output as a bulleted list of facts. Omit opinions, navigation text, and boilerplate." |
| `sql_cached_data` / `introspect_cache` | **Exempt** — full fidelity (Analytical Evidence, ADR-0053) |

Failure cascade for text segments:
1. Router fact-extraction (~4:1 compression)
2. On router failure → `TruncateTextMiddleOut` (deterministic head/tail 30 lines)
3. Final hard truncation if still over budget

Total budget: 32K chars (default), configurable via `RecallCompactionBudgetChars` in `config.json`.

This extends the Compaction Pipeline principle: code is never LLM-compressed; the LLM now also compacts **web/text tool outputs** (not just the model's own reasoning). The new surface is scoped to the Recall fallback only.

### Mechanism C: Recall Loop Inversion

The Recall Node's execution contract inverts from "agentic discovery" to "deterministic floor + Refinement Pass":

1. **Deterministic compaction** (Mechanism B) builds a baseline `refinedContext` from all upstream ThoughtSteps before the agentic loop starts.
2. **Refinement Pass** (the agentic loop) receives the pre-built context and can selectively `fetch_details` for steps where the summary lost important detail, adding facts via `update_refined_context`.
3. If the model signals `SYNTHESIZE_READY` immediately (89% of cases), the baseline context is already good enough for synthesis.
4. If the model engages (11% of cases), it refines the baseline — additive, not required.

The Recall loop also receives Mechanism A's two-pass tool extraction, giving `fetch_details` and `update_refined_context` the same GBNF-backed reliability as Probe tool calls.

The prompt shifts from "discover what's important" to "validate and enrich what we already have."

## Considered Options

- **Early-out on valid `<ACTION>` tags (skip GBNF pass):** Rejected — the 10% success rate doesn't justify maintaining a dual code path. The GBNF pass is cheap (~1-2s on the router) and doubles as validation for malformed JSON, missing characters, and hallucinated tool names.
- **Edge Entry snippets for Recall fallback (instead of ThoughtSteps):** Rejected — Edge Entries are low-fidelity by design (2000-char Code Skeletons for `read_file`, hard truncation for `web_browse`). The Recall tier is supposed to be high-fidelity (ADR-0059). Router summarization preserves information density without hard truncation.
- **Hard total budget cap with blind truncation:** Rejected — produces the same information loss as Edge Entry snippets. Router summarization compresses ~4:1 on text while preserving key facts, statistics, and URLs.
- **Remove the Recall agentic loop entirely:** Rejected — the 11% success rate shows the model can refine context when it cooperates. The inverted design makes cooperation additive, not required.
- **Truncate GBNF rescue input to 1500 chars:** Rejected (legacy) — the router context window was increased to match the worker. Full reasoning output provides better tool extraction signal.

## Consequences

- **Two inference calls per Thought Chain step** instead of one (when `<ACTION>` succeeds) or two (when rescue fires). Net cost is ~1-2s additional per step in the 10% case where tags would have succeeded. Acceptable given the consistency and validation benefits.
- **Router inference calls during Recall compaction.** One call per text-segment ThoughtStep (~1-2s each). With 10-20 text steps, adds 10-40s to Recall execution. This is offset by eliminating the degenerate 99K-char synthesis attempts that previously caused cloud escalation cascades.
- **CONTEXT.md terminology:** "Map-Reduce Recall" updated to "Refinement Pass." "GBNF Rescue" removed as a distinct concept — the GBNF extraction pass is the designed path, not a rescue.
- **`ThoughtChainStepSchema` simplified.** The full schema (`action`, `tool`, `arguments`, `nextThought`, `confidence`, `synthesis`) is no longer used for the per-step inference. Pass 1 is unconstrained; Pass 2 uses the new minimal `action`/`tool`/`arguments` schema.
- **Compaction Pipeline principle extended.** The "code is never LLM-compressed" invariant is preserved. The new surface is "text/web content is LLM-summarized via fact-extraction." The glossary entry is updated accordingly.
- **`RecallCompactionBudgetChars` config field.** Defaults to 32000. Operators can tune based on their model's effective context window.

## References

- ADR-0037: Recall Node for Discovery-Synthesis Alignment (role redefined)
- ADR-0058: Probe Execution Resilience (GBNF rescue superseded by two-pass)
- ADR-0059: Incremental Edge Entry Accumulation (two-tier design preserved, Recall tier enhanced)
- ADR-0053: Analytical Evidence for Data Analysis (exemption preserved)
- Benchmark analysis: DAG Shape Analysis runs 3–5 (research category)
