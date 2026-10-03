# Turn Reduction Evaluation

Status: guided native screen, matched replication, and instrumented comparison passed the 20% gate on development tasks. Last updated: 2026-10-02.

The [native screen report](turn-reduction-screen-results.md) records actual tool use, full-suite timings, and evidence limits.

## Resolved outcome

Verified Completion Time is the user-facing timing outcome. Cloud Decision Round count is a diagnostic for the proposed mechanism.
Completion rate accompanies timing results. Early failure does not count as fast completion.

With equal correctness and provider cost, a five-round, 40-second workflow beats a three-round, 80-second workflow.
Speed can justify higher provider cost. Halving completion time at twice the provider cost is acceptable in the discussed example.
No universal cost ceiling applies by default. Users can declare a usage or cost limit before a benchmark.

Provider latency must be controlled before attributing timing differences to Tzro.
Wall-clock time alone does not establish a causal benefit.
Confirmed causal speed claims require repeated, controlled evidence that Tzro caused the improvement.

The [instrumented comparison](instrumented-turn-reduction-results.md) measured 63 Tzro model invocations versus 111 Native invocations, with all tasks correct.
The [invocation instrumentation](native-invocation-instrumentation.md) requires matched hook pairs and native response events before reporting those counts.

## Domain language and evidence

The [domain glossary](../../../CONTEXT.md) defines Verified Completion Time and Cloud Decision Round.
A round can contain multiple tool calls. Provider request attempts and tool executions have separate counts.
The [workflow runner](../../../pkg/benchmark/workflow/execute.go) counts completed assistant responses separately from tool executions.
Retries and interrupted requests need separate accounting.

The reported [timing fields](../../../pkg/benchmark/workflow/types.go) contain task and tool durations but no per-request provider latency breakdown.
These fields do not isolate provider queue time or cloud computation time.
Parallel tools can overlap, so their summed durations do not establish time on the critical path.

## Agreed timing controls

The comparison uses repeated, matched runs with the same model, provider route, and task conditions.
Balanced run order can reduce bias from provider conditions that change during the comparison.
Completion time, correctness, provider cost, and cloud rounds remain separate measurements. Reports include uncertainty.

If sufficient evidence supports a timing model, that model can estimate completion time under common service conditions.
That estimate remains counterfactual. It retains the time cost of sequential calls, request size, local work, and dependency order.
Eliminated sequential calls can remove real waiting time. The comparison retains that benefit.
Actual wall-clock time and provider cost remain visible beside any modeled timing result.
Modeled timing supplies supporting evidence. A modeled result alone does not establish an observed speed improvement.

## Agreed product comparison gate

The [hypotheses catalog](../../turn-reduction-hypotheses.md) contains predicted savings, which need matched workflow evidence.
The [thesis handoff](../../../.scratch/turn-reduction-thesis.md) describes the three comparison conditions:

- **Native**: The capable agent harness with its normal tools, batching, language services, and project instructions.
- **Simple automation**: The same harness plus the smallest credible native hook, script, map, or language-tool integration.
- **Tzro**: The same harness plus the Tzro mechanism under evaluation.

The comparison preserves task conditions, grading, and relevant information access.
Native versus simple automation measures the value of providing the simple helper. Tzro versus simple automation measures incremental product value.

If simple automation matches Tzro's speed, correctness, and provider cost, the mechanism leaves Tzro's performance thesis.
This rule does not remove the capability from the product or establish that a tie already occurred.
Setup, portability, and maintenance benefits remain separate claims that need their own evidence.

## Agreed initial scope

The first validation targets Tzro Standard integrated into the native Antigravity CLI, using the existing `GEMINI_API_KEY` through the Gemini provider.
The cloud agent retains strategy and code generation. Local deterministic graph execution remains available.
Full's optional decision and extraction runtimes, and T46's replacement orchestration, remain outside this first validation.
This decision does not remove those hypotheses from the catalog.

## Product scope evidence

The [domain glossary](../../../CONTEXT.md) distinguishes Tzro Standard from Tzro Full.
Standard installs the CLI and supported agent integrations. Full adds optional proxy routing and experimental runtimes.
The [runtime configuration](../../../cmd/tzro/runtime.go) creates the graph engine without model workers unless experimental runtimes are enabled.
Graph execution therefore does not require Full's decision and extraction workers.

