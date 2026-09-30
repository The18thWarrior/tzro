# Diagnosis: workflow benchmark savings, September 28, 2026

## Finding

The older and newer reports measure different workflows. They do not establish a regression in the same experiment.

The September 23 scenario benchmark gave Full a prepared context pack and explicitly directed its tool choices.
Its baseline had instructions to read every file, one tool call per turn.
The September 28 installation benchmark uses native Pi tools and an optional skill instead.
The agent rarely used the CLI Toolkit, and never used the experimental runtimes during these tasks.

The latest run therefore exposes an adoption gap and extra model turns.
It does not establish that the underlying compression algorithms lost their previous effectiveness.
The saved repository artifacts support this conclusion, but do not provide a complete two-week series of repeated runs.

The later Baseline/Standard validation passed all 42 task checks and saved 18.3% total tokens overall.
None of its three repetitions met the user’s revised 33% target. No graph calls occurred; the follow-up below records the evidence.

## Sources and scope

- [Latest workflow JSON](../../benchmarks/workflows-20260928.json), timestamp `2026-09-29T01:06:43Z` (September 28 in Los Angeles).
- [Published workflow report](../../benchmarks/workflows-20260928.md).
- [September 23 scenario results](../../../pkg/hooks/testdata/scenarios_benchmark_results.json).
- [September 23 cache comparison](../../../pkg/hooks/testdata/kvcache_e2e_benchmark_results.json).
- [Installation benchmark decision](../../../.scratch/adoption-readiness/issues/11-benchmark-categories-and-evidence.md).
- Saved homes, installed binaries, activity traces, and SQLite stores under the run directory recorded in the JSON.

The initial diagnosis was an offline audit. It used saved results, Git history, source inspection, report regeneration, and local hook replay.
That diagnosis required no paid requests or implementation changes. The follow-up section records later implementation and paid validation.
SQLite inspection used copies of the saved databases and their WAL files.

## Recomputed results

Costs use the prices recorded in the latest JSON. Total tokens include uncached input, cache reads, cache writes, and output.

| Profile | Passed | Requests | Total tokens | Estimated cost | Cost change from Baseline |
| --- | ---: | ---: | ---: | ---: | ---: |
| Baseline | 6/8 | 49 | 145,951 | $0.02532936 | — |
| Standard | 8/8 | 68 | 219,783 | $0.03497142 | +38.1% |
| Full | 8/8 | 69 | 239,843 | $0.03635700 | +43.5% |

Baseline stopped early on tasks 3 and 6. Cheap failures distort an aggregate cost comparison.
The six tasks that every profile passed still show higher costs for Tzro:

| Profile | Requests | Total tokens | Estimated cost | Cost change | Agent time |
| --- | ---: | ---: | ---: | ---: | ---: |
| Baseline | 43 | 134,525 | $0.02350704 | — | 221.979s |
| Standard | 56 | 189,391 | $0.02926884 | +24.5% | 209.986s |
| Full | 54 | 194,329 | $0.02831076 | +20.4% | 212.655s |

This subset is a descriptive comparison. It is not an unbiased estimate across all tasks, because success determines inclusion.
It shows that the two failed baseline tasks do not fully explain the missing savings.
The complete matrix remains necessary for quality reporting.

For those six tasks, Full used almost identical uncached input: 39,250 tokens versus 39,268.
It added 58,752 cache-read tokens and 1,070 output tokens.
Those additions explain its $0.00480372 extra estimated cost under the recorded prices.
Better caching does not make additional turns free.

## Why the older 40%+ savings do not transfer

### Scenario benchmarks changed both the context and the instructions

| September 23 scenario | Baseline turns | Full turns | Full prompt savings | Full reported cost savings |
| --- | ---: | ---: | ---: | ---: |
| TS Monorepo | 50 | 8 | 74.3% | 61.3% |
| Python ML | 37 | 5 | 79.1% | 55.8% |
| Rust Service | 34 | 8 | 54.7% | 45.7% |
| Aggregate | 121 | 21 | 70.9% | 55.0% |

