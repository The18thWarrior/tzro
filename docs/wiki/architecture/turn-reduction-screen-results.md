# Native turn reduction screening results

Date: 2026-10-02. Status: guided screen and matched replication passed.

The subsequent [instrumented comparison](instrumented-turn-reduction-results.md) also passed all 27 tasks.
Tzro used 37.69% less time and 43.24% fewer observed model invocations than Native.
The historical results below retain their original measurements and unknown round counts.

The real native harness passed the 20% speed screen after Tzro tasks received explicit `AGENTS.md` guidance.
Guided Tzro was 23.35% faster than Simple, the fastest eligible control, with successful composite use on all nine tasks.
The matched repeat was 46.71% faster than Simple, with successful composite use on all nine Tzro tasks again.
All 99 comparison tasks passed prescribed checks and private grading.
These repeated development results are promising. They do not establish general performance or provider-latency-controlled causality.

## Completed measurements

Each row sums Verified Completion Time across nine tasks.
This time includes the agent run, final prescribed checks, and independent private grading.

| Installation | Native | Simple helper | Tzro | Tzro versus fastest eligible control | Tzro composite adoption |
| --- | ---: | ---: | ---: | --- | ---: |
| Workspace, v4 | 171.29 s | 229.44 s | 176.37 s | 2.96% slower | 2/9 tasks |
| Global in isolated HOME, v5 | 166.21 s | 180.81 s | 164.58 s | 0.98% faster | 0/9 tasks |
| Global plus explicit AGENTS.md, v6 | 186.65 s | 179.46 s | 137.56 s | 23.35% faster | 9/9 tasks |

Every condition passed 9/9 tasks in each matrix.
Native was the fastest eligible control in v4 and v5. Simple was fastest in v6.
Guided Tzro was also 26.30% faster than Native in v6.
The v4, v5, and v6 setup variants remain separate screens. Their totals are not pooled as repeated samples.
The v7 paired repeat used the same guided candidate as v6:

| Comparison | Simple helper | Guided Tzro | Reduction | Correctness | Composite adoption |
| --- | ---: | ---: | ---: | --- | ---: |
| Paired repeat, v7 | 213.98 s | 114.03 s | 46.71% | 18/18 tasks | 9/9 Tzro tasks |

The v6 and v7 results are shown separately to preserve timing variation.
The repeat did not include Native. It tested the fastest control selected from v6.

## Actual tool use

The v4 TypeScript rename task made one production `tzro_edit_and_verify` MCP call with six edits.
The v4 TypeScript timezone task made one production call with one edit.
Both calls reported applied edits and successful compile and test checks.
Their native traces retain the exact arguments and returned results.

The v5 Tzro tasks made no Tzro tool calls.
The guided v6 tasks made nine production `tzro_edit_and_verify` calls, one per task.
Every call applied edits and passed the configured checks.
The v7 repeat also made nine successful composite calls, one per Tzro task.
All 18 guided Tzro tasks therefore used the actual production MCP implementation.
No measured run contains a Tzro CLI invocation.
The Simple condition invoked the actual `bulk_update.py` helper on one task per full matrix and two tasks in the repeat.
Other tasks used ordinary Antigravity tools.

The real composite tool worked on two unforced live tasks and all 18 guided tasks.
Installation alone did not reliably cause the agent to select it.
Explicit guidance accompanied consistent adoption and a faster complete-suite result in v6.
The comparison does not isolate instructions as the sole cause.

## Harness and grading

The harness launched frozen Antigravity 1.2.15 processes with `gemini-3.8-flash-low` through the Gemini provider.
Each task had an isolated HOME and a temporary workspace outside this repository.
The native client retained its ordinary tools and batching.
Tzro used the production Standard MCP installation. Simple used the actual repository helper.

The suite contains three Go, three Python, and three TypeScript tasks.
Versioned manifests declare task prompts, editable files, and required private tests.
All broken starting fixtures fail their private graders.
All reference solutions pass the prescribed checks and private graders.
Private tests run in isolated copies and require every declared test to execute and pass.
Zero tests cannot count as success.

A ten-minute deadline covers each task through final grading.
Preparation time is reported separately.
The schedule rotates condition positions, with each condition first, second, and third on three tasks.
Source hashes detect drift before another task starts.
The persistent ledger reserves every live launch and retains interrupted attempts.

The complete-suite gate requires all nine Tzro tasks to pass.
It compares Tzro against the fastest complete control with all nine tasks correct.
Tzro must reduce the total time by at least 20% to pass the screen.
No common-passing subset or failed-task timing can improve this score.

The completed [instrumented comparison](instrumented-turn-reduction-results.md) adds native model-boundary hooks. Historical round counts remain unknown.

## Usage and evidence limits

| Matrix | Condition | Tool executions | Client total tokens | Client cache-read tokens |
| --- | --- | ---: | ---: | ---: |
| v4 | Native | 107 | 1,016,112 | 701,783 |
| v4 | Simple | 124 | 1,221,829 | 1,100,542 |
| v4 | Tzro | 106 | 997,631 | 818,703 |
| v5 | Native | 101 | 1,069,636 | 599,739 |
| v5 | Simple | 113 | 1,103,367 | 717,820 |
| v5 | Tzro | 98 | 1,004,186 | 647,958 |
| v6 | Native | 106 | 1,135,092 | 599,122 |
| v6 | Simple | 96 | 1,080,038 | 590,999 |
| v6 | Tzro | 68 | 709,044 | 403,459 |
| v7 | Simple | 114 | 1,191,201 | 807,085 |
| v7 | Tzro | 59 | 699,422 | 268,579 |

