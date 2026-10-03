# Benchmark installation profiles

`tzro bench workflows` compares the same coding tasks through an installed Pi-Coder client.
The recipe identifier is `tzro.installation-profiles.v2`.

| Profile | Installation |
| --- | --- |
| Baseline | Pi without tzro resources or tzro on PATH |
| Standard | The real `install.sh`, using an explicit local tzro binary |
| Full | Standard plus proxy routing, JEV decisions, and GLiNER extraction |

Standard and Full use the same installed skill and hook extension.
The ordinary Pi extension registers native `tzro` and `tzro_execute_graph` tools, a skill, and a tool-result hook.
Pi does not need an MCP bridge for these commands.
Other clients' MCP configuration remains covered by the installer tests.
Deterministic graphs containing tool nodes already work in Standard.
Decision and extraction nodes require the optional workers configured by Full.
The graph tool accepts structured input in one call and returns selected results, failures, or a yield.
Full intermediate output is retained locally with an expansion pointer. The installed reference includes a working graph example.
Validation-v4 exposed no graph calls in 28 attempted cells. It stopped after an unrelated billing-fixture deadlock.
The fixture is corrected and invoice-level grading now covers the public API. Earlier helper-only billing passes do not prove invoice correctness.
Validation-v5 completed all 42 Baseline/Standard cells successfully. Standard saved 18.3% total tokens overall, with no graph calls.
Its three repetitions saved 31.8%, 6.8%, and 3.1%, below the current 33% gate.

The recipe uses the native client's normal tools and resource loading.
It does not inject special benchmark tools, prepared context packs, or profile-specific task instructions.
The default `representative` suite contains seven tasks: two small coding controls, billing repair, authentication discovery,
incident diagnosis, and revenue analysis at 256 and 8,192 rows.
The `legacy` suite preserves the eight historical fixtures for diagnosis.
Task prompts do not prescribe tools. Baseline can use efficient shell commands and targeted reads.
Additional grading checks are written to a separate copy only after the agent exits.
The same fixture files, instructions, timeouts, turn limits, and grading rules apply to every profile.

## Local preflight

Build the CLI and install Pi-Coder before running this recipe.
The local integration checks used Pi 0.74.2. Reports record the detected client version.

```sh
go build -o bin/tzro ./cmd/tzro
bin/tzro bench workflows --model YOUR_MODEL_ID --profiles baseline,standard
```

The default command checks setup without provider requests.
Each task and profile receives a new home, workspace, client configuration, Go cache, and tzro store.
All profiles receive the same ordinary host executables, including language toolchains and formatting tools.
The host tzro and optional runtime entry points are excluded; Standard and Full receive the selected installed binary.
The report records the shared tool inventory.
Use `--work-dir` to choose a new directory outside any project or agent configuration tree.
The default uses a system temporary directory.
The report is written to `report.json` inside that directory. Existing reports are not overwritten.

Full requires every optional dependency listed below.
If any selected profile fails preflight, no selected profile sends model requests.
The report preserves each setup failure and marks the remaining tasks as not run.

## Prepare Full

These commands provision optional dependencies in the source checkout.
They do not change Standard installation.

Build [the native scorer](../cmd/jev-score/README.md) against a recent libllama with Qwen3.5 support:

```sh
brew install llama.cpp
bash scripts/build_jev_score.sh
python3 -m venv .venv-full
.venv-full/bin/pip install gliner2 huggingface_hub
.venv-full/bin/pip freeze > .scratch/full-runtime-requirements.txt
```

The current extractor uses the GLiNER Python/PyTorch checkpoint.
The legacy `bin/setup_models.sh` still references Laya and an ONNX download; it does not provision this recipe.

Download the publisher's model files:

```sh
.venv-full/bin/hf download chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF \
  Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf \
  --revision edf37c26a1098f83cf4264b8adbe0dca2d2ebb0c --local-dir models/decision
.venv-full/bin/hf download fastino/gliner2.5-base-v1 --local-dir models/gliner2.5-base-v1
```