The older [baseline loop](../../../pkg/hooks/picoder_e2e_test.go) mandated exhaustive discovery, individual file reads, and one tool call per turn.
It even specified Go directories and commands for the TypeScript, Python, and Rust scenarios.
Its command adapter returned saved logs or a fabricated success message instead of executing arbitrary shell commands.
These constraints weakened the baseline compared with a native coding agent.

The [scenario harness](../../../pkg/hooks/scenarios_e2e_test.go) pre-indexed source files.
It then called [buildContextPack](../../../pkg/hooks/context_pack_test.go) before the Full model loop.
Full started with skeletons, discovery results, and extracted log evidence in its system prompt.
Dedicated `tzro_probe`, `tzro_skeleton`, and `tzro_expand` tools made the intended workflow explicit.
Context preparation time was outside the model loop's `WallClockMs` measurement.

The older hooks-only condition saved 8.1% in aggregate cost, not 40%+.
Its individual results ranged from a 9.1% cost increase to an 18.3% saving.
Full reduced total returned tool bytes by only 2.2%, excluding the injected context pack.
The much larger cost reduction accompanied the reduction from 121 to 21 turns.

These results demonstrate the potential of prepared local context.
They do not isolate the benefit of installing the current product.

### The older cache comparison also changed model behavior

The saved cache comparison reports a 48.6% cost reduction.
Its direct run took 29 requests, while the proxied run took 18.

| Tool | Direct run | Proxied run |
| --- | ---: | ---: |
| `tzro_probe` | 1 | 1 |
| `tzro_skeleton` | 9 | 1 |
| `tzro_expand` | 12 | 1 |
| `read_file` | 6 | 10 |
| `run_command` | 0 | 4 |

Direct repeatedly alternated skeletons and expansions. Proxied used more complete file reads and commands, then finished earlier.
Both conditions already included Tzro tools and hooks.
The average warm-turn cache ratio increased from 85.80% to 89.88%, or 4.08 percentage points.
The 48.6% cost difference cannot therefore be attributed solely to the Proxy Shield.
A controlled cache replay must hold the request sequence constant.

### The previous installation run already lacked those savings

The committed four-task JSON at `e8c16ae`, timestamp `2026-09-28T22:10:26Z`, reports these costs:

- Baseline: $0.01266948.
- Standard: $0.01418898, approximately 12.0% higher.
- Full: $0.01558392, approximately 23.0% higher.

The latest eight-task run increased the measured disadvantage, but did not introduce the initial discontinuity.

## Failure modes in the latest run

| Category | Evidence | Interpretation |
| --- | --- | --- |
| Planning: CLI Toolkit bypass | No `probe`, `search`, `context`, `impact`, `skeleton`, `expand`, or `compact` invocation in either Tzro profile | Most intended savings mechanisms did not execute explicitly |
| Planning: runtime bypass | Zero `execute`, decision, or extraction activity during Full tasks | Runtime readiness did not produce local offloading |
| Planning: redundant ingestion | Standard task 7 retained identical data in both `tbl_c46275e8eba4` and `orders` | Automatic ingestion and the model's explicit ingestion duplicated work |
| Planning/completion: false completion | Baseline task 3 claimed completion, but grading found `Item.Tags` absent | The model ended without the required implementation |
| Planning/completion: premature stop | Baseline task 6 ended with a statement about reading source next | The required panic repair remained incomplete |
| Parameter or operational error: unresolved | One tool error in Standard task 7 and one in Baseline task 4 | The report omits arguments and error bodies, so precise classification is unavailable |
| Evaluation: weak feature coverage | Prompts reveal exact locations and repairs, and the CSV test exposes the answer | A named mechanism is not proof that the task exercises it |
| Observability: insufficient trace | Native tool events are counted and then discarded | Exact tool sequences and overhead attribution cannot be recovered |

### What actually executed

Across the 16 Tzro cells, the recorded activity contains:

- 132 hook invocations.
- Eight proxy starts.
- Two explicit `ingest` calls, one in each task 7 cell.
- Three explicit `query` calls, two in Standard and one in Full.

