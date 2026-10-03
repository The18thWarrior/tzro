# Turn Reduction Hypotheses Catalog

> **Core Value Proposition**: *Shorten the time to a correct, verified result by removing avoidable cloud coordination.*

## Agreed Evaluation Direction (2026-10-01 to 2026-10-02)

Verified Completion Time is the user-facing timing outcome. Cloud Decision Round count is a diagnostic for the proposed mechanism.
Measure elapsed time from task launch through independent grading completion, including CLI startup and all task-specific work on the elapsed path.
Report agent and grading durations separately. Report observable provider and local tool spans without adding overlapping durations.
Unavailable timing segments remain unknown. Do not estimate provider wait by subtracting tool durations or apply invented latency weights.
Installation and pre-task fixture preparation are reported separately from completion time.
With equal correctness and provider cost, a five-round, 40-second workflow beats a three-round, 80-second workflow.
Completion rate accompanies timing results. Early failure does not count as fast completion.
Every planned Tzro pass must finish all nine tasks correctly with required checks complete before the initial evaluation can pass.
A final incorrect result or timeout prevents a passing claim. Unsuccessful attempts remain in the result matrix without replacement by successful reruns.
Failed checks repaired within the same task run contribute to its completion time and do not count as final task failures.
Native and simple automation qualify as speed comparators only when all nine tasks finish correctly with required checks complete in every planned pass.
If only one comparator qualifies, use it and retain both conditions' outcomes. If neither qualifies, report correctness without a passing comparative speed claim.
Do not omit failed tasks or mix comparator conditions by task to construct a faster baseline.

Speed can justify higher provider cost. Halving completion time at twice the provider cost is acceptable in the discussed example.
Provider latency must be controlled before attributing timing differences to Tzro.
Wall-clock time alone does not establish a causal benefit. No universal cost ceiling applies by default.
Confirmed speed claims require repeated, matched comparisons with the same model, provider route, and task conditions.
Comparisons balance run order and report uncertainty. Completion time, correctness, provider cost, and cloud rounds remain separate measurements.
The initial screen uses one matched suite pass per condition: nine cases across three conditions, or 27 task runs.
The user rejected six initial repetitions as too expensive.
Each complete agent task has a ten-minute default ceiling, configurable when launching the benchmark.
One selected task limit applies to all three conditions and is recorded in the report. It is separate from the edit-and-verify operation timeout.
Sum the nine task Verified Completion Times separately for each condition. Its mean task completion time is that total divided by nine.
Comparing these means gives the same percentage reduction as comparing suite totals. Do not average per-task percentage reductions instead.
The practical speed threshold is at least 20% lower suite total for the complete enabled Tzro workflow.
For the initial screen, compare the observed Tzro suite total with the lower total of the correct native and simple automation conditions.
If follow-up repetitions are planned, compare median suite totals as previously agreed.
The Tzro total must be at most 80% of that comparator total. Correctness and completion rates accompany the timing result.
One pass provides initial timing evidence without measuring repeat variation within a task. A 20% result is an observed initial screen, not confirmed causation.
Any confirmation campaign needs a separately declared repeat count, uncertainty method, and budget. The screen does not trigger further paid runs automatically.
This is a net workflow threshold, including local overhead and interactions. Individual improvements do not each need a 20% gain.
The 20% gate applies to the combined nine-case suite. Each language does not need to meet the threshold separately.
Per-task and per-language results expose slowdowns. A suite-level win does not establish improvement in every language or task.
The combined result is measured directly. Component percentages are not added to claim a net improvement.
For the selected Antigravity Gemini API route, reports separate observed token usage, available provider usage evidence, and actual charge evidence.
Monetary cost remains unknown without reliable charge evidence. Token reductions alone do not establish dollar savings.
The 20% speed screen and usage or cost outcomes are reported separately, without a combined weighted score.
A speed result can pass with higher usage when that increase is explicit. It does not establish cost savings or improvement across all measures.
Users can declare an optional maximum usage or cost increase at benchmark launch, naming the measure and limit before the run.
Report that limit's outcome separately from the speed screen. A monetary limit remains unknown without reliable monetary evidence.
Timing models under common provider conditions supply supporting, counterfactual evidence.
The comparison retains waiting time saved by eliminating sequential service calls.

A Cloud Decision Round can contain multiple tool calls. Tool calls and provider request attempts have separate counts.
The current workflow runner counts completed assistant responses separately from tool executions.
Retries and interrupted requests need separate accounting.

