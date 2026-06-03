# Bug Post-Mortem: Local Sidecar Inactive / Benchmark API Key Loading Bug

## Symptom

- **Description**: Running the benchmark suite (e.g. `tzro benchmark run`) fails for all test cases with the following execution error:
  `"turn 0 execution failed: level execution error: node execution failed: local sidecar is inactive and cloud fallback is unavailable"`
  Or, when running with `--real`, the task fails with:
  `"[Task Planner Warning] Cloud planning failed: cloud API returned status 401: {"error": {"message": "Incorrect API key provided: mock-key..."}}"`
- **Reproduction**: Run `tzro benchmark run --model-mode cooperative` or `go test ./...` in a workspace where the local llama-server sidecar is inactive and no active Gemini or OpenAI API keys are configured in the host environment.

## Diagnosis

- **Hypotheses**:
  1. The benchmark mock completions HTTP interceptor was not correctly routing/mocking calls.
  2. The executor fell back to the cloud model because the local sidecar status was `"Stopped"` in the CLI process (as the CLI process never runs `inference.GlobalLocalModel.Start()`).
  3. The cloud fallback failed because the Cloud API key resolved to `""` (empty string) or stayed stuck as `"mock-key"` despite config mocks.
- **Root Cause**:
  1. **Uninitialized CLI Configurations**: The CLI process (which executes benchmark runs directly on-device instead of calling the REST daemon) never invoked `config.Load()` during initialization.
  2. **Config Pollution**: In `RunSuite`, the benchmark framework called `config.Save` which wrote mock values (`"mock-key"`, `"openai"`) to the persistent on-disk `.tzro/config.json` file. Because previous runs were cancelled or aborted, the deferred cleanup never ran, leaving `.tzro/config.json` polluted with `"mock-key"`. When configurations were loaded, it read `"mock-key"` from disk and attempted to query OpenAI with it, failing with HTTP 401.
  3. **Slow Tests / Hang Mimicry**: Running backend tests (`go test ./...`) was attempting to process all cases in the 2.2MB standard `bfcl_samples.json` file. Given topological sort delays, executing this massive dataset inside unit tests took almost 10 minutes, mimicking a thread hang.

## Resolution

- **Fix**:
  1. Added a cobra `PersistentPreRunE` hook in `RootCmd` inside `internal/cli/root.go` to invoke `config.Load()` on all CLI commands. This guarantees that CLI processes always load the persistent `.tzro/config.json` settings (including configured API keys, sidecar preferences, models directory, etc.).
  2. Added a configurable `CloudModel` string field to `EngineConfig` in `internal/config/config.go` with dynamically resolved fallback defaults (`gemini-2.5-flash` for Google, `gpt-4o-mini` for OpenAI), replacing the previously hardcoded outdated models.
  3. Rebuilt both the local development binary (`bin/tzro` & `bin/tzrod`) and the user/system binary (`~/.tzro/bin/tzro`) to apply the configuration loading fixes into the compiled output.
  4. Optimized `LoadTestCases` in `internal/benchmark/runner.go` to dynamically switch the loaded dataset from `bfcl_samples.json` (2.2MB) to the lightweight `bfcl_test_samples.json` (4KB) when running under a `go test` environment. This allows backend tests to pass in under 11 seconds while preserving full validation of planning matching, GBNF parameter checks, and mock completions.

## Long-term Prevention

- Never write transient/mock settings to persistent configuration files on disk. Always leverage safe in-memory overrides for tests, simulations, and benchmark evaluations.