Full recorded 69 proxy requests, matching its 69 reported assistant requests.
This supports proxy routing. It does not establish that every hook transformed its output successfully.
The hook extension catches errors and leaves the original result unchanged.
The activity event records command entry, not successful transformation or byte savings.

The `skill_loaded` field also overstates its evidence.
[discoverSkill](../../../pkg/benchmark/workflow/pi.go) checks whether Pi lists `skill:tzro` during preflight.
It does not check whether the task agent reads or obeys `SKILL.md`.
The installed Pi source puts skill descriptions and paths in the system prompt, then asks the model to read applicable skills.
The latest runner supplies no native Tzro tool schemas and disables context files during tasks.
This creates an extra selection step compared with the older explicit tool interface.

### Hooks work selectively

A replay with the exact installed binary produced these results:

| Input | Original bytes | Hook result bytes | Result |
| --- | ---: | ---: | --- |
| Short successful Go test output | 22 | 22 | Unchanged |
| Saved `service.go` source | 18,624 | 18,624 | Unchanged |
| Task 7 CSV | 5,560 | 796 | 85.7% smaller |

The automatic hook does not perform AST skeletonization on ordinary source reads.
The source reduction requires an explicit CLI call.
The saved task stores contain no log artifacts. Standard task 7 contains the sole automatic tabular artifact.
This supports limited automatic transformation in this run, rather than broad compression across every tool result.
Short transformations without artifacts remain possible.

The tabular task illustrates the workflow cost:
Baseline used eight requests, Standard used fourteen, and Full used thirteen.
Their estimated costs were $0.00475, $0.00667, and $0.00696 respectively.
The extra query in Standard coincided with one tool error, but its cause is unknown.

### Fixtures leave little discovery work

[Task 5](../../../pkg/benchmark/workflow/tasks_realistic.go) names `service.go`, the enterprise discount, and the exact test.
Task 6 names the panic location and supplies the required `EMPTY` behavior.
Task 8 names `pkg/auth/authenticator.go`, `VerifyToken`, and the required interface operation.
Large surrounding files do not force broad investigation when the target is already known.

Task 7 contains 110 rows, despite source comments that describe roughly 120.
Its five relevant rows come first, and its visible test states the expected answer, `3450.00`.
Every profile returned that constant.
The grader proves the expected output for this fixture, but cannot prove that correct data analysis was necessary.

## Reporting and accounting defects

1. The generator hardcodes 4/4 success, twelve cells, old output totals, and `100%` in each aggregate success label.
   Regeneration from the latest JSON produces `6/8 (100%)` for Baseline.
   The checked-in markdown includes manual corrections that the generator does not reproduce.
2. The generator always prints `clean tree`.
   The JSON records modified tracked files, untracked fixtures, and `binary_vcs.modified=true`.
   The recorded Git diff hash also excludes untracked file contents.
3. [runTask](../../../pkg/benchmark/workflow/execute.go) retries a connection error after resetting usage, tool counts, and final response.
   It overwrites elapsed time and does not fully reset workspace or activity state.
   This can omit first-attempt costs and preserve duplicate activity.
   No saved evidence proves that this branch affected the latest results.
4. The runner uses `--no-session` and retains only aggregate native tool counters.
   Hook records lack tool names, arguments, raw bytes, transformed bytes, and outcomes.
5. Older scenario costs come from provider usage. Latest costs use caller-supplied prices.
   Older `PromptTokens` includes cached input. Pi's `input` excludes cache reads and writes.
   Equivalent prompt totals require `input + cache_read + cache_write` in the latest format.
   The current total-token sum is correct for Pi's schema. This is not a cache double-counting defect.

The observed cost increase survives recomputation from JSON.
The report defects reduce trust and reproducibility, but do not explain away the higher recorded cost.

## Recommended next experiment

1. Record native tool names, arguments, outcomes, and per-turn usage with secret redaction.
   Record hook input/output sizes and actual skill reads.
   Preserve all attempts, including their costs and workspace state.
2. Generate every report claim from structured data.
   Preserve an immutable source snapshot, task hashes, client version, and dirty-tree evidence.
3. Repeat the same native-installation tasks with balanced profile order and multiple runs per cell.
   Keep failed runs visible. Report variation and matched-success comparisons separately.