The numerical savings in this catalog remain predictions until matched workflow evidence supports them.
Comparisons include the capable native workflow, simple native automation, and the Tzro mechanism under evaluation.
The native comparison retains normal batching and language tools.
It can create bulk-update scripts during a task and combine edits with native verification commands in one invocation.
Script creation and adaptation during a task contribute to its completion time.
The simple automation direction uses a bulk-update script, such as applying a function rename across references.
Verification uses the shared native checks. The agent can combine those checks with the bulk updates.
Simple automation receives an existing generic bulk-edit helper before the task begins.
The agent supplies edits and scope; the helper contains no fixture-specific solution. Helper setup effort is reported separately from task completion time.
If simple native automation matches speed, correctness, and provider cost, the mechanism leaves Tzro's performance thesis.
Setup, portability, and maintenance benefits remain separate claims that need their own evidence.
The first validation targets Tzro Standard integrated into the native Antigravity CLI, using the existing `GEMINI_API_KEY` from the process environment.
The user selected this route on 2026-10-02, superseding cached-account authentication.
Set `modelProvider: "gemini"` in each isolated client's settings. Load the key explicitly if its source is `.env`; the CLI does not load that file.
Tzro Standard's local tools require no separate model credential.
All three comparison conditions use the exact model `gemini-3.8-flash-low`, with the same Low reasoning variant.
The cloud agent retains strategy and code generation. Local deterministic graph execution remains available.
Full's optional decision and extraction runtimes, and T46's replacement orchestration, remain outside this first validation.
The first workload is Automatic Verification after source edits.
The initial task set includes Go, Python, and TypeScript from the start.
Each language has one single-file bug fix, one multi-file change, and one task diagnosing an existing test failure: nine initial cases.
Each language needs native verification checks and independent task correctness grading.
Use small, self-contained projects with realistic source structure, visible tests, and fixed dependency versions.
Include a multi-file function rename across modules and tests as one bulk-edit case.
Before model runs, confirm each starting fixture fails its task-specific grader and a reference solution passes.
Private behavior tests and regression checks grade stated requirements, without matching an exact patch or adding undisclosed requirements.
Visible tests may change when the requested task requires it. Independent grading inputs remain fixed.
All three conditions use the same declared editable and protected inputs.
Prompts state the requested behavior without prescribing tool use or a fixed sequence of edits and failures.
The agent receives check results automatically, including failures, without a separate polling call.
The intended mechanism replaces repeated cloud coordination with a prescribed local verification workflow.
Local checks still run, and their time and tool executions remain part of the measured workflow.
The first comparison uses the same required checks to isolate scheduling from impact-based test selection.
T07 and T29 inform this workload. T07's agent-supplied test command differs from an automatic edit-triggered check.
The boundary is an explicit Edit Batch submitted through one grouped edit-and-verify operation.
The submitted edit list defines the batch. Verification results return with that operation, without a separate batch-closing or polling call.
Every result includes a Verification Summary with application state, check outcomes, and available failure diagnostics.
Its configurable default size target is 8,000 bytes. Mandatory application state, check outcomes, and primary failure diagnostics remain inline even when they exceed that target.
The summary remains in the original response when full logs require expansion. Unknown details and omitted output remain explicit.
For large failure sets, it includes each required check command's outcome, known counts, and grouped representative diagnostics with available messages and source locations.
Complete parsed diagnostics and full logs remain expansion evidence. Counts stay unknown when the native output does not establish them.
`edit_and_verify({ edits: [...] })` is the illustrative interface, not an existing product tool.
If the batch applies but a required check fails, the applied edits remain available for repair and the operation returns diagnostics.
Independent required checks continue after failure within the remaining operation budget.
Checks blocked by a failed prerequisite are reported as not run. A blocked check does not count as a pass.
Native check outcomes determine verification status. Detected source changes during execution do not automatically invalidate a passing check.
Task correctness remains a separate evaluation outcome.
Every submitted patch is validated before any batch writes. If one patch cannot apply, the whole batch is rejected without applying its edits.
If a write-time error leaves Partial Application, the operation preserves observed state and reports changed files and uncertainty.
Verification is not run for an incompletely applied batch.
Required checks come from a repository-defined Verification Preset, reused for every grouped operation.
Required check commands run sequentially by default. Parallel groups require explicit opt-in in the preset.
Each command retains its configured internal parallelism. All three benchmark conditions use the same preset scheduling policy.
The agent does not choose arbitrary required check commands per batch. All three comparison conditions use the same preset.
Missing verification configuration or unavailable checks do not block valid edits.
The summary reports edit application separately from verification that is not configured or unavailable, including the reason checks did not run.
The harness agent can report the missing setup to the user. No executed checks means no reported verification pass.
The first design uses a five-minute default timeout that users can configure differently for long jobs.
One shared timeout covers the entire operation, including patch validation, edit application, and required checks.
Each stage uses the remaining budget. Starting another check does not reset the timeout.
This revises the earlier direction of extending waits for unknown runtimes. Local waiting cannot automatically extend the configured timeout.
Verification stays within the original tool call. Delayed final results after that call returns are rejected for the first design.
The user cites harness compatibility as the reason for retaining ordinary tool results.
Tzro owns routine waiting locally. The cloud agent participates when an exception requires its judgment.
On timeout, verification stops and returns an incomplete result with available diagnostics. Applied edits remain available for repair.
Harness cancellation stops remaining edits and checks, including owned check process groups.
Applied edits and completed check outcomes remain. Unfinished verification is incomplete; incomplete application retains the Partial Application policy.
Return the summary through the original call when possible. If the caller disconnects, retain observed state locally when possible.
Process exit triggers prompt result delivery before the timeout.
Concrete fixtures still need selection and review before a benchmark run. Their design and independent grading approach are agreed.
The [implementation plan](../.scratch/turn-reduction/implementation_plan.md) is approved. Provider configuration and exact-model availability on the Gemini API route still need runtime verification.
The evaluation design discussion is complete. Concrete fixtures, runner integration, and the grouped operation still require implementation and validation.
Confirmation parameters belong to any separately budgeted follow-up campaign.
Detailed crash recovery and concurrent external edits remain implementation planning topics.
The [thesis handoff](../.scratch/turn-reduction-thesis.md) describes the three comparison conditions.

---

## 1. Executive Summary & Problem Context

