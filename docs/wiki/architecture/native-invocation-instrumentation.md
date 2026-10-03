# Native model invocation instrumentation

Date: 2026-10-02. Status: implemented and verified in all 27 live tasks.

The [completed comparison](instrumented-turn-reduction-results.md) passed: Tzro used 37.69% less time and 43.24% fewer model invocations than Native.
All tasks passed, with hook counts matching native response events throughout.

The benchmark now installs a passive recorder for native model invocation boundaries.
It separates model-invocation accounting from tool execution counts.
The earlier [screen results](turn-reduction-screen-results.md) retain unknown cloud-round counts because those runs lacked these hooks.

## Measurement

The recorder observes `PreInvocation`, `PostInvocation`, and `Stop`.
Antigravity documents these events and invocation identifiers in its [hook contract](https://antigravity.google/docs/hooks/).
Each event records the conversation identity, invocation number, trajectory position, and timestamp.
Stop metadata records the termination reason, idle state, and whether an error occurred.

The parser requires consecutive invocation numbers from zero and exactly one pre/post pair for each invocation.
It binds the sequence to the native event stream's conversation identity.
It also requires an idle terminal Stop and a successful native terminal result.
The hook count must equal the deduplicated completed `agent_response` count from the same native conversation.
A disagreement leaves the cloud-round total unknown.
Missing, duplicate, mismatched, or interrupted evidence leaves the round count unknown.
An enabled observer with incomplete evidence stops the matrix before another launch.

Complete evidence supplies `events.cloud_decision_rounds` as the completed main-agent model invocation count.
The report retains the detailed `events.native_invocations` summary separately.
This measurement follows the client invocation boundary.
It does not count internal HTTP retries, auxiliary model calls outside that boundary, or provider request attempts.
`events.provider_requests` remains unknown.

Invocation intervals use recorder timestamps at the pre/post boundaries.
These intervals include hook dispatch and native client work around the model call.
They do not isolate provider queue time or computation time.
The parser rejects reversed timestamps and intervals that contradict the measured agent duration.
Verified Completion Time retains its monotonic task timer and includes all enabled observer overhead.

## Isolation and configuration

The [standalone recorder](../../../cmd/tzro-invocation-recorder/main.go) uses a small compiled Go executable.
It returns an empty JSON object and does not inject steps or change execution policy.
Only allowlisted metadata reaches the append-only evidence file.
Prompt text, response text, tool arguments, credentials, and transcript contents are not copied.
File locks and synchronized writes preserve complete records across concurrent writers.

The benchmark option is `--invocation-recorder <absolute-path>`.
The CLI resolves a relative input path before the benchmark starts.
Every condition receives the same observer in its isolated global hook configuration.
The installer preserves existing product hooks.
The experiment keeps the production Tzro binary and explicit guidance from the previous screen unchanged.

Each task retains these additional evidence files:

- `invocations.ndjson`: observed lifecycle metadata.
- `invocation-summary.json`: sequence completeness, counts, intervals, and errors.
- `client-config/hooks.json`: the exact observer and product hook configuration.

The frozen benchmark contract includes the recorder binary's SHA-256.
The report also records `invocation_recorder_sha256`.
Source or recorder drift stops subsequent launches through the existing contract guard.

## Local checks

Recorder tests cover passive output, metadata filtering, invalid payloads, and concurrent durable writes.
Parser tests distinguish two model invocations from multiple trajectory steps.
They reject missing stops, missing completions, duplicate or skipped counters, wrong identities, reversed timestamps, and error termination.

The integration test compiles and invokes the actual recorder through generated hook commands in all three conditions.
It observes two model invocations independently of four tool calls and retains prescribed checks and private grading.
Another test proves that missing hooks stop a multi-task matrix after its first retained attempt.
The response cross-check rejects mismatched hook and native-stream counts.
These local tests use a scripted client. The subsequent live matrix verified native hook delivery in all 27 tasks.

The full Go suite passed. Recorder race tests also passed.
After adding the native-response cross-check, the affected benchmark and CLI suites passed again.
Offline readiness passed for the frozen client, toolchains, fixtures, and graders.
The [validation record](../../../.scratch/turn-reduction/instrumented-screen-20261002/local-validation.json) links the retained test artifacts.
A local calibration measured 100 hook process executions, including shell startup and synchronized writes.
The median was 7.99 ms, the 95th percentile was 11.02 ms, and the maximum was 314.53 ms.
These are local calibration values, not observed per-hook overhead during a live model run.
The primary task timer includes all observer overhead without subtraction.

## Instrumented comparison

The user requested another full Native/Simple/Tzro comparison.
The [E070 contract](../../../.scratch/turn-reduction/instrumented-screen-20261002/preregistration.json) fixes nine tasks per condition, strict grading, and the 20% speed threshold.
All conditions receive the same observer. Tzro retains the successful explicit `AGENTS.md` guidance.
The first live task established that the frozen native client delivers the documented hooks.
The runner stops before another task if that evidence is incomplete.

The user approved increasing the aggregate allowance from 100 to 127 launches.
The [authorization record](../../../.scratch/turn-reduction/instrumented-screen-20261002/authorization.json) retains that approval separately from the frozen preregistration.
The ledger retains all 127 completed attempts, including the previous 100.
All 27 new tasks passed with matched hook and native response counts.
The full comparison remains separate from earlier uninstrumented timings.
The result reports time, completed main-agent invocations, tool executions, correctness, and usage separately.

The [implementation plan](../../../.scratch/turn-reduction/invocation-instrumentation-plan.md) and [candidate record](../../../.scratch/turn-reduction/instrumented-screen-20261002/candidate-v2.json) preserve the design and frozen inputs.