[ADR-0095](../../adr/0095-system1-graph-calls-and-encoder-decision-engine.md) keeps strategy and code generation with an external cloud planner.
[ADR-0096](../../adr/0096-migration-to-jev-style-and-libllama-decision-engine.md) changes the decision runtime, while retaining that boundary.
T46 proposes a local primary orchestrator with cloud code-synthesis calls. That changes workflow ownership beyond the selected initial scope.

## Existing capabilities relevant to workload selection

- **T09**: The [executor query tool](../../../pkg/executor/tools.go) ingests a provided file and runs supplied SQL in one call.
- **T39**: `tzro test` provides impact-based selection and execution through the [TestSelector](../../../pkg/context/test_selection.go).

These source and CLI checks do not establish natural adoption, selection correctness, or workflow performance.
For verification workflows, automatic scheduling and impact-based selection remain distinct treatments.
The [thesis handoff](../../../.scratch/turn-reduction-thesis.md) proposes automatic verification and cold-start orientation as the first workload comparisons.
Automatic Verification is the selected first workload.

## Agreed first workload

Automatic Verification runs local checks following source edits and delivers results automatically, including failures.
The agent does not need separate Cloud Decision Rounds to schedule checks or poll for results.
The intended mechanism replaces repeated cloud coordination with a prescribed local verification workflow.
Local checks still run. Their time and tool executions remain part of the measured workflow.
The first comparison keeps the required check set constant across native scheduling, simple automation, and Tzro.
This isolates scheduling from impact-based test selection. T39 remains a separate treatment.

T07's agent-supplied verification command describes a related composite tool, rather than the complete automatic scheduling workflow.
T29's verification packet informs result delivery. Results return with the grouped edit-and-verify operation.
Required checks come from the agreed Verification Preset. The sections below define the agreed cancellation and summary policies.

The current [native hook adapter](../../../pkg/hooks/native.go) handles privacy checks and supported output compaction.
It does not schedule tests or attach Automatic Verification results.
The [Pi extension](../../../pkg/hooks/pi_extension.ts) also processes tool results for compaction and exposes graph execution.
These observations describe current repository code. They do not establish all capabilities of the upstream clients.

## Agreed edit boundary

The boundary is explicit: one edit request lists the related patches, which can modify multiple files.
The request defines the submitted batch. Completion of that request needs no separate cloud call to close the batch.
Verification follows completion of the submitted edits.
This boundary does not establish that the entire intended change or task is complete.

File-save events and idle timers do not establish the end of an intended multi-file change.
Completion of an individual edit tool establishes completion of that invocation, rather than all related future edits.
The current hooks expose individual tool results. The [graph API](../../../pkg/executor/graph.go) exposes declared execution dependencies.
Neither provides an implicit marker for the agent's finished-editing intent.

The first design uses a grouped edit-and-verify operation whose submitted edit list defines the boundary.
`edit_and_verify({ edits: [...] })` is the illustrative interface name, not an existing product tool.
The current [builtin dispatcher](../../../pkg/executor/tools.go) has no dedicated grouped patch-and-verify tool.
Generic shell tools can express edit commands, but do not provide that dedicated edit contract.

The tradeoff is adoption: the agent must use the grouped operation to obtain this batch guarantee.
Ordinary separate edits do not silently acquire a reliable semantic batch boundary.
Natural adoption remains part of workflow validation. A forced tool-use demonstration does not establish natural use.
The user accepted this explicit grouped operation as the first design's batch boundary.

## Agreed check failure policy

If the batch applies successfully but a required check fails, the operation retains the edits and returns failure diagnostics.
The cloud agent can repair the current change without reapplying the previous batch.
T19 now reflects this decision, superseding its earlier automatic rollback proposal for failed checks.
Batch Rejection before writes and failure during writes are separate cases.

## Agreed application preflight

The operation verifies that every submitted patch can apply before writing any batch edits.
If a patch cannot apply, the operation rejects the submitted batch and reports the conflict without applying the other patches.
This policy concerns a detectable patch conflict before application.
The write-time failure and cancellation policies below cover observed interruptions. Detailed crash recovery and concurrent external edits need implementation planning.
Preflight validation does not establish atomic writes across all files.

## Agreed write-time failure policy

The first design preserves the observed workspace state after a write-time failure and reports which batch files changed.
It reports Partial Application and any uncertain file state. Verification is marked as not run.
The operation does not automatically restore the earlier contents.
Detailed crash recovery and concurrent external edits remain implementation planning topics.

## Agreed required check configuration

