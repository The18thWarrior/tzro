# Generation Guard on Inference Backend

Benchmark run `results-full-6` exposed a catastrophic failure mode: `update_add_method` (T1, quality 1.25) where the local model entered an infinite repetition loop, generating 8,910 lines of duplicate method declarations. The existing `stripTrailingRepetition` in `recall.go` runs post-synthesis — by the time it fires, the model has already consumed 131K tokens and 918 seconds of wall clock. The line-count cap in the compilation gate (`8910 > 500 max`) caught it post-write but couldn't recover the wasted inference time.

We introduce the **Generation Guard**: a per-inference quality gate on the **Inference Backend** abstraction that detects degenerate output during generation (streaming backends) or via post-generation scan (non-streaming backends). For streaming backends (llama-server, OpenAI-compatible), a callback monitors the growing output buffer and aborts the HTTP stream on detection. For non-streaming backends (harness callback), the complete response is scanned and truncated post-generation.

Detection uses three tiers:
- **Character-level collapse**: Single-character or single-token repetition (backtick-space, dot fills). Inherited from the existing `stripTrailingRepetition` logic. Fires when >80% of trailing 200 chars are degenerate pairs.
- **Block-level repetition**: A sliding window of 10 lines is hashed. If the same hash appears 3 consecutive times (30 identical contiguous lines), generation is aborted. This catches `update_add_method`'s failure pattern (structurally valid but repeated code blocks) while allowing legitimate structural repetition in handler/switch patterns.
- **Compression-ratio detection**: Periodic `flate` compression of trailing 2K chars, compared against content-mode-aware thresholds (Code: <0.35 with 4096-char minimum, Prose: <0.50, Tabular: <0.10). Content mode auto-detected via table/CSV markers. Added after benchmark run 21 revealed paraphrased degeneration (semantically repetitive but lexically varied content) that evades character-level and block-level detection. The prose threshold was corrected from 0.35 to 0.50 after false positives on structured comparison content in research synthesis tasks — valid comparison prose compresses to ~0.53, while degenerate paraphrased loops compress to ~0.39.

The streaming callback fires per-line (on newline character), not per-token, keeping overhead negligible. The block hash check is O(1) amortized (update rolling hash, compare to previous). The compression check runs every 50 lines (configurable) to amortize the flate overhead.

The interface is designed for extensibility: `GenerationGuard` with a `func OnTokens(accumulated string) Action` contract returning `Continue` or `Abort`. `RepetitionGuard` is the first implementation. Future guards (token budget enforcement, early `</ACTION>` tag completion, wall-clock circuit breaker) plug in without touching plumbing.

`stripTrailingRepetition` in `recall.go` is promoted to the Generation Guard as the non-streaming fallback path. The Recall Node no longer owns repetition detection.

## Considered Options

- **Post-generation, pre-write gate only**: Scan the full response after generation completes but before writing to disk. Doesn't save the wasted inference time (918s). The streaming abort is the only way to cut the waste at the source.
- **`CompilationGateHook` line-count check**: Already exists (`8910 > 500 max`) but fires post-write. Moving it pre-write still doesn't save inference time. A line-count check is also a blunt instrument — it can't distinguish 500 lines of valid code from 500 lines of repeated garbage.
- **Repetition penalty in sidecar generation config**: Tuning `repeat_penalty` in the llama-server sampling parameters. This is a model-level control that affects all generation, including legitimate structural repetition. The Generation Guard is a content-level detector that can distinguish degenerate loops from valid patterns.

## Consequences

- The **Inference Backend** interface gains a `GenerationGuard` registration point. All three backend implementations must respect it (streaming backends invoke during generation, non-streaming invoke post-generation).
- `stripTrailingRepetition` moves from `internal/executor/recall.go` to the Generation Guard. The Recall Node's `validateSynthesisOutput` no longer calls it — the guard runs earlier in the pipeline.
- The block-level detection threshold (10-line window, 3 matches) is hardcoded initially. If legitimate edge cases surface, it can be exposed via **Execution Policy**.
- Aborted generations return a truncated response with a `[GENERATION_ABORTED: repetition detected]` marker. Downstream consumers (compilation gate, synthesis pass) see this marker and can decide how to handle it (e.g., retry, escalate).