Token fields come from terminal native-client events.
Cache-read tokens are shown separately, without an inferred billing formula.
Tool executions are not Cloud Decision Rounds.
Provider request attempts, cloud rounds, provider latency, and charged dollars remain unknown.
The results do not establish monetary savings.

These are inspected development fixtures, not withheld validation tasks.
Provider cache and queue state remain uncontrolled.
The local Go cache was shared and warmed during readiness checks.
The guided comparison has one screen and one repeat on the same development tasks.
Two passes do not establish a reliable uncertainty interval or performance on withheld tasks.
The variation in control times remains visible in the separate results.

## Retained corrections

The first native task passed, then Antigravity updated itself from 1.2.14 to 1.2.15.
The hash guard stopped before the second launch.
That attempt remains separate from the complete matrices and counts toward the allowance.
Subsequent runs used a frozen client with self-updates disabled.

An early interpretation incorrectly treated an empty `agy mcp list` result as proof that workspace MCP was unavailable.
The full v4 trace disproved that interpretation with two successful MCP calls.
The listing command reports global configuration. Headless execution also loaded the workspace configuration in v4.
The [v5 correction](../../../.scratch/turn-reduction/native-screen-20261002/preregistration-v5-correction.json) preserves this correction without rewriting the frozen preregistration.

## Explicit instruction experiment

The user requested task-level instructions that explain when and why to use Tzro.
The installed skill described the tool, but the earlier Tzro tasks had no root `AGENTS.md`.
Its discovery description also omitted editing and verification.
That omission is a possible adoption factor, not an established cause.

The new `--tzro-guidance` option appends a declared file only to Tzro task workspaces.
The [guidance](../../../pkg/benchmark/turnreduction/guidance/tzro-agents.md) explains batching, exact edit fields, preset checks, and failure handling.
The report records its SHA-256, and the drift guard freezes its content.
Antigravity supports workspace `AGENTS.md` instructions through its [ordinary rule discovery](https://www.antigravity.google/docs/cli/gcli-migration/).

The [v6 contract](../../../.scratch/turn-reduction/native-screen-20261002/preregistration-v6.json) declared 27 fresh matched launches with unchanged tasks, grading, client, model, and threshold.
The production Tzro binary stays at the frozen v5 build.
This experiment measures guided Tzro against fresh Native and Simple controls.
It does not isolate the effect of instructions from all other integration effects.

## Paired replication

The guided screen passed, so the [v7 contract](../../../.scratch/turn-reduction/native-screen-20261002/preregistration-v7.json) reserved the final 18 launches for a matched repeat.
Simple was selected before that repeat because it was the fastest eligible v6 control.
The candidate, instructions, client, model, tasks, graders, deadline, and threshold remain frozen.
The two conditions alternate first position by task.

The native report retains its `incomplete` three-condition summary because this repeat has two conditions.
A separate paired summary requires all nine Simple and all nine Tzro tasks to pass.
It compares complete suite totals against the same 20% threshold.
It does not relabel this pair as a complete three-condition matrix.
The paired result passed the unchanged quality and 20% speed gates.
This supports a repeated benefit for the guided integration on these tasks.
It does not validate the historical custom REST benchmark claim.

## Regression evidence

The full `go test ./...` suite passed after the fixture assets received a separate module boundary.
The benchmark still executes every private grader against its subject and reference solution.
The compiled CLI test covers successful edits, failed checks, unavailable commands or presets, and preflight conflicts.
The offline 27-cell matrix exercises the production MCP service, actual helper, and private graders with scripted reference edits.
These local tests establish harness behavior and correctness, not agent performance.

The affected benchmark and CLI suites passed again after the optional guidance input was added.
A local regression covers condition-specific instructions and stops execution after guidance drift.

The [validation record](../../../.scratch/turn-reduction/native-screen-20261002/local-validation.json) lists test evidence and limitations.
The historical custom REST benchmark remains invalid as native performance evidence. Its [audit](../bugs/turn-reduction-evidence-audit.md) explains why.

## Evidence files

- [Workspace matrix and native traces](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v4/report.json), [derived analysis](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v4-analysis.json), [evidence hashes](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v4-evidence-sha256.json).
- [Global matrix and native traces](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v5/report.json), [derived analysis](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v5-analysis.json), [evidence hashes](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v5-evidence-sha256.json).
- [Guided matrix and native traces](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v6/report.json), [derived analysis](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v6-analysis.json), [evidence hashes](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v6-evidence-sha256.json).
- [Paired native report](../../../.scratch/turn-reduction/native-screen-20261002/live-pair-v7/report.json), [paired outcome](../../../.scratch/turn-reduction/native-screen-20261002/live-pair-v7-paired-summary.json), [adoption analysis](../../../.scratch/turn-reduction/native-screen-20261002/live-pair-v7-analysis.json), [evidence hashes](../../../.scratch/turn-reduction/native-screen-20261002/live-pair-v7-evidence-sha256.json).
- [Retained first attempt](../../../.scratch/turn-reduction/native-screen-20261002/live-matrix-v2/report.json) and [aggregate launch ledger](../../../.scratch/turn-reduction/native-screen-20261002/live-launches.json).

These historical comparisons used 100 authorized launches with unknown charges accepted.
They comprise 81 full-matrix tasks, 18 paired-repeat tasks, and the first retained attempt.
Every attempt passed grading. The first attempt remains excluded from matched timing comparisons.
The user then approved 127 total launches for the separate instrumented comparison, which passed all 27 additional tasks.
The shared ledger now contains 127 completed launches. All charged-dollar fields remain unknown. The allowance is exhausted.