Required checks come from a repository-defined Verification Preset, reused for every grouped operation.
The agent does not select arbitrary required check commands within an Edit Batch.
The first comparison uses the same preset across native scheduling, simple automation, and Tzro.
The [domain glossary](../../../CONTEXT.md) defines Verification Preset.

The existing [repository context configuration](../../../pkg/context/config.go) defines test-file conventions and a command allowlist.
It does not define a required verification preset.
The existing [TestSelector](../../../pkg/context/test_selection.go) constructs commands from selected targets and available test frameworks.
Impact-based test selection remains separate from the first scheduling comparison.

## Agreed required-check concurrency

Run required check commands sequentially by default. Allow parallel groups when the Verification Preset explicitly declares them safe to run together.
This policy concerns separate check commands; each command retains its configured internal scheduling.
Apply the same preset concurrency policy across all three benchmark conditions.
Continue independent checks after failure as previously agreed. Sequential execution does not make one independent check a prerequisite of another.
The existing [TestSelector](../../../pkg/context/test_selection.go) executes its targets sequentially.
The [graph executor](../../../pkg/executor/engine.go) supports concurrent nodes with a default limit of four.
The agreed scheduling policy uses sequential commands by default and explicit preset opt-in for parallel groups.
The grouped operation still needs implementation.
The user accepted sequential defaults and explicit preset opt-in for parallel groups.

## Agreed timeout and delivery

The first design uses a five-minute default timeout. Users can configure a different value for jobs that need longer runtimes.
The user explicitly acknowledges that this reverses the earlier waiting decision.
The configured timeout is a cutoff, rather than another wait interval. Local waiting cannot automatically extend it.
This supersedes the earlier direction of progressively extending waits to accommodate unknown runtimes.

The operation waits for verification and returns its result through the original tool call.
The user rejects delayed final results after that call returns, citing harness compatibility.
This records the user's design constraint. It does not establish a measured capability claim about all agent harnesses.
On timeout, verification stops and returns an incomplete result with available diagnostics. Applied edits remain available for repair.
An elapsed timeout does not establish a deadlock or an assertion failure.
One shared timeout covers the entire operation: patch validation, edit application, and required checks.
Each stage uses the remaining budget. Starting another check does not reset the timeout.
If the timeout interrupts edit application, the existing Partial Application policy applies when edits remain partly applied.

The existing [Pi integration](../../../pkg/hooks/pi_extension.ts) limits graph and direct CLI execution to 60 seconds.
That shorter limit needs adjustment before it can support the proposed five-minute default or a longer configured limit.
Tzro configuration alone cannot override a shorter timeout imposed by the client integration.
The current [shell dispatcher](../../../pkg/executor/tools.go) waits for process exit and captures output without an adaptive waiting schedule.
On Unix, the [shell executor](../../../pkg/executor/shell_unix.go) cancels the process group when its context is cancelled.
The [MCP graph handler](../../../cmd/tzro/mcp.go) reports graph start and completion, without intermediate check progress or an adaptive wait policy.

## Agreed waiting ownership

Tzro owns routine waiting locally, without cloud decisions to keep waiting before the configured timeout.
Process exit supplies the result promptly before the timeout. A status interval does not delay completion delivery.
The agent receives the result through the original operation, including incomplete verification when the timeout expires.
The cloud agent participates when an exception requires its judgment.

The current [MCP server](../../../cmd/tzro/mcp.go) handles requests serially and awaits graph execution before reading the next request.
It has no detached verification job or automatic delivery of a final result after the request returns.
This describes the repository implementation, rather than all capabilities of the upstream agent clients.

## Agreed timeout scope

The user selected one timeout budget for the whole grouped operation.
Three sequential checks share the configured budget. Users can increase the overall limit for a longer workflow.

## Agreed check failure scheduling

Independent required checks continue after one check fails, within the remaining operation budget.
Checks that require a failed prerequisite are blocked and reported as not run. A blocked check does not count as a pass.
This returns more repair evidence in one result, but can spend additional local time after failure is already known.
Expiry of the shared timeout stops further verification. Earlier failed and completed checks remain evidence in the returned result.

The existing [TestSelector](../../../pkg/context/test_selection.go) continues through its selected targets after a nonzero exit.
The [graph executor](../../../pkg/executor/engine.go) blocks dependent nodes after failure unless they explicitly accept failed dependencies.
These are relevant capabilities. The proposed edit-and-verify operation is not yet implemented.