Cloud Decision Round count can affect workflow latency, cost, and correctness. Fewer rounds alone do not establish an improvement.
The following estimates motivate hypotheses. They do not establish measured savings or a causal effect:
- **Quadratic Context Cost ($O(N^2)$)**: Because frontier chat models ingest the cumulative conversation history on every turn, a 15-turn workflow re-reads prior context 15 times. Reducing turns from 14 to 3 eliminates up to 75% of cumulative prompt tokens.
- **Latency Multiplication**: Each cloud inference turn incurs network transit, pre-fill queuing, and generative latency (typically 5–20 seconds per turn). A 15-turn task takes 3–5 minutes; a 3-turn task finishes in 20 seconds.
- **Drift & Compounding Hallucinations**: Each turn boundary introduces an opportunity for the model to lose context, pursue irrelevant tangents, misunderstand prior tool outputs, or terminate prematurely.

### 1.1 Observed Empirical Turn-Inflation Drivers (from `validation-v1` to `validation-v5`)

1. **Serial Discovery Chains (The 5-Turn Cold Start)**:
   Models spend Turns 1–5 performing serial directory and file inspection (`ls` $\to$ `find` $\to$ `grep` $\to$ `read_file` $\to$ `read_callee`) before writing a single line of code.
2. **Micro-Action Ping-Pong**:
   Models execute isolated micro-steps: Turn 5 (read file), Turn 6 (edit file), Turn 7 (run test), Turn 8 (view failure), Turn 9 (edit file again), Turn 10 (re-run test).
3. **Tool Parameter Errors & Apology Spirals**:
   In `validation-v3` and `validation-v5`, models frequently made trivial syntax errors (passing `#hash` with `#`, positional args to `--budget`, wrong positional args to `ingest`), triggering 2–3 wasted apology and retry turns.
4. **Data Exploration Marathons**:
   On tabular datasets, models often took 8–14 turns importing files, inspecting schemas, running isolated SQL queries, and debugging ad-hoc Python/awk scripts.
5. **Decomposition Spirals (Excessive Granularity)**:
   When skeletons elided function bodies too aggressively without return types, models issued up to 17 sequential `tzro expand` calls.
6. **Chatty Acknowledgment Turns (Zero-Progress Turns)**:
   Models frequently generate conversational turns without tools ("I see the file. Now I will inspect its contents"), consuming a turn without altering workspace state.

---

## 2. Exhaustive Hypothesis Catalog (48 Entries)

---

### Pillar 1: Preflight & Zero-Turn Context Seeding

#### T01: Task-Mention Preflight Scanner & Turn-1 Evidence Injection
* **Type**: Radical
* **Target Bottleneck**: Turns 1–4 spent finding files and reading signatures.
* **Mechanism**: When a task prompt arrives, tzro runs an offline lexical and AST scan *before dispatching the first request to the cloud LLM*. It identifies mentioned files, function names, and error strings, extracts their declaration spans and enclosing implementations, and injects them directly into the Turn 1 prompt as pre-loaded evidence.
* **Turn & Quality Impact**: Slashes turn count from 10–14 turns down to 2–3 turns. The model can formulate and apply the fix on Turn 1.
* **Implementation Surface**: `pkg/benchmark/workflow/execute.go`, Preflight hook.

#### T02: Automatic Failing-Test Triangulation on Turn 1
* **Type**: Visionary
* **Target Bottleneck**: Turns spent running tests to reproduce failures, then locating the test file.
* **Mechanism**: If the prompt describes a bug or mentions tests, the preflight hook executes the test suite locally before Turn 1. It extracts the failing assertion line, panic stack trace, and git diff, and injects them as a structured diagnostic into Turn 1.
* **Turn & Quality Impact**: Eliminates 2–3 exploratory turns; the agent starts with exact root-cause failure coordinates.
* **Implementation Surface**: `pkg/compactor/compactor.go`, Preflight engine.

#### T03: Repository Topology Pre-Mapping
* **Type**: Pragmatic
* **Target Bottleneck**: Initial blind `ls`, `find`, and directory traversal turns.
* **Mechanism**: Inject a compact 20-line structural topology (key packages, exported interfaces, entrypoint files) into the initial system context.
* **Turn & Quality Impact**: Completely eliminates Turns 1–2 of directory reconnaissance.
* **Implementation Surface**: `pkg/hooks/instructions.go`.

#### T04: Automatic Import & Call-Closure Pre-Resolution
* **Type**: Architectural
* **Target Bottleneck**: Turns spent jumping from caller to callee across separate files.
* **Mechanism**: When a target symbol is identified in preflight, parse its Concrete Syntax Tree to resolve its immediate callers, callees, and imported type definitions, bundling them into a single interconnected context graph on Turn 1.
* **Turn & Quality Impact**: Saves 3–4 exploratory file-reading turns.
* **Implementation Surface**: `pkg/ast/spans.go`, `pkg/context/`.

#### T05: Pre-Baked Schema & Head/Tail Injection for Tabular Tasks
* **Type**: Pragmatic
* **Target Bottleneck**: Turns 1–4 of tabular tasks spent doing `head`, `wc -l`, and schema inspection.
* **Mechanism**: Detect `.csv`, `.tsv`, or `.parquet` files mentioned in the prompt. Automatically inject: row count, column data types, and first 5 / last 5 rows directly into Turn 1.
* **Turn & Quality Impact**: Drops 3–4 exploratory data discovery turns immediately.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