4. Add tasks that require unknown-symbol discovery, diagnosis from logs, and data aggregation without visible expected answers.
   Vary repository and dataset sizes to find where setup and extra turns become worthwhile.
5. Compare explicit CLI use and prepared context in separately labeled diagnostic conditions.
   Keep the installation benchmark faithful to the actual product.
   Evaluate any automatic context preparation through the real installer and client integration before claiming installation savings.
6. Replay identical requests for cache attribution.
   Separate savings from fewer turns, smaller tool results, and provider cache discounts.

These recommendations follow the installation benchmark decision and the evidence principles in [ADR-0093](../../adr/0093-benchmark-harness-hardening-and-invariant-enforcement.md).
Restoring benchmark-only context injection would obscure the adoption gap rather than resolve it.

## Initial diagnosis status

The initial diagnosis was complete before product or benchmark changes began.
The strongest explanation is a methodology discontinuity, followed by low tool adoption and extra turns in small, highly directed tasks.
The exact native tool sequence, individual tool error causes, and historical variance remain unknown.


## Implementation and validation follow-up

The user set a release gate of at least 40% Standard total-token savings and raised the combined provider budget to $10.
The new runner retains native tool payloads, assistant-turn usage, skill reads, hook transformation sizes, source snapshots, and progress reports.
It rotates profile order across repetitions and grades private checks after the agent exits.
The report generator now derives success rates, source state, and gate results from measured data.
The release workflow requires passing measurements, their native traces, and matching product source before it builds or uploads.

The ordinary Pi installer now provides a native tzro tool and recoverable structural views for complete source reads.
Explicit source slices remain exact. Tabular envelopes tell the model that import is already complete.
Go stack compaction recognizes standard-library function/location pairs and preserves application frames.
These changes use tool semantics and content, with no task IDs or benchmark fixture conditions.

### Retained diagnostic failures

- [First diagnostic](../../benchmarks/workflows-20260928-diagnostic.md): Baseline emitted tool-call-like XML as a final response on the cache task. The data task later reached its turn limit.
- The isolated PATH omitted Python and uniq, and data tasks exposed irrelevant Go files. Those harness defects were corrected equally for every profile.
- [First repeated attempt](../../benchmarks/workflows-20260928-validation.md): Full authentication stopped when the default file-path privacy rule matched the ordinary test string secret-auth-token-12345.
- The policy now evaluates recognizable path references and structured path fields. Credential redaction and blocked-file enforcement remain covered by proxy tests.
- [Second repeated attempt](../../benchmarks/workflows-20260928-validation-v2.md): All twelve coding cells passed. The run was deliberately stopped when the shared PATH was found to omit gofmt.
- The harness now exposes all ordinary host PATH executables equally and records the inventory. It excludes the product and optional worker entry points from Baseline.
- Automatic source substitution now verifies each stored body. If recovery is unavailable, the hook preserves the original source.
- Proxy lifetime now extends through final metrics collection after client cancellation.

The failed attempts remain in separate reports and in the spending ledger. They do not count as complete release-validation repetitions.
A third matched three-repetition matrix completed against the corrected product and shared tool environment.
The full Go race suite and actual Pi smoke passed after the corrections.

### Completed matched validation

The [completed report](../../benchmarks/workflows-20260928-validation-v3.md) contains 63 cells that passed their configured checks, with complete native usage.
Later invoice-level testing exposed a fixture deadlock missed by its helper-only billing checks; see the follow-up below.

| Profile | Passed | Requests | Total tokens | Conservative estimated cost |
| --- | --- | --- | --- | --- |
| Baseline | 21/21 | 200 | 948,753 | $0.773867 |
| Standard | 21/21 | 207 | 816,826 | $0.683508 |
| Full | 21/21 | 229 | 1,135,140 | $0.927592 |

Standard saved 13.9% total tokens and 11.7% estimated cost overall.
Its per-repetition token savings were 33.4%, -20.1%, and 19.2%. None reached the required 40%.
Full used 19.6% more tokens. Release remains blocked.

