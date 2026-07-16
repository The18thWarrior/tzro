# Use Case: Content-Aware Compaction

**Actor**: Developer delegating multi-step exploration tasks via `tzro_run`
**Route**: N/A (backend engine — affects all probe node executions and task outputs)
**Backend**: Internal pipeline in `internal/compactor/` and `internal/executor/probe.go`
**Priority**: P0

---

## Intent

The user delegates a complex exploration or research task to tzro. As probe nodes execute thought chains with many tool calls, the accumulated context (tool outputs, reasoning text) can overflow the local model's context window. The content-aware compaction engine must transparently compress this context while preserving all code verbatim — only the model's own reasoning text is LLM-compressed, and tool outputs receive deterministic content-type-aware truncation (skeleton extraction for code, JSON pruning for structured data, line truncation for logs).

## Preconditions

- tzro daemon is running (`curl -sf http://localhost:36888/health`)
- A local model is loaded (router sidecar for compaction, worker sidecar for synthesis)
- A task with probe nodes has been submitted via `tzro_run` or `tzro chat`

## Success Criteria

- [ ] Probe thought chain completes without context window overflow errors, even with 20+ steps
- [ ] Code content in tool outputs (Go, TypeScript, Python, etc.) is never LLM-compressed — preserved verbatim or skeleton-extracted
- [ ] Model reasoning text is compressed via the router model into key conclusions
- [ ] JSON tool output is pruned deterministically (arrays truncated, nested objects capped)
- [ ] Log/text content is truncated by lines, not LLM-compressed
- [ ] Compaction triggers every 3 steps (architectural constant, not planner-controlled)
- [ ] When compaction runs, subsequent inference calls operate within the model's context window
- [ ] Terminal synthesis output is coherent and reflects findings from all steps, not just post-compaction steps
- [ ] CompactResult metrics (InputChars, OutputChars, LLMCalls) are accurate

## Edge Cases to Probe

- Probe with 30 steps (max budget) reading large files — compaction must prevent overflow
- Tool output that is entirely code (e.g., `read_file` on a 200-line Go file) — must not be LLM-compressed
- Mixed content: reasoning mentioning code snippets inline — reasoning compressed, code preserved
- Router model returns error during compaction — graceful fallback to original text
- Empty tool output — compaction handles gracefully without errors

## Anti-Patterns to Watch For

- [ ] Code snippets appear garbled, summarized, or hallucinated after compaction
- [ ] Context window overflow crashes mid-thought-chain
- [ ] Compaction removes critical file paths or line numbers from tool output
- [ ] LLM compaction inflates text (output longer than input)
- [ ] Terminal synthesis is incoherent or missing findings from early steps
- [ ] Compaction runs on every step instead of every 3rd step (perf regression)
- [ ] JSON tool output has its keys renamed or values fabricated by LLM