The [JEV publisher](https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF) provides the GGUF weights and readout contract.
The [GLiNER publisher](https://huggingface.co/fastino/gliner2.5-base-v1) provides the extractor checkpoint.
Record the resolved model revision and dependency versions when publishing results.
The report also hashes the supplied model files, executables, and worker script.
Download time and model storage are separate setup costs; the runner does not measure them.
Model memory is additional to the core CLI footprint.

Run Full preflight from the checkout:

```sh
bin/tzro bench workflows --model YOUR_MODEL_ID \
  --decision-bin "$PWD/bin/jev-score" \
  --decision-model "$PWD/models/decision/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf" \
  --decision-version 'JEV v3; record your libllama build here' \
  --extractor-bin "$PWD/.venv-full/bin/python" \
  --extractor-arg "$PWD/bin/gliner_worker.py" \
  --extractor-model "$PWD/models/gliner2.5-base-v1" \
  --extractor-version 'record gliner2 and torch versions here'
```

Both runtime workers must load and answer a local readiness request.
Full also starts the public proxy and checks its local route without contacting the provider.
Readiness workers stop before task execution. Their store is removed before task timing.
Missing dependencies produce `setup_incomplete`; the runner does not substitute a reduced Full profile.

## Execute tasks

After preflight passes, add `--run` and provide all four token prices explicitly.
Set `TZRO_BENCH_API_KEY` in the environment. The runner passes it to the client without writing it to configuration.
Use `--base-url` for an OpenAI-compatible endpoint ending in `/v1`.

```sh
bin/tzro bench workflows --model YOUR_MODEL_ID --profiles baseline,standard \
  --run --repeats 3 --max-cost 2 --timeout 3m --max-turns 20 \
  --input-price 1 --output-price 2 --cache-read-price 0.1 --cache-write-price 1
```

The prices above are examples in USD per million tokens. Supply the actual prices for the selected provider and model.
Full uses the same execution flags plus its runtime paths.
Use `--tasks` to select fixture IDs shown in the preflight report.

The recipe advertises a 128,000-token context and an 8,192-token output limit to Pi.
Use a model compatible with those limits. Reasoning is disabled equally across profiles.
`--repeats` creates independent cells. The profile order rotates across tasks and repetitions.
Provider caches and operating-system caches remain uncontrolled.

Cost is an estimate from native client usage and supplied prices.
The guard runs after reported assistant messages; an in-flight request can exceed the limit.
A retry, automatic compaction, or incomplete usage stops further requests because the remaining cost is unknown.
These controls are not a provider billing cap.

For several paid runs, use `scripts/run_workflow_validation.py` or `make benchmark-run`.
The wrapper keeps a shared spending ledger and reserves one extra maximum-size request.
Supply conservative prices that cover every permitted provider route.
An interrupted run retains its full reservation until its usage is reconciled.
The ledger does not track calls made outside the wrapper.
`make benchmark-publish REPORT_JSON=...` only renders saved evidence. It never starts a paid run.

## Read the evidence

Each result records task success, completion state, input/output/cache tokens, estimated cost, and failures.
Tests, `go.mod`, and task input datasets must remain unchanged for grading to pass.
The report preserves timeouts and failed tasks.
It records each assistant turn, native tool name, arguments, result, error, and duration.
Semantic events are retained in a redacted `task-events.jsonl` file with a SHA-256 checksum.
`progress.json` is updated after each cell. Failed attempts are not silently retried.
The run also retains an archive of the source tree, including dirty and non-ignored untracked files.

`setup_ms` covers workspace preparation and installation.
`preflight_ms` covers resource discovery and runtime readiness.
`agent_ms` includes native client startup, Full's proxy startup, and runtime calls during the task.
`grade_ms` covers the independent Go test check.
Runtime model startup during a task remains inside agent time.

`runtime_ready` describes preflight only.
`skill_loaded` is a legacy field name for resource discovery. Only `skill_read` records an observed successful skill read.
`activity` records observed CLI, graph, decision, and extractor outcomes during the task.
Hook events include input/output byte counts, the applied transformation, and raw fallbacks.
`proxy_requests` records requests observed by Full's proxy.
A ready runtime with no task invocation does not count as runtime use.
The recipe performs no hidden fallback; it records failures if the agent attempts an unavailable capability.

The normal Pi hook converts eligible complete source reads into structural views with recoverable bodies.
Explicit line slices remain exact. Query envelopes identify data that was already imported.
These rules use content and tool arguments. They do not inspect task IDs or benchmark flags.

## Release validation

The Standard gate requires at least 33% fewer total tokens in each of three complete repetitions.
The user revised this target from 40% during validation-v5; measured task inputs remain unchanged.
Estimated cost must decrease, and no successful Baseline cell may become a failed Standard cell.
Total tokens include uncached input, cached input, cache writes, and output.
All seven representative tasks remain in the gate, including failures and small controls.
The report also shows matched successful pairs as a separate descriptive comparison.
The guarded runner and Make targets default to Baseline and Standard: 42 cells across three repetitions.
Full remains available through an explicit profile selection, but is excluded from the current validation work.

```sh
python3 scripts/generate_benchmark_report.py run.json run.md --require-gate
python3 scripts/verify_release_benchmark.py run.json
```

The verifier checks source and trace checksums, recalculates usage from native events, and compares the measured product source with the checkout.
The release workflow requires `docs/benchmarks/release-validation.json` and its referenced evidence before it builds or uploads binaries.
Missing, stale, incomplete, or insufficient evidence blocks release.
Use `scripts/bundle_benchmark_evidence.py run.json docs/benchmarks/release-validation.json` to retain portable evidence for CI.
The bundle command copies local files only. A generated PASS field alone cannot authorize release.

Different agent tool sequences cannot establish a causal proxy-cache benefit.
Use an identical-request replay for that separate component claim.
Installation-profile results measure the complete agent workflow, including tool recovery and extra turns.

Homes and PATH are isolated for reproducibility. They are not an operating-system security sandbox.
Review task files and dependencies before running arbitrary external fixtures.
Reports include local paths and source state. Review artifacts before publication.

## Verify without paid requests

```sh
TZRO_NATIVE_PI_SMOKE=1 go test ./pkg/benchmark/workflow -run TestNativePiProfiles -count=1
```

This smoke test runs the installed Pi client against a loopback response fixture.
It verifies skill loading, hook invocation, proxy routing, runtime invocation evidence, usage, and grading.
Set `TZRO_TEST_JEV_BIN` and `TZRO_TEST_JEV_MODEL` to include the real JEV worker.
GLiNER responses remain fixtures in this test.
These checks establish integration behavior, not token savings or task-quality results.

`tzro bench signal-density` and the existing direct/proxy comparisons remain component diagnostics.
Published installation-profile results require separate paid evaluation and artifact review.