---

### Pillar 2: High-Order Composite Macro-Tools

#### T06: `locate_and_read` (One-Shot Symbol Explorer)
* **Type**: Pragmatic
* **Target Bottleneck**: The ubiquitous 2-turn sequence: `probe <symbol>` at Turn $N$, followed by `view_file <path>` at Turn $N+1$.
* **Mechanism**: A unified tool that takes a symbol or pattern, locates the file via Tree-sitter AST, extracts its exact declaration span, docstrings, and full implementation, returning the complete code block in one turn.
* **Turn & Quality Impact**: Reduces symbol exploration from 2 turns to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T07: `edit_and_test` (Atomic Modification & Verification)
* **Type**: Pragmatic / High-Impact
* **Target Bottleneck**: 3-turn ping-pong: Turn 1 (edit file), Turn 2 (run test), Turn 3 (inspect test result).
* **Mechanism**: Agent supplies the target file edit and the verification command. The tool applies the patch and immediately executes the test command locally, returning the edit status and test outcome in a single response.
* **Turn & Quality Impact**: Compresses a 3-turn cycle into 1 turn. Multiplied across 2–3 edit attempts, saves 4–6 turns per task.
* **Implementation Surface**: `pkg/executor/tools.go`.
* **Initial Workload Boundary**: The selected Automatic Verification workload concerns checks triggered by source edits without a separate cloud scheduling decision. This entry's agent-supplied verification command is a related composite-tool treatment. The first scheduling comparison keeps the required check set constant.

#### T08: `diagnose_incident` (Automated Stack-to-Source Traceback)
* **Type**: Visionary
* **Target Bottleneck**: Incident diagnosis requiring 6–8 turns of log inspection, grep, stack tracing, and file reads.
* **Mechanism**: Ingests raw panic/error logs, parses stack frames, automatically fetches source lines for every application frame on disk, and returns the annotated call chain in a single turn.
* **Turn & Quality Impact**: Resolves incident diagnosis tasks in 1 turn instead of 6–8 turns.
* **Implementation Surface**: `pkg/compactor/`, `pkg/context/`.

#### T09: `data_pipeline` (Unified Tabular Ingest + Query + Compute)
* **Type**: Pragmatic
* **Target Bottleneck**: Ingesting a CSV, inspecting schema, and running multiple SQL queries across 7–14 turns.
* **Mechanism**: A single primitive that takes a file path and a SQL query (or aggregation goal), auto-imports the table into SQLite, runs the query, and returns formatted results or writes them to the target file.
* **Turn & Quality Impact**: Slashes tabular tasks from 12+ turns down to 1–2 turns.
* **Implementation Surface**: `pkg/compactor/tabular.go`, `cmd/tzro/mcp.go`.
* **Existing Capability**: The [executor query tool](../pkg/executor/tools.go) already ingests a provided file and runs supplied SQL in one call. Natural-language query generation and result-file writing are additional parts of this hypothesis. The capability check does not establish a workflow advantage.

#### T10: `refactor_symbol` (Global Multi-File Rename/Update)
* **Type**: Architectural
* **Target Bottleneck**: Iteratively editing 4–5 files one-by-one to rename or update a signature across turns.
* **Mechanism**: Accepts an old symbol signature and replacement, uses AST cross-references to apply the refactoring across definition, call-sites, and imports in one atomic workspace operation.
* **Turn & Quality Impact**: Turns a 5-turn sequential editing chore into 1 turn.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/executor/tools.go`.

---

### Pillar 3: Local Speculative Execution & Self-Healing Loops

#### T11: Speculative Patching with Local Test Gating
* **Type**: Visionary
* **Target Bottleneck**: Cloud LLM trying minor syntax/formatting tweaks across multiple turns after failed test runs.
* **Mechanism**: Agent supplies a candidate fix. The local engine applies it and runs tests. If tests fail due to a simple compile/syntax error or minor typo, the engine attempts deterministic AST fixes (e.g. adding missing imports or formatting) before yielding.
* **Turn & Quality Impact**: Eliminates 1–3 compiler-debugging turns.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T12: Multi-Candidate Patch Evaluator
* **Type**: Visionary
* **Target Bottleneck**: Model unsure between two edge-case fixes, trying one, failing tests, then trying the second.
* **Mechanism**: Agent submits up to 3 candidate patch variants in a single tool call (`patches: [patchA, patchB, patchC]`). The local engine tests them in sequence and commits the first one that passes tests.
* **Turn & Quality Impact**: Reduces trial-and-error hypothesis testing from 3–6 turns to 1 turn.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T13: Automatic Post-Edit Linter & Formatter Hook
* **Type**: Pragmatic
* **Target Bottleneck**: Wasted turns running `gofmt`, `eslint`, or fixing whitespace errors flagged by CI/tests.
* **Mechanism**: On every file modification, the tool hook automatically runs the project's native formatter (`gofmt`, `ruff`, `prettier`) before executing tests or returning to the model.
* **Turn & Quality Impact**: Completely eliminates turns spent resolving code formatting or lint failures.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T14: Local Deadlock & Concurrency Trace Harvester
* **Type**: Pragmatic
* **Target Bottleneck**: Integration tests deadlocking and hitting the full 30-second timeout, forcing the agent to retry or guess.
* **Mechanism**: Run tests with a tight timeout and Goroutine stack dump monitor. If deadlock occurs, capture Goroutine states, pinpoint the exact mutex and line numbers holding locks, and return the deadlock diagnostic immediately.
* **Turn & Quality Impact**: Eliminates 2–3 turns of blind concurrency debugging and timeout retries.
* **Implementation Surface**: `pkg/executor/shell_unix.go`, `pkg/compactor/`.

**Initial Verification Boundary**: This hypothesis does not establish a universal deadline for Automatic Verification.
A long runtime or silent output alone does not establish deadlock.
The first design uses a configurable five-minute default timeout. Longer jobs require an appropriate configured limit.

#### T15: Speculative Search-and-Expand
* **Type**: Architectural
* **Target Bottleneck**: Receiving a skeleton, realizing a function body is needed, and calling `expand` on the next turn.
* **Mechanism**: When an agent requests a skeleton and its prompt mentions a specific function, automatically expand that specific function in the initial skeleton response.
* **Turn & Quality Impact**: Saves 1 turn per file read by avoiding the immediate follow-up expansion call.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

---

### Pillar 4: Directed Graph & Multi-Step Pipeline Offload

#### T16: Shell-Pipe Execution DSL (`tzro pipe`)
* **Type**: Visionary
* **Target Bottleneck**: Models refusing to construct complex JSON DAGs, falling back to 6 sequential shell turns.
* **Mechanism**: Allow models to submit an intuitive Unix-style pipeline: `tzro pipe "probe VerifyToken | skeleton | grep crypto > out.txt"`. The engine compiles it into an execution graph and runs it on-device in 1 turn.
* **Turn & Quality Impact**: Replaces 4–6 interactive cloud turns with a single local execution.
* **Implementation Surface**: `pkg/executor/engine.go`, `cmd/tzro/execute.go`.

#### T17: Autonomous Local Exploration Sub-DAG
* **Type**: Visionary
* **Target Bottleneck**: Broad code investigation spanning 8–10 turns of grep, find, and file reads.
* **Mechanism**: Agent dispatches an exploration directive to Tzro's local engine: "Trace all callers of `GenerateInvoice` and return their error handling blocks". Tzro executes the traversal locally and returns only the final structured findings.
* **Turn & Quality Impact**: Collapses an 8-turn investigation into 1 turn.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/executor/engine.go`.