## Agreed minimum result evidence

Every result includes a Verification Summary in the original response, including when full logs exceed its size target.
The summary distinguishes application state from verification state, and records passed, failed, blocked, and timed-out checks.
Known failure messages and source locations accompany check identifiers. Unknown diagnostic details remain explicitly unknown.
Full logs can use expansion artifacts. Any omitted detail has an explicit notice and an available retrieval reference when storage succeeds.
Expansion can provide additional detail, but is not required just to discover which checks failed and their primary diagnostics.
The summary is mandatory. Grouping for large failure sets and the soft inline target are agreed below.
The [domain glossary](../../../CONTEXT.md) defines Verification Summary.

The current [selected graph result](../../../cmd/tzro/graph_result.go) removes outputs and return values when the response exceeds 8,000 bytes.
It then supplies an expansion pointer, and can also remove node statuses when they exceed the limit.
This fallback does not guarantee that primary failure diagnostics remain in the original response.
The [compaction evidence model](../../../pkg/compactor/evidence.go) already represents diagnostic identifiers, messages, locations, exit confidence, and omitted spans.
These fields can inform the proposed result contract. Their existence does not establish the proposed operation's end-to-end evidence guarantee.

## Agreed summaries for large failure sets

When many tests fail, return every required check command's outcome, known failure counts, and representative primary diagnostics in the summary.
Group repeated diagnostics where the evidence supports grouping. Include available messages, representative identifiers, and source locations.
Retain complete parsed diagnostics and full logs as expansion evidence. Make omitted details explicit, with a retrieval reference when retention succeeds.
Leave failure counts unknown when the native output does not establish them.
The summary selects from retained evidence; it does not replace the Compaction Evidence Contract with a lossy diagnostic parser.
The current [compactor formatter](../../../pkg/compactor/evidence.go) prints every diagnostic and caps each diagnostic's output at ten lines.
That per-diagnostic cap does not bound the combined output from thousands of failures.
The user accepted grouped and representative diagnostics for large failure sets.

## Agreed inline size target

Use a configurable 8,000-byte target for the inline Verification Summary.
Keep application state, each required check command's outcome, and representative primary failure diagnostics in the response.
Use grouping and expansion to reduce verbose diagnostic detail within the target.
The target is soft for mandatory evidence. If mandatory fields alone exceed it, retain those fields inline and allow a larger response.
This uses the current graph response's size threshold as a starting point, while preserving the agreed evidence requirements.
The user accepted the configurable default target and the mandatory-evidence exception.

## Agreed harness cancellation policy

When the harness cancels an operation that is still running, stop further edits and checks, and cancel its owned check process groups.
Keep edits already applied. If application is incomplete, report observed Partial Application and uncertain state using the agreed write-time policy.
Retain outcomes from completed checks. Cancellation does not change an earlier reported check pass into a failure.
The unfinished operation is incomplete. Return its summary when the original caller can still receive the result.
If the caller has disconnected, retain the last observed state locally when possible. Final-result delivery remains within the original tool call.
The existing [Unix shell executor](../../../pkg/executor/shell_unix.go) cancels its shell process group on context cancellation.
The [MCP server](../../../cmd/tzro/mcp.go) handles each request before reading the next; active-call cancellation needs a delivery path.
The user accepted this cancellation policy. Detailed crash recovery and concurrent external edits remain implementation planning topics.

## Agreed native check outcomes

The operation treats the native check's reported outcome as authoritative for that check.
Detected source changes during execution do not automatically invalidate a passing outcome or change verification to incomplete.
The user rejects the proposed source freshness gate. The first design does not require an isolated snapshot or input fingerprint veto for success.
Successful verification requires an available required check set whose checks all report success.
Timeout and checks that did not run retain their agreed incomplete, blocked, or unavailable outcomes.
Task correctness remains independently evaluated in the workflow comparison.

The current [TestSelector](../../../pkg/context/test_selection.go) labels its selection scope but executes current worktree files.
It warns that staged selection with unstaged changes does not verify the exact staged snapshot.
The [session check model](../../../pkg/session/session.go) has per-check file hashes and configuration or dependency fingerprints.
These existing provenance structures do not impose a new success gate on the proposed Automatic Verification operation.

## Agreed missing or unavailable verification

