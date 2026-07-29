# Probe Execution Resilience

Benchmark run `results-full-4` exposed three distinct failure modes in Probe Node and Analyze Node execution that caused 3/20 tasks to score ≤2.0 despite the underlying Local Model having sufficient capability. In each case the model *had* the right information but the execution layer either misdirected it (DirectSynthesis on 2.1M chars), rejected valid output (repetition detector false-positive on tabular data), or failed to enforce the Thought Chain protocol (15 steps of reasoning-without-acting). These are execution-layer gaps, not model capability limits.

We introduce three complementary mechanisms: (A) a DirectSynthesis size cap with full summary concatenation, (B) analyze-node-aware synthesis validation, and (C) the Exploration Queue with no-action retry and SQL auto-extraction.

## Mechanism A: DirectSynthesis Hard-Cap + Summary Concatenation

When `preloadDirectoryContext` output exceeds 200K chars, the system currently auto-promotes to DirectSynthesis. For `internal/` (2.1M chars), this produced a 910-char vacuous summary — DirectSynthesis is designed for pre-structured content, not raw megabyte-scale directory dumps. Above 200K chars, the system now falls through to the normal Thought Chain with truncated preload instead.

Separately, `runSynthesisPass` calls `GetLatestSummary` which returns only the most recent compaction summary. For long-running probes (25+ steps), this means synthesis only sees the last compaction window's summary while earlier exploration is lost. Changed to concatenate all summaries chronologically, giving synthesis the full rolling view.

## Mechanism B: Analyze Node Repetition Detector Exemption

`validateSynthesisOutput` rejects any synthesis where a 4-word phrase repeats 3+ times. Tabular data naturally repeats column headers and structural patterns (e.g., `leads\n - Distinct Lead_Sources:` across rows). In `lead_source_by_owner`, the probe captured 7699 chars of correct SQL results and injected them into synthesis — but the synthesis was rejected as "repetitive," escalated to cloud, then garbled by the Recall fallback's 2K-per-step cap. The probe's output was correct; the validator rejected it.

Analyze Node synthesis outputs are now exempt from the repetition detector. The existing content-level validation (control token leak detection, degenerate output length check) still applies.

## Mechanism C: Exploration Queue + No-Action Retry + SQL Auto-Extract

Three sub-mechanisms targeting different stall patterns:

**Exploration Queue**: A side-channel file list built from PreloadPaths at probe start. Tracks visited/unvisited files. When duplicate `read_file` calls are detected, the execution layer substitutes tool arguments with the next unvisited file rather than injecting a text hint the model ignores. `cache_function_index` called `read_file` on `cache.go` 12 consecutive times despite hint injection — the model cannot reliably follow "try something different" instructions at 4B scale.

**No-Action Retry**: The Thought Chain contract is think → act → observe. A step that produces reasoning text without an `<ACTION>` tag is a protocol violation, not a valid step. Previously, these burned a step slot and injected a generic error. Now: the step counter does not increment, the step is not persisted, and a corrective prompt is injected containing the model's own text and the available tools list. Capped at 2 retries per step position. `lead_lookup_by_company` ran 15 steps of narration without a single tool call — with no-action retry, step 1 would have been re-prompted immediately.

**SQL Auto-Extraction (Analyze Nodes only)**: When an Analyze Node model produces SQL in its response text but fails to wrap it in an `<ACTION>` tag, the execution layer regex-extracts `SELECT ... FROM cache_\d+` patterns and auto-executes via `sql_cached_data`. Scoped to Analyze Nodes only — they have a closed tool surface (`introspect_cache`, `sql_cached_data`) with well-defined SQL syntax. Generic Probe Nodes have arbitrary tool schemas where auto-extraction would be fragile.

## Considered Options

- **Subdir fan-out for large preloads**: Decompose large directories into per-subdirectory Probe Nodes and merge syntheses. Rejected — adds DAG complexity for a problem solvable by falling back to the existing Thought Chain, which is purpose-built for large content. The hard-cap on DirectSynthesis promotion is simpler and doesn't introduce new multi-probe coordination.
- **Force-inject analytical evidence into Recall Node synthesis**: The evidence injection code already exists in the probe synthesis pass (`probe.go:857-881`). The gap was the false-positive rejection, not missing injection. A second injection point in the Recall Node would mask the root cause rather than fixing it.
- **Hard force-synthesis after N duplicate tool calls**: Treats the symptom (loop) by accepting defeat (synthesize with partial data). The Exploration Queue redirects the loop productively rather than terminating it. Force-synthesis is the last resort when the queue is exhausted, not the first response.
- **Golden output regression testing**: Premature — the failure modes are known-open and reproducible. Regression baselines make sense after the execution-layer fixes land, not before.
- **LoRA adapters per node/edge envelope**: The long-term solution for enforcing `<ACTION>` tag compliance at the weight level rather than prompt level. Deferred to the model fine-tuning roadmap. No-action retry is the zero-cost bridge.

## Consequences

- `preloadDirectoryContext` oversized outputs (>200K chars) now route through Thought Chain instead of DirectSynthesis. Large codebase exploration takes longer but produces higher-quality results.
- `GetLatestSummary` replaced by `GetAllSummaries` in synthesis pass. Synthesis prompts grow by the cumulative summary size (typically 2-5K chars for a 25-step probe). Well within the 4B model's context budget.
- Analyze Node synthesis outputs bypass the repetition detector. If a genuine degenerate output occurs on an Analyze Node, it will only be caught by the length and control-token checks. Accepted risk — tabular repetition is far more common than actual degeneration for data analysis tasks.
- Exploration Queue adds a `[]string` side-channel per probe. Negligible memory cost. Requires the preload directory walk to return both content and file paths (currently returns only content).
- No-action retry changes step accounting: invalid steps don't count toward the step budget. A probe could theoretically retry indefinitely if the model never produces a valid action — the 2-retry cap per step position prevents this.
- SQL auto-extraction in Analyze Nodes executes model-generated SQL without explicit model intent. Mitigated by: (1) scoping to Analyze Nodes only, (2) the existing 4-layer SQL safety in the Ephemeral Query Database (physical isolation, SELECT-only parsing, table allowlist, timeout+row cap), and (3) only extracting `SELECT` statements targeting `cache_*` tables.