#### T18: Conditional On-Device Branching Workflows
* **Type**: Architectural
* **Target Bottleneck**: Cloud model waiting for test output just to decide whether to commit or retry.
* **Mechanism**: Local execution graph with conditional branching: `Node A (run test) -> on_pass: Node B (write summary) -> on_fail: Node C (extract diff and stack trace)`. The entire decision tree runs locally.
* **Turn & Quality Impact**: Cuts 2–3 turns of mechanical branch evaluation.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T19: Multi-File Batch Patching in Single Graph
* **Type**: Pragmatic
* **Target Bottleneck**: Applying changes across 4 files sequentially over 4 separate turns.
* **Mechanism**: Submit an array of file patches in one graph invocation. After the batch applies, run the required checks. If a check fails, retain the applied edits and return diagnostics for repair.
* **Turn & Quality Impact**: Cuts multi-file editing from $N$ turns down to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.
* **Agreed Policies**: Failed checks retain successfully applied edits. Preflight validates every patch before batch writes. One invalid patch rejects the whole batch without applying its edits. Write-time failures preserve and report observed partial state, with verification not run. This entry describes a hypothesis, rather than an existing grouped editing capability.

#### T20: Deterministic Tabular Analysis Pipeline
* **Type**: Pragmatic
* **Target Bottleneck**: Ingest $\to$ Schema Check $\to$ Query 1 $\to$ Query 2 $\to$ Write Result taking 5–7 turns.
* **Mechanism**: Standardized local pipeline: Ingest CSV $\to$ Run SQL aggregations $\to$ Format JSON output $\to$ Write to disk in 25ms locally.
* **Turn & Quality Impact**: Reduces data tasks from 7–14 turns down to exactly 1 turn.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

---

### Pillar 5: Eliminating Ergonomic Errors & Recovery Detours

#### T21: Permissive Flag & Argument Normalization
* **Type**: Pragmatic
* **Target Bottleneck**: Real errors seen in validation: `#hash` rejecting `#`, positional args to `--budget`, positional table names to `ingest`.
* **Mechanism**: Make CLI parsers maximally tolerant: strip leading `#` from hashes automatically; detect numeric arguments to `context` as budgets; infer positional filenames vs table names.
* **Turn & Quality Impact**: Directly eliminates the 2–3 turn error recovery penalties observed in benchmark runs.
* **Implementation Surface**: `cmd/tzro/`, `pkg/context/`.

#### T22: Auto-Correcting Fuzzy Path Resolver
* **Type**: Pragmatic
* **Target Bottleneck**: Models hallucinating slightly incorrect paths (e.g. `/workspace/pkg/auth.go` instead of `pkg/auth/authenticator.go`), causing ENOENT errors and retry turns.
* **Mechanism**: When a tool receives a non-existent path, run an instant fuzzy match across workspace paths. If a high-confidence match exists, execute against the real path with an inline notice: `[Resolved to 'pkg/auth/authenticator.go']`.
* **Turn & Quality Impact**: Converts an immediate failure turn into an instant success turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T23: Missing Host Utility Shimming
* **Type**: Pragmatic
* **Target Bottleneck**: Model attempting to run `jq`, `sqlite3`, or `column` in isolated environments where they are missing, triggering failure and replanning turns.
* **Mechanism**: Provide built-in Go shims for common CLI data utilities (`jq`, `sqlite3`, `curl`, `tree`) within the tzro environment.
* **Turn & Quality Impact**: Prevents 1–2 turns of tool-discovery detours when standard host binaries are missing.
* **Implementation Surface**: `pkg/executor/shell_unix.go`.