Missing verification configuration or an inability to run checks does not block valid batch edits.
Patch preflight and write-time failure policies still apply. The operation applies valid edits within the shared timeout.
The mandatory Verification Summary reports application and verification separately, including the reason checks could not run.
An applied batch can therefore have verification that is not configured or unavailable. Neither state reports a verification pass.
The harness agent can explain the missing setup to the user. The operation does not guess replacement required commands for each batch.
The user selected this behavior over rejecting edits because verification setup is missing.
Here, truthful reporting means actual edit and check outcomes, rather than a single success value that hides unavailable verification.

The existing [context configuration loader](../../../pkg/context/config.go) supplies defaults when its configuration file is absent.
Those defaults do not define required verification commands. They cannot establish a valid Verification Preset for the proposed operation.

## Agreed first client and authentication

The [thesis handoff](../../../.scratch/turn-reduction-thesis.md) names Antigravity as the original assessment target. Earlier paid trials used Pi.
The user selected `GEMINI_API_KEY` on 2026-10-02, superseding the earlier cached-account authentication choice.
Use the key in the native CLI's environment and configure provider `gemini` in each isolated client home.
The CLI does not load `.env`; the launch wrapper must load the key explicitly when that is its source.
The native CLI remains the selected first execution surface; IDE claims require separate evidence.
The current [Standard installation](../../installation.md) configures an Antigravity skill and local MCP server registration.
Its native post-tool hook does not replace tool results. The grouped operation would need to return its own result through an exposed tool.
The proposed grouped tool is not yet implemented. Registration alone does not establish tool adoption or a workflow benefit.

## Antigravity model access: authentication updated 2026-10-02

Antigravity owns model access while Tzro supplies local tools. The chosen authentication now uses the existing Gemini API key.
The earlier account-route model discovery does not prove the selected model is available on this API route.
The current [installer](../../../pkg/hooks/setup_clients.go) registers a local MCP process, and the [Standard engine](../../../cmd/tzro/runtime.go) does not enable model workers.

