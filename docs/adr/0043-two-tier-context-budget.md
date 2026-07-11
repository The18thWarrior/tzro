# Two-Tier Context Budget — Probe Generation Cap and Accumulated Context Truncation

Two independent mechanisms to prevent context size from collapsing local model inference speed.

## Status

Accepted.

## Context

Benchmark analysis (results-full-2) revealed two distinct mechanisms producing oversized prompts that collapse local model throughput:

1. **Probe step generation blowup**: A single probe step generated 16,384 tokens (the entire context window), inflating every subsequent probe step prompt to 17K+ tokens and dropping speed to 0.1–2.2 t/s. Root cause: no `max_tokens` on probe inference calls.

2. **DAG accumulated context growth**: Large node outputs (e.g., a Recall node containing 30 ADR files, ~25K chars) are passed untruncated through `buildAccumulatedContext` into every downstream node's prompt. Three nodes at 25K chars each → 48K chars of context → 15–17K prompt tokens.

The local model's sweet spot is 1K–3K prompt tokens (10–30 t/s). Beyond 5K prompt tokens, speed drops below 10 t/s. Beyond 10K, it collapses below 3 t/s.

## Decision

### Mechanism A: Probe Step Generation Cap

Set `max_tokens` (via `n_predict` on llama-server) to a configurable cap (default: 2048) for probe **step** inference calls. Synthesis calls remain uncapped. The cap is passed through the existing context-key pattern (like `ThinkingEnabledKey`) to avoid breaking the `InferenceBackend` interface.

When generation is truncated mid-`<ACTION>` tag, the probe treats it as a no-op step (no tool call). Existing futility detection (ADR-0019 guard rail) catches repeated no-op patterns.

Config: `probeStepMaxTokens` (default: 2048, 0 = use default)

### Mechanism B: Per-Node Accumulated Context Truncation

Apply `TruncateToolOutput` to each node's output at collection time in `buildAccumulatedContext`, with a per-node budget of `totalBudget / min(nodeCount, maxAccumulatedContextNodes)`. This is non-destructive — full output remains in SQLite for terminal synthesis and debugging.

Config: `accumulatedContextMaxChars` (default: 16000, 0 = use default)

The existing `maxAccumulatedContextNodes = 6` constant remains hardcoded. The char budget is the primary control; node count is a safety net.

## Considered Options

- **Single unified "context budget" concept**: Rejected — the two mechanisms operate at different seams (inference-time generation vs. context-assembly-time truncation) and require different controls.

- **Post-hoc truncation of probe responses (no `max_tokens`)**: Rejected — truncating after generation doesn't save the initial generation time. The 16K generation at 98 t/s took 167s; a 2048 cap saves ~150s on that call alone plus ~500s of downstream context inflation.

- **Total-context truncation instead of per-node**: Rejected — truncating the assembled string risks cutting the most recent node (often the most relevant for downstream extraction). Per-node truncation distributes the budget fairly.

- **Weighted per-node budgets (recall > action)**: Deferred — even distribution is sufficient for now. Recall node supersession already skips probe raw output, giving recall a natural advantage. Can revisit based on next benchmark run.

- **Truncation at persistence time (destructive)**: Rejected — full node output is needed by terminal synthesis and for debugging. Collection-time truncation preserves the raw data.

## Consequences

- Probe steps that generate long reasoning before a tool call may get truncated. The 2048 default is 14x the median healthy probe step (140 tokens) and should accommodate all normal responses.

- Downstream validators may lose fine-grained details from large upstream outputs. The content-aware `TruncateToolOutput` preserves function signatures and structural landmarks, mitigating this for code-heavy outputs.

- Both caps are configurable per-deployment. Users with faster hardware (or smaller codebases producing less context) can raise them.