Standard hook output fell from 160,976 to 93,714 bytes, a 41.8% reduction.
This reduction did not include tool-schema overhead or conversation growth across additional requests.
One Full billing cell made 17 separate expansion calls after receiving an automatic structural view and requesting another skeleton.
Other observed overhead included incorrect absolute paths, unrelated Pi README reads, and repeated calculations through SQL, Python, and awk.
Two Standard expansion calls failed because the model copied the displayed hash prefix literally.

No measured task invoked graph execution, the decision runtime, or the extractor.
No task explicitly invoked context, probe, search, impact, or compact.
The workers passed readiness checks, but readiness did not establish task adoption.

The saved report has an empty client_version field. Pi printed its version to stderr; preflight captured stdout only.
A post-run regression test reproduced this defect and acceptance of empty version output.
The runner now captures both streams and rejects empty output. The regression passed with the race detector.
Raw reports retain the original metadata. Their source snapshots describe the measured implementation before this correction.
Source, trace checksums, and native usage were verified before the correction; portable evidence is retained in the working scratchpad.

The [billing audit](../../benchmarks/billing-20260928.json) reconciles all 965 recorded workflow generations, totaling $0.52550514.
The final matrix accounts for $0.35913064 of that amount. Including cache replay, confirmed charges total $0.53436066.
Unrecorded interrupted usage remains reserved. The conservative ledger holds $8.429676 of the authorized $10 cap.

### Controlled cache replay

The [fixed-request replay](../../benchmarks/cache-replay-20260928.md) sent twelve identical request pairs, with route order counterbalanced.
Upstream captures show three tool-prefix variants per repetition for Direct and one for Proxy.
Both routes used 29,700 input tokens. Direct cached 19,997; Proxy cached 17,620.
Provider-reported costs were $0.00414312 and $0.00471240.
Shared provider caches and backend placement remain uncontrolled. The small cost difference is descriptive, not a causal penalty estimate.
The replay verifies normalization, but supplies no token-saving or cache-advantage claim.

### Graph execution and tool granularity

The user identified a further hypothesis: individual primitive calls may prevent the intended savings from local graph execution.
At diagnosis time, the Pi adapter supported an execute command, but its description explained individual primitives and provided no graph schema.
The MCP frontend exposed a dedicated tzro_execute_graph tool. The native Pi adapter lacked that structured entry point.
Its argv-only interface also requires a separate graph-file write or a shell pipeline.
The installed skill puts graph details in a secondary reference, which incorrectly implies that all graphs require experimental workers.
Source inspection and a local CLI replay confirm that deterministic tool graphs already work in Standard.
Only decision and extraction nodes require optional workers.

An offline replay batched the first Standard data cell's import and five SQL queries into one six-node graph.
All query results matched the recorded calls. Execution took approximately 25 ms, with zero cloud requests and workers disabled.
This replay used a known plan. It proves execution capability, not natural graph adoption or cloud-token savings.
The completed paid matrix measures the installed interface as supplied; it contains no graph adoption.

At diagnosis time, the graph dispatcher had no native expand, context, or impact node handler.
Shell nodes can invoke those CLI commands, but require additional planning.
The graph response includes intermediate node outputs as well as selected returns, which can duplicate context.
A useful next integration should accept structured graphs directly and return bounded evidence through the ordinary installation.
Benchmark-only graph instructions or prepared plans would not establish that the installation itself produces the savings.

The description should explain delegated work before introducing graph syntax:
"Run a multi-step workflow locally in one call. Execute tools, use configured local models for decisions and extraction, and return results or yield when cloud reasoning is needed."
The Go executor schedules nodes. System 1 handles bounded decisions and extraction; cloud reasoning still supplies the plan and code changes.
Standard currently supports deterministic graphs without local models. Full configures the workers.
Whether Standard should include System 1 is a separate installation-scope decision pending user clarification.

### Graph interface follow-up

After validation-v3, the ordinary Pi extension gained a structured tzro_execute_graph tool, sharing its schema and description with MCP.
Selected results retain full intermediate output locally with an expansion pointer. Failures and low-confidence yields remain visible.
The graph dispatcher now supports expansion. CLI expansion also accepts the displayed hash prefix, fixing the two recorded recovery errors.
Shell cancellation now stops descendant processes. Private graph input is removed after success, failure, or cancellation.
The installed reference now explains local workflow delegation and includes a complete example.

