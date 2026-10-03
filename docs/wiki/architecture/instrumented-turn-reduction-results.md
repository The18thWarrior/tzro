# Instrumented native turn reduction results

Date: 2026-10-02. Experiment: E070. Status: full comparison passed.

Guided Tzro completed the suite in 125.28 seconds, with all nine tasks correct.
It used 37.69% less time and 43.24% fewer completed model invocations than Native.
Against Simple, it used 36.91% less time and 40.57% fewer invocations.
The complete-suite result passed the declared 20% timing threshold.
These results establish a measured benefit for the guided integration on this development suite.
They do not establish performance on unseen tasks or other models.

## Complete comparison

Each row covers the same nine tasks. Time includes the agent, final checks, private grading, and instrumentation overhead.

| Condition | Correct | Total time | Completed model invocations | Tool executions | Successful Tzro use |
| --- | ---: | ---: | ---: | ---: | ---: |
| Native | 9/9 | 201.07 s | 111 | 116 | 0/9 |
| Simple helper | 9/9 | 198.58 s | 106 | 104 | 0/9 |
| Guided Tzro | 9/9 | 125.28 s | 63 | 63 | 9/9 |

Simple was the fastest eligible control. Tzro reduced its time by 36.91%, exceeding the 20% threshold.
All 27 tasks passed. No failed task or selected subset was removed from the comparison.
Tzro was faster on seven of nine tasks against Native and eight against Simple.
It used fewer model invocations on eight tasks against each control, with one tie in each comparison.

## Actual native execution and tools

The harness launched frozen Antigravity 1.2.15 with Gemini gemini-3.8-flash-low.
Each task used an isolated HOME and a temporary workspace outside this repository.
The client retained its ordinary tools and batching. Conditions rotated first position across the suite.
The production Tzro binary, explicit AGENTS.md guidance, tasks, graders, and ten-minute deadline matched the earlier guided screen.
Native used ordinary tools. Simple had the repository bulk_update.py helper and invoked it on two tasks.
Tzro made nine successful production tzro_edit_and_verify MCP calls, one per task.
Every composite call reported applied edits and successful verification. Independent final checks and private grading then passed.
The benchmark exercised the production MCP implementation through the actual native client.

## How model turns were measured

Every condition received the same passive PreInvocation, PostInvocation, and Stop observer.
The recorder returned empty JSON and retained only allowlisted metadata and timestamps.
Each completed invocation required one matched pre/post pair, consecutive numbering, and the correct conversation identity.
A successful native result and an idle terminal Stop were also required.
The count had to match deduplicated completed agent_response events from that conversation.
All 27 tasks satisfied both measurements. Independent analysis reproduced every count from the retained raw events.
Native had 111 completed invocations, Simple had 106, and Tzro had 63.
These are observed main-agent invocation boundaries. They do not count hidden auxiliary calls or HTTP retries.
Tool executions remain separate: 116, 104, and 63 respectively.
The [instrumentation specification](native-invocation-instrumentation.md) describes the implementation and rejection rules.

## Task results

Each entry shows completion time in seconds, followed by completed model invocations. All tasks passed.

| Task | Native: seconds / invocations | Simple: seconds / invocations | Tzro: seconds / invocations |
| --- | ---: | ---: | ---: |
| go-duration-diagnosis | 16.36 / 9 | 24.33 / 10 | 10.55 / 6 |
| go-pricing-bugfix | 16.48 / 10 | 16.30 / 9 | 13.69 / 6 |
| go-pricing-rename | 30.04 / 15 | 31.33 / 14 | 14.81 / 7 |
| py-mutable-default | 12.89 / 8 | 12.82 / 9 | 12.22 / 8 |
| py-pagination-bugfix | 15.04 / 11 | 16.27 / 11 | 15.05 / 10 |
| py-parameter-rename | 18.12 / 11 | 16.93 / 10 | 9.94 / 5 |
| ts-function-rename | 34.68 / 23 | 41.58 / 23 | 20.55 / 6 |
| ts-timezone-diagnosis | 44.05 / 16 | 24.35 / 13 | 12.92 / 8 |
| ts-validation-boundary | 13.42 / 8 | 14.67 / 7 | 15.54 / 7 |

The TypeScript validation task was slower with Tzro against both controls.
The Python pagination task was slightly slower than Native despite fewer invocations.
Fewer model turns do not guarantee a faster individual task.

## Tokens, intervals, and limits

| Condition | Client total tokens | Client cache-read tokens | Sum of invocation intervals |
| --- | ---: | ---: | ---: |
| Native | 1,149,103 | 718,303 | 185.78 s |
| Simple helper | 1,079,802 | 743,937 | 183.47 s |
| Guided Tzro | 701,629 | 329,251 | 111.23 s |

Invocation intervals include hook dispatch and native client work around the model call.
They are not isolated provider latency. Queue state and provider cache behavior remain uncontrolled.
The primary timer includes observer overhead; more invocations also trigger more hook executions.
Local calibration measured 7.99 ms median per hook process, with an 11.02 ms p95 and a 314.53 ms maximum.
Calibration was separate from the live run, and no estimated overhead was subtracted.
Token counts are client-reported. Their billing semantics were not independently verified.
Actual charges, provider request attempts, and isolated provider latency remain unknown.
This matrix used nine previously inspected development tasks and one model.
Earlier guided runs also passed the timing gate, but they lacked this model-turn instrumentation.
Their raw reports and unknown round counts remain unchanged.
The treatment includes tool installation and explicit guidance. This comparison does not isolate their individual effects.

## Validation and evidence

The full Go suite and recorder race tests passed before live execution.
The affected benchmark and CLI suites passed again after adding the native-response cross-check.
Offline readiness verified toolchains, the frozen client, broken fixtures, reference solutions, and private graders.
The final audit matched all task receipts, retained workspace hashes, observer configurations, and frozen inputs across 1,773 evidence files.
The aggregate ledger contains 127 completed launches, including the previous 100 attempts.
All 27 additional launches were used for this matrix. No retry or discarded attempt was needed.
The approved allowance is exhausted. Charges remain unknown.

- [Native report and task artifacts](../../../.scratch/turn-reduction/instrumented-screen-20261002/live-matrix/report.json).
- [Derived metrics and task details](../../../.scratch/turn-reduction/instrumented-screen-20261002/analysis.json).
- [Independent verification](../../../.scratch/turn-reduction/instrumented-screen-20261002/evidence-verification.json) and [file hashes](../../../.scratch/turn-reduction/instrumented-screen-20261002/evidence-sha256.json).
- [Frozen contract](../../../.scratch/turn-reduction/instrumented-screen-20261002/preregistration.json), [measurement addendum](../../../.scratch/turn-reduction/instrumented-screen-20261002/measurement-addendum.json), and [launch approval](../../../.scratch/turn-reduction/instrumented-screen-20261002/authorization.json).
- [Frozen candidate](../../../.scratch/turn-reduction/instrumented-screen-20261002/candidate-v2.json), [validation record](../../../.scratch/turn-reduction/instrumented-screen-20261002/local-validation.json), and [calibration](../../../.scratch/turn-reduction/instrumented-screen-20261002/observer-calibration.json).
- [Earlier native comparisons](turn-reduction-screen-results.md) and [historical claim audit](../bugs/turn-reduction-evidence-audit.md).