#### T24: Pre-Execution AST Syntax Validation on Edits
* **Type**: Pragmatic
* **Target Bottleneck**: Model submitting code with unclosed braces or invalid syntax, waiting a full turn for a compiler failure, and spending another turn fixing it.
* **Mechanism**: Parse proposed edits with Tree-sitter before writing to disk. If syntax is malformed, reject immediately with exact line syntax diagnostics, prompting immediate in-turn correction.
* **Turn & Quality Impact**: Prevents wasted test execution turns on broken syntax.
* **Implementation Surface**: `pkg/ast/`, `pkg/executor/tools.go`.

#### T25: Suppressing Empty Apology & Conversational Filler Turns
* **Type**: Pragmatic
* **Target Bottleneck**: Chatty turns where the model says "I see the problem. Let me now edit service.go" without calling any tool.
* **Mechanism**: In the agent harness or proxy, detect assistant messages that contain zero tool calls and zero final answers. Prompt the model inline to emit the tool call immediately before returning the turn to the user/runner.
* **Turn & Quality Impact**: Eliminates 1–3 zero-progress conversational filler turns per task.
* **Implementation Surface**: `pkg/hooks/picoder.go`, Pi Extension.

---

### Pillar 6: Parallel Action Batching & Multi-Call Scheduling

#### T26: System-Level Multi-Tool Batching Enforcement
* **Type**: Pragmatic
* **Target Bottleneck**: Models issuing tool calls strictly one-by-one when operations are completely independent.
* **Mechanism**: Instruct the agent in system guidelines to emit parallel tool calls for independent reads, searches, and checks in a single turn.
* **Turn & Quality Impact**: Collapses 3 sequential reads into 1 parallel turn.
* **Implementation Surface**: `pkg/hooks/instructions.go`.

#### T27: Multi-File Slicing Primitive (`read_files_batch`)
* **Type**: Pragmatic
* **Target Bottleneck**: Reading 3 related interface files across 3 separate conversational turns.
* **Mechanism**: Expose a tool that accepts an array of file paths or symbol names, returning all declaration spans and implementations in a single consolidated response.
* **Turn & Quality Impact**: Cuts 3 file-reading turns down to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T28: Concurrent Probe & Grep Fan-Out
* **Type**: Pragmatic
* **Target Bottleneck**: Searching for symbol A, then searching for symbol B on the next turn.
* **Mechanism**: Allow `tzro probe` to accept multiple query terms simultaneously, returning combined ranked symbol matches in 1 turn.
* **Turn & Quality Impact**: Halves discovery turn count during multi-component investigations.
* **Implementation Surface**: `cmd/tzro/probe.go`, `pkg/search/`.

#### T29: Bundled Post-Edit Verification Packet
* **Type**: Pragmatic
* **Target Bottleneck**: Turn 10 (run test) $\to$ Turn 11 (`git diff` to verify) $\to$ Turn 12 (`git status`).
* **Mechanism**: Provide a verification tool that executes unit tests, computes git diff of modified files, and checks git status concurrently, returning the complete verification state in 1 turn.
* **Turn & Quality Impact**: Reduces post-edit verification from 3 turns to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

**Agreed Result Boundary**: Automatic Verification returns a mandatory Verification Summary in the original tool response, with a configurable size target that preserves mandatory evidence.
Full logs can require expansion, but the operation does not replace the summary with an artifact pointer.

#### T30: Batched Tabular Ingest & Multi-Query Array
* **Type**: Pragmatic
* **Target Bottleneck**: Calling `tzro query` 5 separate times across 5 turns to calculate 5 numbers.
* **Mechanism**: Allow `tzro query` to accept an array of SQL queries in a single call, executing them in a single SQLite transaction and returning all results at once.
* **Turn & Quality Impact**: Directly eliminates 4 cloud turns on analytical data tasks.
* **Implementation Surface**: `cmd/tzro/query.go`, `pkg/compactor/tabular.go`.

---

### Pillar 7: Rich Semantic Skeletons & Proactive Dependency Inlining

#### T31: Self-Contained Skeletons with Inlined Types & Return Shapes
* **Type**: Architectural
* **Target Bottleneck**: Skeletons showing function names but eliding return types or struct definitions, forcing follow-up read turns.
* **Mechanism**: Ensure AST skeletons retain complete type signatures, return structs, interface definitions, and field tags.
* **Turn & Quality Impact**: Eliminates the primary reason models call `expand` or read surrounding files, cutting follow-up turns by 80%.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

#### T32: Call-Site Preview Inlining
* **Type**: Architectural
* **Target Bottleneck**: Agent opening 2 other files just to see how a target function is invoked.
* **Mechanism**: When returning declaration spans, extract the top 2 call-sites across the codebase and inline them as 3-line usage snippets directly below the signature.
* **Turn & Quality Impact**: Satisfies the model's need for usage examples in 1 turn, avoiding 2 caller-reading turns.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/ast/spans.go`.