The real Pi protocol check verifies dependent steps, extraction-to-tool wiring, and decision input in one call.
Local checks cover storage failure, oversized results, missing workers, low-confidence yields, and cancellation.
The full race run passed every package except a new extractor test-fixture error; the corrected workflow package subsequently passed under race.
The offline query replay preserved all selected results and reduced its response from 3,208 to 1,528 bytes.

A separate check with real local workers revealed a decision-quality limitation.
GLiNER extracted main.go and the graph retrieved a Go skeleton, but JEV selected Python with confidence 0.655.
The input was present and short. No evidence shows adapter truncation caused this answer.
Adding a 0.8 acceptance threshold made the graph yield and block dependent work, without cloud requests.
This known-plan diagnostic does not establish general model accuracy or natural graph adoption.
The previous paid matrix invoked neither worker, so this result cannot explain that matrix's token totals.

Validation-v4 attempted 28 of 63 cells: 26 passed, Standard stopped prematurely on revenue_256, and Baseline billing timed out.
No task invoked the graph or either worker. Task prompts contained no graph plan or tzro-specific instruction.
In its first repetition, Standard used 20.9% more tokens across the six mutually successful pairs.
The incomplete data task cannot support a savings claim.
The Standard initial control prompt grew from 2,147 tokens in v3 to 2,863 in v4. The structured schema adds overhead without observed adoption.

### Billing fixture deadlock and narrower validation scope

The timed-out Baseline cell added a valid invoice integration test after fixing the requested discount.
GenerateInvoice held the write lock and called CalculateTax, which requested a read lock on the same RWMutex.
A two-second isolated reproduction retained the blocked stack. All nine saved v3 billing workspaces contain the same defect.
Better agent verification exposed an unrelated fixture failure. This cannot count as a product or graph failure.

The fixture now shares a private tax helper between the locked public methods, avoiding nested locking.
Hidden checks verify enterprise, pro, and basic invoice totals and tax through the public account/invoice API.
The new check failed before the fix and passed afterward under the race detector.
Fixture and grader hashes changed; earlier runs cannot validate this corrected suite.

All 1,199 recorded workflow generations have receipts totaling $0.65323255. Cache replay adds $0.00885552.
The v4 runner ended while a local bash tool was blocked. All 234 recorded assistant completions have receipts.
A budget-only audit retains its ceiling-priced recorded usage plus one full extra-request reserve: $0.937779.
Its raw usage remains incomplete and its release gate remains failed. Other interrupted-run reservations are unchanged.
Aggregate charged/reserved is $7.34161864 of $10 before the next run; this is a conservative budget amount, not the provider bill.

The user requested that subsequent paid runs omit Full. Validation now targets Baseline and Standard across seven tasks and three repetitions.
The savings, cost, quality, and aggregate spending requirements remain unchanged.

### Updated validation requirements

During validation-v5, the user raised the aggregate provider cap to $20 and revised the token-savings target from 40% to 33%.
The gate still requires three complete repetitions, lower estimated cost, and no paired quality regression.
Only the evaluation policy changed during the run; the measured product, prompts, fixture files, and hidden grading stayed fixed.
Report and release verification share the 33% threshold. Boundary tests accept 33% and reject 32%; existing quality and evidence checks remain enforced.
Historical reports retain their original 40% evaluations. Further paid runs compare Baseline and Standard only.

### Completed Baseline/Standard validation-v5

The [paired report](../../benchmarks/workflows-20260928-validation-v5.md) contains 42 successful cells with complete native usage.
It uses the corrected invoice fixture and stronger hidden checks. Full was excluded at the user’s request.

| Profile | Passed | Model requests | Total tokens | Conservative estimated cost |
| --- | --- | --- | --- | --- |
| Baseline | 21/21 | 190 | 1,083,693 | $0.887828 |
| Standard | 21/21 | 185 | 885,465 | $0.733057 |

