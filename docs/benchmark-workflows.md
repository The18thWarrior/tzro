# Benchmark installation profiles

`tzro bench workflows` compares the same coding tasks through an installed Pi-Coder client.
The recipe identifier is `tzro.installation-profiles.v1`.

| Profile | Installation |
| --- | --- |
| Baseline | Pi without tzro resources or tzro on PATH |
| Standard | The real `install.sh`, using an explicit local tzro binary |
| Full | Standard plus proxy routing, System 1 graph execution, JEV decisions, and GLiNER extraction |

Standard and Full use the same installed skill and hook extension.
This Pi recipe has no verified native MCP registration. It exposes tzro through the installed skill and CLI.
Other clients' MCP configuration remains covered by the installer tests.

The recipe uses the native client's normal tools and resource loading.
It does not inject special benchmark tools, prepared context packs, or profile-specific task instructions.
Four small Go fixtures cover implementation, bug fixing, refactoring, and diagnosis.
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
  --run --max-cost 2 --timeout 3m --max-turns 20 \
  --input-price 1 --output-price 2 --cache-read-price 0.1 --cache-write-price 1
```

The prices above are examples in USD per million tokens. Supply the actual prices for the selected provider and model.
Full uses the same execution flags plus its runtime paths.
Use `--tasks` to select fixture IDs shown in the preflight report.

The recipe advertises a 128,000-token context and an 8,192-token output limit to Pi.
Use a model compatible with those limits. Reasoning is disabled equally across profiles.
Tasks run in the requested profile order. Repeat runs and vary that order before drawing comparisons.
Provider caches and operating-system caches remain uncontrolled.

Cost is an estimate from native client usage and supplied prices.
The guard runs after reported assistant messages; an in-flight request can exceed the limit.
A retry, automatic compaction, or incomplete usage stops further requests because the remaining cost is unknown.
These controls are not a provider billing cap.

## Read the evidence

Each result records task success, completion state, input/output/cache tokens, estimated cost, and failures.
Tests and `go.mod` must remain unchanged for grading to pass.
The report preserves timeouts and failed tasks.

`setup_ms` covers workspace preparation and installation.
`preflight_ms` covers resource discovery and runtime readiness.
`agent_ms` includes native client startup, Full's proxy startup, and runtime calls during the task.
`grade_ms` covers the independent Go test check.
Runtime model startup during a task remains inside agent time.

`runtime_ready` describes preflight only.
`activity` records observed CLI, graph, decision, and extractor outcomes during the task.
`proxy_requests` records requests observed by Full's proxy.
A ready runtime with no task invocation does not count as runtime use.
The recipe performs no hidden fallback; it records failures if the agent attempts an unavailable capability.

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