#### T33: Auto-Inlining of Small Functions (<6 Lines)
* **Type**: Pragmatic
* **Target Bottleneck**: Models requesting expansion of trivial 2-line helpers, getters, or error wrappers across turns.
* **Mechanism**: In AST skeletons, never elide function bodies that are 5 lines or fewer. Inline them directly.
* **Turn & Quality Impact**: Eliminates 1–3 expansion turns per file read.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

#### T34: Semantic Breadcrumb Inlining
* **Type**: Architectural
* **Target Bottleneck**: Viewing a method, then having to read the top of the file to understand receiver struct fields and imports.
* **Mechanism**: When slicing or skeletonizing an individual method, always include the enclosing struct definition, receiver fields, and relevant imports at the top of the excerpt.
* **Turn & Quality Impact**: Saves 1 turn of scrolling/reading file headers.
* **Implementation Surface**: `pkg/ast/spans.go`.

#### T35: Automatic Error Type & Constant Inlining
* **Type**: Pragmatic
* **Target Bottleneck**: Model reading `service.go`, seeing `return ErrInvalidTier`, then spending a turn searching for `ErrInvalidTier` definition.
* **Mechanism**: When returning a function's code, detect referenced package-level constants and errors, inlining their definitions in a comment block.
* **Turn & Quality Impact**: Eliminates 1 follow-up search turn per bug fix.
* **Implementation Surface**: `pkg/ast/spans.go`.

---

### Pillar 8: On-Device System 1 / Deterministic Decision Delegation

#### T36: Local Fast-Path Routing (JEV / ONNX Rule Router)
* **Type**: Visionary
* **Target Bottleneck**: Calling cloud LLM on Turn 1 just to ask "Which file should I inspect?".
* **Mechanism**: Use the local lightweight decision worker (JEV / GLiNER / rule classifier) to parse the task prompt and automatically trigger the initial file read or test run locally before the cloud LLM is invoked.
* **Turn & Quality Impact**: Skips Turn 1 cloud inference entirely; cloud LLM receives initial code on its first invocation.
* **Implementation Surface**: `pkg/decision/`, `pkg/extractor/`.

#### T37: Local Output Artifact Generator
* **Type**: Pragmatic
* **Target Bottleneck**: Agent spending 2 turns formatting and writing a required `summary.json` or benchmark result artifact.
* **Mechanism**: Provide a dedicated tool `write_benchmark_result(data: object)` that validates schema compliance and writes the required artifact in 1 turn.
* **Turn & Quality Impact**: Eliminates 1–2 formatting and retry turns at the conclusion of tasks.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T38: Local Heuristic Bug Classifier & Fix Suggester
* **Type**: Visionary
* **Target Bottleneck**: Model spending 3 turns analyzing an obscure stack trace or nil pointer dereference.
* **Mechanism**: Parse panic stack traces with deterministic heuristics (e.g. nil pointer in mutex lock, missing map initialization, out of bounds slice), injecting the exact root cause and suggested fix pattern.
* **Turn & Quality Impact**: Cuts diagnostic analysis turns from 3 turns to 1 turn.
* **Implementation Surface**: `pkg/compactor/compactor.go`.

#### T39: Impact-Driven Test Selector (Fast Local Feedback)
* **Type**: Pragmatic
* **Target Bottleneck**: Running the entire test suite (taking 45 seconds or hitting timeouts) on every turn.
* **Mechanism**: Use `tzro impact` to automatically select and execute *only* the specific test that covers the modified function, running in <500ms.
* **Turn & Quality Impact**: Eliminates timeout failures and lets agents verify edits instantly in 1 turn.
* **Implementation Surface**: `pkg/context/impact.go`.
* **Existing Capability**: `tzro test` already provides impact-based selection and execution through the [TestSelector](../pkg/context/test_selection.go). The proposed latency and coverage claims still need validation. Automatic scheduling after edits is a separate treatment from test selection.

#### T40: Deterministic NL-to-SQL Template Engine
* **Type**: Pragmatic
* **Target Bottleneck**: Model taking 3 turns to craft and debug a SQL query for a simple aggregation.
* **Mechanism**: In tabular workflows, provide pre-compiled SQL templates for common analytical questions (sum by category, top N by revenue, filter by date), auto-executing on ingest.
* **Turn & Quality Impact**: Turns analytical query formulation into a 1-turn automated answer.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

---

### Pillar 9: Intent-Driven Declarative Primitives (Goal-Oriented Action)

#### T41: `solve_failing_test` Primitive
* **Type**: Radical
* **Target Bottleneck**: The entire 8-turn cycle of reproducing, finding, editing, and verifying a test failure.
* **Mechanism**: A single high-level tool that takes a failing test identifier. Tzro runs the test, locates the failing function, extracts its AST span, applies the agent's proposed logic change, and runs the test to confirm.
* **Turn & Quality Impact**: Solves targeted bug fix tasks in 1–2 turns total.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T42: `implement_interface_method` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: 3 turns spent reading interface, finding target struct, and generating boilerplate method signature.
* **Mechanism**: Accepts interface name and struct name; auto-scaffolds the exact method signature and docstring in the target file, leaving only the body for the agent to complete.
* **Turn & Quality Impact**: Cuts 2 boilerplate turns from interface implementation tasks.
* **Implementation Surface**: `pkg/ast/`.