Standard saved 18.3% total tokens and 17.4% estimated cost overall.
Per-repetition token savings were 31.8%, 6.8%, and 3.1%. None met the revised 33% target.
Product source, source-archive checksum, all native traces, usage totals, and calculated estimates passed verification.
The release verifier rejected only the three savings thresholds. No release or push occurred.

Observed behavior explains why output reduction did not translate into the target savings:

- **No graph adoption:** zero native graph calls and zero graph runtime activity. Standard executes deterministic graphs, but supplies no System 1 workers.
- **Sparse discovery:** one successful context invocation, after initial file reads, followed by another skeleton. No probe, search, or impact calls occurred.
- **Argument recovery:** the model supplied an ingest table name and a context budget as positional arguments. Both required retries with the documented syntax.
- **Variable batching:** one billing cell expanded four bodies in a single model response. Four primitive calls therefore did not imply four cloud hops.
- **Variable exploration:** Standard authentication used 93,934, 32,919, and 48,156 tokens across identical fixtures. Baseline used 22,278, 25,308, and 77,135.
- **Variable data volume:** Baseline’s first large-data cell read 1,000 CSV rows and used 306,470 tokens. The second sampled 15 rows at each end and used 40,778.
- **Fixed prompt cost:** the first cache-control request contained 1,696 input tokens in Baseline versus 2,905 in Standard.
- **Partial output gains:** Standard hook-observed bytes fell from 198,829 to 92,975 (53.2%). Model requests fell only from 190 to 185 (2.6%).

The native Standard tool calls included nine expansions, four ingest attempts, thirteen queries, two context attempts, and one skeleton.
Additional CLI calls occurred through bash; runtime evidence records sixteen successful query invocations in total.
No Standard cell read the installed skill. Resource discovery and schema availability did not ensure efficient tool use.
Pi’s normal system-prompt construction includes the extension’s snippets and guidelines; no custom system prompt replaced them in the inspected Standard home.

The large-data pair illustrates the baseline effect particularly clearly: Standard saved 68.8%, then 25.1%, then used 1.2% more tokens.
All six cells passed. These changes track different read and calculation sequences rather than a change in fixture size.
The earlier forced-exhaustive baseline cannot establish the same advantage over an unrestricted native agent.

Portable source and native traces are retained in the working scratchpad. Billing reconciliation is separate from release evidence.
The spending ledger now reflects the authorized $20 cap and preserves all unresolved earlier reservations.

All 375 v5 generations have provider receipts: Baseline $0.14711181, Standard $0.12202452, total $0.26913633.
Across this investigation, 1,574 recorded workflow generations total $0.92236888; cache replay adds $0.00885552.
The conservative ledger holds $7.61075497 of the authorized $20, including unresolved earlier usage reserves.
No billing record is missing for a recorded generation. Unrecorded interrupted usage remains reserved.

### Offline context retrieval follow-up

The existing context assembler was tested on pristine copies of all seven fixtures with their original task prompts.
Each task used budgets of 1,000, 1,500, and 2,000 tokens. These checks made no provider requests.

Two general defects reproduced on independent source fixtures:

- The assembler assigned every symbol match the same score, discarding the search engine's relevance order.
- Filename matching retained sentence punctuation, so an explicitly named document such as `architecture.md.` was missed.

Both defects are fixed in `pkg/context/context.go`, with regressions in `pkg/context/retrieval_test.go`.
The context, store, and tokenizer race suites passed. The fixture replay now includes billing's README at all three budgets.
At budget 1,500, this adds 49 content tokens of previously missing evidence.

The remaining results do not justify automatic context injection. Authentication still returns entries spanning all 12 files.
Both data tasks and the incident task return empty packs with explicit truncation notices.
The full task prompts also retrieve weakly relevant symbols. Preserving search order does not fix query relevance by itself.

The content budget excludes Markdown metadata, and the recorded serialized count precedes trace-ID insertion.
This is a separate accounting issue. Local tokenizer counts are not provider usage measurements.
The repairs have not been measured in a paid workflow run and cannot establish the 33% release target.
Before/after artifacts are retained under `.scratch/standard-savings/context-diagnostic/`.

### Body elision and repeated context

