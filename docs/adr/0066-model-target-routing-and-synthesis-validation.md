# ADR-0066: ModelTarget Routing and Synthesis Validation Gate

**Status**: Accepted  
**Date**: 2024-08-04

## Context

Probe exploration quality suffered from two root causes:

1. **Implicit model routing**: The system used two concrete structs (`DefaultProbeInference` for the 1B Router and `WorkerInference` for the 4B Worker) with no way to route individual inference calls to a specific model. Call sites chose their model at struct construction time, making it impossible for a single Thought Chain step to consult both models.

2. **Premature synthesis**: The 1B Router signaled `synthesize` as early as step 8 because it cannot judge information completeness. Once synthesis was signaled, the probe immediately stopped exploring — even when significant tools remained unused.

3. **Repetition false positives**: The 4-word n-gram detector at 3× threshold flagged structural markdown patterns (repeated section headers, tabular data formats) as degenerate output.

## Decision

### ModelTarget Routing Enum

Collapse `DefaultProbeInference` and `WorkerInference` into a single `ProbeInference` struct. Add a `ModelTarget` enum (`TargetAuto`, `TargetWorker`, `TargetRouter`) as the final parameter to both `Infer` and `InferMessages` on the `ProbeInferenceEngine` interface.

- **TargetAuto** (default): Uses the Router (1B) for fast routing decisions.
- **TargetWorker**: Forces the 4B Worker for quality-sensitive calls (synthesis, validation).
- **TargetRouter**: Forces the 1B Router explicitly.

### Synthesis Validation Gate (Pass 3)

After the Router signals `synthesize` and existing guards (minimum step budget, phase gate) pass, insert a **Pass 3 validation call** to the Worker:

1. Build a validation suffix with: step position, step budget, successful call count, and unused tools.
2. Call `engine.Infer` with `TargetWorker` and `SynthesisValidationSchema` (`{ready, reason, additionalSteps}`).
3. If Worker returns `ready: false`, increment `synthesisRejections` (max 2), extend `minStepBudget`, and `continue`.
4. If Worker returns `ready: true` or validation fails, proceed with synthesis.

The KV cache prefix is reused because the system prompt is byte-identical — only the user message suffix changes.

### Repetition Threshold Fix

Change n-gram size from 4 to 5 words. Scale threshold by output length: `max(len(words)/250, 4)`. This eliminates false positives on structural markdown while still catching genuine degenerate output (e.g., the same sentence repeated 20+ times).

## Consequences

- **Breaking API change**: `ProbeInferenceEngine.Infer` and `InferMessages` gain a `ModelTarget` parameter. All mocks and call sites updated.
- **Latency**: Pass 3 adds one 4B inference call per synthesis signal (only when the gate fires, max 2× per probe). Expected ~500ms overhead per rejection.
- **Eliminated types**: `DefaultProbeInference` and `WorkerInference` are removed. All routing goes through `ProbeInference`.
- **Reduced false positives**: Structural markdown with varied content (e.g., file listings with repeated headers) no longer triggers the repetition guard.