The [authentication documentation](https://antigravity.google/docs/cli/install/) requires `modelProvider: "gemini"` with the exported key for this route.
Configure `.gemini/antigravity-cli/settings.json` inside the isolated home. Keep the user's global settings and credential files unchanged.
The key's presence alone does not prove credential validity or requested-model access. Verify these under the declared execution allowance.

The current [workflow runner](../../../pkg/benchmark/workflow/execute.go) launches Pi with a provider configuration and benchmark API key.
It does not run Antigravity. Native Antigravity validation needs a client adapter or an explicit native workflow protocol.
The native CLI is selected for validation, but the required runner adapter and proposed grouped tool are not yet implemented.
The target client, native CLI surface, Gemini API-key authentication route, and Low model variant are agreed. Do not fall back to account sign-in or another model.

## Agreed model control

The user selected `gemini-3.8-flash-low`, their usual Antigravity model.
All three comparison conditions use that exact model and the same Low reasoning variant.
The installed Antigravity CLI reports version 1.2.14. Its read-only `agy models` command listed the selected model and exited successfully on 2026-10-01.
This confirms model discovery, rather than a successful inference or activation of Tzro tools.
The model-list command required sandbox access for the CLI's local listener and log files. No model prompt was sent.
The official [headless documentation](https://antigravity.google/docs/cli/headless/) describes available-model discovery and explicit model and effort selection.
It also documents a five-minute default timeout for an entire headless run.
On 2026-10-01, installed CLI version 1.2.14 advertised a zero default in `agy --help`, with zero meaning wait until completion.
The published default and local help disagree. No inference run has tested the effective default.
Set the whole-task limit explicitly for the comparison. This limit differs from the agreed timeout for each edit-and-verify operation.

## Agreed first execution surface

The first comparison uses the native Antigravity CLI for all three conditions.
It documents explicit model selection and machine-readable output for repeated comparisons.
The installed CLI has discovered the selected model. The proposed tool and benchmark adapter still need implementation and validation.
CLI results apply to the tested CLI workflow. Claims about the IDE need corresponding IDE workflow evidence.
The CLI's reported user-turn counter is distinct from the agreed Cloud Decision Round metric; its instrumentation needs separate accounting.

## Agreed net speed threshold

The complete enabled Tzro workflow requires at least 20% lower suite total of Verified Completion Times than the fastest correct native or simple automation comparison.
Each suite total sums the nine task completion times. The comparison must preserve correctness and report completion rates.
The initial screen compares one observed total per condition. Planned follow-up repetitions use median suite totals.
The threshold applies to the net workflow result, including local overhead and component interactions.
Individual improvements do not each need a 20% gain. Several smaller improvements can contribute to the final result.
The combined workflow is measured directly. Adding component percentages does not establish its net improvement.
Matched repeats and uncertainty still need to support a confirmed causal speed claim. A favorable single pass supplies preliminary timing evidence.
The user accepted 20% and clarified its net scope. The threshold does not require implementing every catalog entry.
Provider cost remains a separate measurement. Optional usage or cost limits are declared before a run; the speed threshold does not establish a cost ceiling.
The [domain glossary](../../../CONTEXT.md) includes enabled-component overhead in Verified Completion Time.

## Agreed provider cost reporting

The official [headless result schema](https://antigravity.google/docs/cli/headless/) documents token counts, but no dollar-cost field in the result envelope.
The current choice uses the Gemini API route rather than account-plan quotas. Token counts alone do not establish actual monetary charges.

Reports separate observed token usage, available provider usage evidence, and any actual charge evidence.
Monetary cost remains unknown without reliable charge evidence. It is not inferred as zero or priced using the earlier OpenRouter route.
Any estimates or reservation ceilings must identify their Gemini API basis separately from actual charges.
Usage supplies a cost diagnostic, rather than a dollar-saving claim. Token reductions alone do not establish dollar savings.

## Agreed initial language scope

The first complete comparison includes Go, Python, and TypeScript edit-and-verify tasks from the start.
The user selected this scope instead of starting with Go alone.
All three comparison conditions need the same task inputs and required checks within each language.
Native verification results remain distinct from independent task correctness grading.

The existing [representative fixtures](../../../pkg/benchmark/workflow/tasks_representative.go) and [realistic fixtures](../../../pkg/benchmark/workflow/tasks_realistic.go) include Go coding tasks and graders.
The current [grader](../../../pkg/benchmark/workflow/execute.go) invokes Go tests and protects Go test files and module configuration.
It supports injecting private grading files into a copy of the final workspace.
Python and TypeScript need corresponding fixtures, native grading commands, and protection of grading inputs.
The Antigravity runner and grouped operation also still need implementation and validation.
Results support the tested workflows and build systems; language coverage alone does not establish general performance across every project.

## Agreed initial task mix

The initial suite has three task shapes in each language: a single-file bug fix, a multi-file change, and diagnosis of an existing test failure.
This gives nine initial cases, each repeated across the native, simple automation, and Tzro conditions.
Prompts state the task without prescribing tool use or requiring a fixed sequence of edits and failures.
Independent grading checks the requested behavior, separately from the agent's reported verification results.
The user accepted this task mix and initial case count. Concrete fixtures and language-specific grading details still need definition.

## Agreed speed threshold scope across languages

The net 20% speed threshold applies to the combined nine-case suite, with per-case and per-language results reported separately.
A suite-level improvement would not establish that every language or task improves.
Language-specific slowdowns remain visible, and claims must identify where the benefit holds.
The user selected the combined gate. Each language does not need to meet the 20% threshold separately.
Correctness and completion rates still accompany the timing result.
The current [report generator](../../../scripts/generate_benchmark_report.py) reports pooled cell latency medians and a separate token-savings gate.
Those existing outputs do not implement the agreed speed evaluation.

## Agreed suite timing calculation

For each suite pass, sum the nine task Verified Completion Times separately for each comparison condition.
Its mean task completion time is the total divided by nine.
With the same nine cases in every condition, comparing means gives the same percentage reduction as comparing totals.
This does not average per-task percentage reductions, which would change the agreed weighting.
The initial screen compares observed suite totals. Planned follow-up repetitions compare median suite totals as previously agreed.
The comparator is the lower eligible total from native and simple automation.
The gate requires the Tzro total to be at most 80% of that comparator total.
For a 1,000-second comparator suite, this means 800 seconds or less.
Longer tasks contribute more to this calculation because it measures seconds saved across the agreed task mix.
Per-task and per-language results expose the distribution of gains and slowdowns.
Failed attempts do not supply a Verified Completion Time or count as fast completion. Their outcomes remain in completion-rate reporting.
The user accepted this calculation and the comparator eligibility rule below.

## Agreed initial correctness gate

Require all nine Tzro tasks to finish correctly with required checks complete in every planned pass before this initial evaluation can pass.
A final incorrect result or timeout prevents a passing claim, even when the other tasks exceed the speed threshold.
Keep unsuccessful attempts in the declared result matrix. Do not replace them with successful reruns or omit them to improve the timing result.
An intermediate failed check that the agent repairs within the same task run is part of that task's completion time, not a final task failure.
The user accepted this strict correctness gate for the initial suite.

## Agreed initial screen budget

Use one matched pass of the nine-case suite across native, simple automation, and Tzro: 27 task runs.
The user rejected the proposed six repetitions, or 162 task runs, as too expensive for the initial evaluation.
Report mean completion time across the nine cases in each condition alongside the equivalent suite total.
Interleave conditions within each case instead of completing an entire condition's suite before starting the next condition.
Balance execution positions across cases and retain the observed condition order.
One pass measures the selected task mix but does not measure repeat variation within a task.
The initial result is a screen. Select confirmation scope and uncertainty methods for any separately budgeted follow-up campaign.

## Agreed comparator eligibility

A native or simple automation condition supplies an eligible speed comparator only when all nine tasks finish correctly with required checks complete.
Apply this requirement to every planned pass of that condition.
If one comparator fails and the other completes correctly, use the complete comparator and retain both conditions' outcomes.
If neither comparator completes correctly, report the correctness results without a passing comparative speed claim.
Do not construct a faster comparator by omitting failed tasks or selecting a different condition for each task.
The user accepted this comparator eligibility rule.

## Agreed whole-task time limit

Use a ten-minute default ceiling for each complete agent task, identically across native, simple automation, and Tzro.
Users can configure a different whole-task limit when launching the benchmark.
Select the limit once for the run, apply it to all three conditions, and record its value in the report.
The native CLI exposes `--print-timeout`; the Antigravity runner must pass the selected value explicitly for each task.
The five-minute default still applies to each grouped edit-and-verify operation, subject to the remaining whole-task budget.
The larger task ceiling leaves time for discovery, edits, and repair around verification.
This is a maximum wait, not a required runtime. Completed tasks return promptly.
The user accepted ten minutes and required configuration as part of running the benchmark.
The existing [Pi benchmark command](../../../cmd/tzro/bench_workflows.go) already exposes a per-task `--timeout` flag with a three-minute default.
That existing flag is not an implemented Antigravity adapter or the new ten-minute default.

## Agreed simple automation direction

The user prefers a bulk-update script as the credible simple alternative, citing function renames across many references.
This supersedes the proposal for a generic grouped edit-and-verify wrapper as the required simple automation interface.
Verification uses the shared native check commands. The agent can combine those checks with a script invocation when useful.
The comparison preserves the agent's natural choice of tools and command batching.
Native also retains the ability to write bulk-update scripts during a task. Creating or adapting those scripts contributes to task completion time.
The current [host-tool discovery](../../../pkg/benchmark/workflow/tools.go) preserves ordinary executable access for the native workflow.
It does not restrict native editing to separate manual changes.

## Agreed simple automation helper availability

Provide simple automation with an existing generic bulk-edit helper before the task begins.
The agent supplies the edits and their scope; the helper contains no fixture-specific solution.
Native can still create its own scripts with its ordinary tools during the task.
This distinguishes access to an existing helper from ordinary native scripting. Record helper setup effort separately from task execution.
The user accepted providing the helper before the task starts.
This gives simple automation the opportunity to skip authoring its own script during the timed task.
Actual time and cloud-round savings still depend on adoption and the task. They are measured, not assumed.
The initial screen still has 27 task runs. No added repetitions follow from this decision.

## Agreed visible test editing policy

A function rename can require updating references in visible tests as well as production code.
The current [Go grader](../../../pkg/benchmark/workflow/execute.go) rejects every change to a fixture's Go test files and module configuration.
That rule can reject a correct rename when a test calls the renamed function.
Allow visible test updates when the requested change requires them, while keeping independent grading inputs fixed.
Declare the same editable and protected inputs for all three conditions before the run.
The agent still has to satisfy the independent task grader. A reported native check pass alone does not establish task correctness.
The user accepted visible test edits when the requested change requires them.
The native benchmark now enforces each fixture manifest's editable-file policy and grades a separate final-workspace copy.

## Implementation and evidence status

The [native runner](../../../pkg/benchmark/turnreduction/native.go) now launches the installed Antigravity CLI.
It uses isolated Gemini settings, the production Standard MCP installation, and the actual Simple helper.
The default `tzro bench turn-reduction` command performs local readiness checks without model requests.
Live execution requires explicit authorization, unknown-cost acceptance, and a persistent launch allowance.

The [validation repair report](../bugs/turn-reduction-evidence-audit.md) explains why the historical speed claim is unsupported.
The old custom Gemini REST loop has been removed from this benchmark.
Historical result files remain unchanged.

Nine versioned manifests define prompts, editable files, reference edits, and required private tests.
All starting fixtures fail grading. All reference solutions pass the prescribed checks and private tests.
The complete scripted-client matrix passed 27 cells through the real guard, helper, MCP service, and graders.
Scripted timings cannot establish a product speedup.

One monotonic deadline covers client execution, final required checks, and isolated private grading.
Failed or incomplete cells have no Verified Completion Time.
The report requires all nine Tzro tasks and an eligible complete control suite before comparing time.
Native user-turn counts are not treated as Cloud Decision Rounds.

The user authorized one live screening matrix of 27 launches with unknown charges accepted.
The [frozen contract](../../../.scratch/turn-reduction/native-screen-20261002/preregistration-v2.json) records the schedule, limits, cache policy, and stop rules.
The previous OpenRouter campaign remains separate and paused.
Repeated confirmation and withheld-task validation require another declared plan.

## Agreed initial fixture and grading approach

Use small, self-contained projects with realistic source structure, visible tests, and fixed dependency versions.
Cover the agreed three task types in Go, Python, and TypeScript, for nine cases in total.
Include a multi-file function rename with references across modules and tests as one bulk-edit case.
Prompts define expected behavior and compatibility requirements without prescribing tool use or an edit sequence.
Before model runs, confirm that each starting fixture fails its task-specific grader and that a reference solution passes.
Grade the final workspace with private behavior tests and regression checks, independent of the agent's reported check result.
The grader checks observable requirements, rather than matching a reference patch or requiring a particular implementation.
Keep task requirements explicit; private tests must not introduce undisclosed requirements.
Use identical fixture revisions, editable inputs, protected grading inputs, and grading commands across all three conditions.
The user accepted this fixture and grading approach. The nine versioned fixture contracts now implement this approach.

## Agreed timing and initial-result interpretation

Measure Verified Completion Time with one monotonic timeline from task launch through independent grading completion.
Include CLI startup, provider waiting, local tool work, required checks, and task-specific initialization on the elapsed path.
Report installation and pre-task fixture preparation separately, as agreed for the generic helper.
Report agent duration and independent grading duration separately alongside the complete measured time.
Record observable provider and tool spans. Overlapping spans do not add extra elapsed time.
If the client does not expose a timing segment, report it as unknown. Do not infer provider waiting by subtracting tool durations.
Keep actual elapsed time as the primary metric without invented latency weights.
Use matched conditions and balanced order to reduce provider confounding; retain common-provider timing models as supporting evidence only.
Count observable cloud responses separately from tool calls and request attempts. Do not use user-turn counts as cloud-response counts.
The native runner records preparation, agent, grading, and whole-task durations separately.
Report a single-pass 20% result as an observed initial screen, without a confirmed causal claim or repeat-based uncertainty estimate.
Any confirmation campaign needs a separately declared repeat count and budget; the screen does not trigger more paid runs automatically.
The user accepted this measurement and interpretation policy. Select repeat counts and uncertainty methods for any separately budgeted confirmation campaign.

## Agreed speed and cost reporting policy

Keep the 20% speed screen separate from usage and cost outcomes. Do not combine them into one weighted score.
An observed speed result can pass with higher usage, provided the report makes that increase explicit.
For example, report 20% faster completion with 10% more tokens as a speed gain with higher token usage.
Do not describe this result as cost savings or an unqualified improvement across all measures.
Users can declare an optional maximum usage or cost increase when launching a benchmark.
Name the selected measure and limit before the run, and report its outcome separately from the speed screen.
No universal cost ceiling applies by default. The accepted half-time, twice-cost example remains a valid preference rather than a rule for every run.
When reliable monetary evidence is unavailable, monetary cost and any monetary-limit outcome remain unknown.
The user accepted this policy. Speed, usage, and monetary outcomes remain separate claims with their own evidence.

## Example dialogue

Developer: "Tzro used three rounds. The alternative used five. Did Tzro win?"

Domain expert: "Both results were correct and had the same cost. Tzro took 80 seconds. The alternative took 40 seconds."

Developer: "Then the alternative won on Verified Completion Time. Round count alone did not establish a benefit."