#### T43: `extract_and_summarize_dataset` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: 8 turns of exploratory data wrangling.
* **Mechanism**: High-level declarative tool: agent specifies the dataset file and natural language aggregation question. Tzro runs ingest, executes query, formats output, and writes `summary.json` in one atomic step.
* **Turn & Quality Impact**: Collapses the entire data analysis benchmark task into exactly 1 turn.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

#### T44: `sync_documentation_and_types` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: Updating docs, types, and comments across 3 turns after a code change.
* **Mechanism**: Analyzes git diff of code changes, updates related docstrings and markdown specs across the repo in 1 turn.
* **Turn & Quality Impact**: Eliminates 2 post-implementation documentation turns.
* **Implementation Surface**: `pkg/context/impact.go`.

---

### Pillar 10: Radical & Assumption-Breaking Turn Reducers

#### T45: One-Turn End-to-End Task Solver (Zero-Turn Synthesis)
* **Type**: Radical
* **Target Bottleneck**: Multi-turn agentic workflows when a single high-context prompt could solve the task in Turn 1.
* **Mechanism**: When a task prompt arrives, tzro's offline compiler gathers all relevant source files, types, and test specs into a self-contained "mega-prompt". The frontier LLM emits the complete solution in Turn 1. A local runner verifies and commits.
* **Turn & Quality Impact**: Reduces turn count from 10–15 turns to **exactly 1 turn** ($>85\%$ turn reduction).
* **Implementation Surface**: `pkg/benchmark/workflow/execute.go`.

#### T46: Reverse Orchestration: Local Agent with Cloud Oracle Calls
* **Type**: Radical
* **Target Bottleneck**: Cloud LLM acting as the primary orchestrator, making slow cloud roundtrips for every trivial shell command.
* **Mechanism**: Invert the control loop: a local, deterministic state-machine agent runs locally on device, managing file reads, test runs, and git status. It calls the cloud LLM *only* as an oracle for code synthesis when a specific block needs implementation.
* **Turn & Quality Impact**: Slashes conversational turns from 15 down to 1–2 cloud calls.
* **Implementation Surface**: `pkg/executor/engine.go`, `cmd/tzro/`.

#### T47: Continuous Patch Streaming & Auto-Commit Loop
* **Type**: Visionary
* **Target Bottleneck**: Turn-based conversational handoffs between code generation and execution.
* **Mechanism**: Model streams a patch. The local engine applies hunks on the fly as they stream in, runs tests in parallel, and yields execution feedback immediately without waiting for full turn completion.
* **Turn & Quality Impact**: Converts discrete multi-turn cycles into a fluid single-stream execution.
* **Implementation Surface**: `pkg/proxy/`, `pkg/executor/`.

#### T48: Virtual Entity Mutation (Direct State Manipulation)
* **Type**: Radical
* **Target Bottleneck**: Navigating file paths, line numbers, and disk I/O across multiple turns.
* **Mechanism**: Represent codebase components as virtual entities in memory (e.g. `service.AccountDiscount[Enterprise] = 0.20`). The model mutates the entity state declaratively; the local engine compiles the change into AST source edits and commits.
* **Turn & Quality Impact**: Replaces 6 turns of file search, read, line identification, and editing with 1 declarative state mutation turn.
* **Implementation Surface**: `pkg/ast/`, `pkg/executor/`.

---

## 3. Turn Reduction Prioritization Matrix

| Tier | Focus Areas | Key Hypotheses | Expected Turn Reduction | Implementation Complexity |
| :--- | :--- | :--- | :--- | :--- |
| **Tier 1: Immediate Wins (Pragmatic)** | Composite Tools & Ergonomic Auto-Fixes | **T07** (`edit_and_test`), **T06** (`locate_and_read`), **T21** (Param Normalization), **T09** (`data_pipeline`), **T25** (Suppress Empty Turns) | **30% – 50% fewer turns** | Low (< 1-2 days each) |
| **Tier 2: Discovery Collapsers (Architectural)** | Preflight Seeding & Batching | **T01** (Preflight Scanner), **T02** (Failing-Test Triangulation), **T26** (Parallel Batching), **T31** (Rich Skeletons), **T39** (Impact Test Selection) | **50% – 65% fewer turns** | Medium (3-5 days) |
| **Tier 3: Autonomous Sub-Loops (Visionary)** | Pipe DSL, Multi-Candidate & Incident Packets | **T16** (`tzro pipe`), **T08** (`diagnose_incident`), **T12** (Multi-Candidate Patch), **T11** (Speculative Patching) | **60% – 75% fewer turns** | Medium-High |
| **Tier 4: Radical Paradigms (Assumption-Breaking)** | Control Inversion & Zero-Turn Synthesis | **T45** (One-Turn Solver), **T46** (Reverse Orchestration), **T41** (`solve_failing_test`), **T48** (Virtual Entity Mutation) | **75% – 90% fewer turns** (Down to 1-2 turns) | High (Architectural shift) |

---

## 4. Synergies Between Turn Reduction and Token Reduction

Reducing turns is the most powerful lever for reducing total tokens:
1. **Compounding Savings**: Each eliminated turn removes one entire round of prompt overhead ($2{,}000–$10{,}000 tokens per turn in mid-to-late conversation).
2. **Preventing Error Drift**: A 2-turn workflow cannot get lost in the weeds; a 15-turn workflow has a high cumulative probability of pursuing false leads.
3. **KV-Cache Alignment**: Fewer turns mean fewer prefix invalidations, maximizing the prompt cache hit rate across the entire task.
