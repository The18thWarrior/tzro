# Use Case: Verified Task Execution (VTE)

**Actor**: Developer running tasks through the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

A developer runs a task that produces a synthesis output and expects the engine to automatically verify that the output meets the original goal before delivering it. The Verification Gate scores the synthesis on goal alignment, factual grounding, coherence, and completeness. If the output fails verification, the engine either re-synthesizes from accumulated context or spawns targeted scatter probes to fill specific gaps — all without user intervention.

## Preconditions

- The `tzro` daemon is running with at least one inference sidecar active.
- A cloud model backend is configured for the verification rubric evaluation.
- The task has completed its exploration phase and produced a terminal synthesis.

## Success Criteria

- [ ] After a probe or recall node completes synthesis, the Verification Gate runs automatically before the output is finalized.
- [ ] Structural pre-check runs locally first, catching empty output, too-short output (<50 chars), generation guard markers, meta-response framing, meta-commentary degeneration, and repetitive content — without any cloud API call.
- [ ] When structural pre-check fails, the result is recorded with `source: "local_precheck"` and no cloud verification is attempted.
- [ ] When structural pre-check passes, the cloud verification rubric evaluates goal alignment, factual grounding, coherence, and completeness as scores between 0.0 and 1.0.
- [ ] The cloud rubric uses structured output mode (JSON schema constraint) to guarantee parseable responses.
- [ ] The verification result includes an `accepted` boolean, numeric scores, a reason string, and an optional `reSynthesis` field.
- [ ] When verification rejects the output with `accepted: false`, the engine uses the `reSynthesis` content as the corrected output if provided.
- [ ] When verification identifies missing goal items (`scatterItems`), the engine spawns targeted scatter probe nodes to address each gap (ADR-0071).
- [ ] Scatter probes have a capped token budget (300 tokens) to prevent attention fatigue.
- [ ] Scatter probe count respects the mutation budget, using at most half the remaining spawns.
- [ ] A scatter assembly node concatenates scatter probe outputs and triggers a re-verification cycle.
- [ ] The verification result is persisted in the execution envelope for observability.

## Edge Cases to Probe

- Synthesis output is exactly 50 characters — verify it passes the length check.
- Synthesis output contains `[GENERATION_ABORTED]` marker embedded mid-text — verify structural pre-check catches it.
- Cloud model returns malformed JSON despite schema constraints — verify graceful fallback.
- Verification rejects with 3 scatter items but mutation budget only allows 1 probe — verify budget cap is respected.
- Verification scores all above 0.8 but `accepted` is false — verify the reason string explains the rejection.
- All scatter probes return empty results — verify assembly node produces a degraded but non-empty output.

## Anti-Patterns to Watch For

- [ ] Cloud verification is called when structural pre-check already failed, wasting API tokens.
- [ ] Scatter probes inherit the full step budget of the parent probe instead of the capped 300-token limit.
- [ ] Verification runs in an infinite loop — re-synthesis triggers re-verification which triggers re-synthesis.
- [ ] The `reSynthesis` field from the cloud model contains raw meta-commentary ("Here is the improved version:") instead of clean synthesis content.
- [ ] Scatter assembly node drops scatter probe outputs due to compaction before concatenation.
- [ ] Verification result is not persisted, making post-hoc quality analysis impossible.