The skeletonizer replaced even empty functions and one-line returns with larger hash markers.
Skeletons and declaration spans now retain a body when the marker would use at least as many local tokens.
Symbol indexing and exact body recovery remain available. Independent Go, Python, and TypeScript regressions reproduced the defect before the fix.
Relevant race suites, CLI checks, and the actual Pi client with a fixture provider passed afterward.

On pristine authentication input, this reduced context-pack content from 1,047 to 937 tokens at a 1,500-token budget.
All 25 entries remained included. Billing decreased from 962 to 958 tokens. These are local counts, not workflow savings.
All nine v5 expansion results had bodies of 52–237 tokens and 12–36 lines; the correction cannot be credited with removing those calls.

A separate counterfactual retained two assistant turns before replacing old successful outputs with an 80-token recovery notice.
For outputs of at least 512 tokens, it removed 52,201, 18,438, and 8,136 repeated local tokens across the three repetitions.
It assumes unchanged subsequent calls and excludes recovery requests and cache effects. No history replacement was implemented.

The native tool description now shows the explicit budget and table-name flags that the v5 agent initially omitted.
Progressive native tool exposure is proposed separately, with a user decision pending because it changes the default interface.
No additional provider spend or validated savings resulted from these offline checks.

## Diagnostic-v6: graph adoption exposed missing read support

The [one-repetition diagnostic](../../benchmarks/workflows-20260929-diagnostic-v6.md) passed all 14 task checks.
Baseline used 61 requests and 228,102 tokens; Standard used 59 requests and 261,723 tokens.
Standard used 14.7% more tokens and 13.5% more estimated cost.
Observed hook output fell 58.3%, but extra instructions, recovery work, and retained history outweighed that reduction.
This run cannot meet the three-repetition release requirement. V5 remains the latest complete repeated validation.

Standard attempted its first natural graph during authentication: eleven source-file reads with whole-output return paths.
Pi rejected the graph because the shared tool schema omitted read. No graph executed.
The model then used native reads; separate tool calls may share a model response.
The executor also rejected whole-output references and silently omitted these requested returns.
This gives direct evidence for an interface failure when the model tried to use graphs.
It does not establish that graph execution alone would reach 33% savings.

Independent tests reproduced both defects. The repair adds exact graph reads and whole-output selection.
Read supports line slices, workspace-relative paths, explicit policy failures, and bounded output.
The installed reference also documents query's existing optional file import.
Local integration verification and later natural-use measurements remain separate from the retained v6 evidence.
All 120 v6 requests have provider receipts, totaling $0.06808552.

## Diagnostic-v7: first successful natural graph, overall regression

The [next diagnostic](../../benchmarks/workflows-20260929-diagnostic-v7.md) passed all 14 task checks.
Baseline used 66 requests and 243,175 tokens; Standard used 68 requests and 309,359 tokens.
Standard used 27.2% more total tokens and 26.8% more conservatively estimated cost.
Provider receipts cover all 134 requests: Baseline $0.032374742, Standard $0.035598836, total $0.067973578.
The complete source, trace, and accounting audit passed. This one repetition does not satisfy release validation.

On the 256-row data task, Standard naturally ran six SQL queries in one successful graph call.
The call took 31 ms and returned 1,165 bytes. The task saved 43.2% tokens versus Baseline.
These query nodes need no local inference model. The new read primitive was not selected in this paid run.
An offline replay separately verified the eleven-read graph that failed during v6.
All eleven requested whole outputs now return successfully, with unchanged fixture files.

Billing still exposed redundant skeletonization, three serial expansions, and repeated source slices.
Standard also tried a missing absolute skill path and attempted to ingest a plain-text incident log as tabular data.
Expected failing authentication tests occurred in both profiles and are separate from these interface mistakes.
Hook-observed output shrank 55.4%, but the complete workflow used more tokens and time.

The graph repair passed executor, hook, privacy, and CLI race checks.
The installed Pi client passed against a local fixture provider, including graph reads and whole-output selection.
Large graph output remained recoverable from local artifacts. No release or push occurred.
The proposed progressive native tool interface remains unimplemented, pending the earlier user choice.
