# Use Case: Turn Reduction Benchmark Suite

**Actor**: Performance engineer, researcher, or CI pipeline evaluating LLM agent turn efficiency, tool adoption, and task completion speed
**Route**: CLI — `tzro bench turn-reduction [--run] [--conditions <list>] [--fixtures <dir>] [--output <dir>] [--client <bin>] [--offline-client <bin>] [--authorize-live] [--accept-unknown-cost] [--run-id <id>] [--ledger <path>] [--max-launches <n>] [--model <name>] [--task-timeout <dur>]`
**Backend**: Benchmark harness (`pkg/benchmark/turnreduction/`) with native agent runner, invocation recorder, independent grading, and launch guard
**Priority**: P1

---

## Intent

A performance engineer or researcher wants to empirically evaluate how effectively Tzro reduces agent conversational turns and Verified Completion Time across realistic multi-language coding tasks. Running `tzro bench turn-reduction` compares three conditions: Native (ordinary agent tools), Simple (bulk-update helper script), and Tzro (`tzro_edit_and_verify` MCP tool) across 9 standardized fixtures (Go, Python, TypeScript across bugfix, rename, and diagnosis shapes). The harness runs isolated native client processes, records high-level model invocation boundaries, tracks spend/launch allowances via a persistent ledger, enforces protected file constraints, executes independent private grading, and applies a strict full-suite correctness gate before computing speedup percentages.

## Preconditions

- Tzro binary is installed and on PATH
- Toolchains installed for target fixture languages (`go`, `python3`, `node`, `tsc`)
- Valid agent executable available (native `agy` or `--offline-client` mock)
- For live runs: valid provider credentials (`GEMINI_API_KEY` or `--env-file`), explicit `--authorize-live`, `--accept-unknown-cost`, `--run-id`, `--ledger`, and launch allowance (`--max-launches`)
- Fixture manifests present with valid version 2 schemas

## Success Criteria

- [ ] Default CLI invocation without `--run` executes readiness probing, validating toolchains and fixture contracts without launching models or incurring costs
- [ ] `--run` executes the 27-cell balanced schedule (9 fixtures × 3 conditions: Native, Simple, Tzro) to control for provider queue latency and API drift
- [ ] Freezes immutable evaluation contract (`contract.json`) hashing client executable, Tzro binary, helper script, guard script, guidance, invocation recorder, and all subject and grading files
- [ ] Mid-run contract tampering immediately halts matrix execution before the next launch
- [ ] Launch guard (`scripts/run_workflow_validation.py`) enforces launch limits and prevents unbudgeted runs or contract reuse across a persistent ledger
- [ ] Each evaluation cell runs in an isolated temporary workspace initialized from the fixture's `subject/` directory
- [ ] Native agent invocation hooks (`invocation.Recorder`) passively record `PreInvocation`, `PostInvocation`, and `Stop` events with timestamp, model, and invocation numbers without modifying prompts or injecting steps
- [ ] High-level Cloud Decision Rounds are accurately counted and validated for consecutive order and complete termination
- [ ] Incomplete invocation evidence halts matrix execution while retaining cell artifacts
- [ ] Credentials (`GEMINI_API_KEY`) are redacted across all saved events, client logs, stderr, and launch receipts
- [ ] Protects untouched files: edits outside declared `editable_files` fail the cell
- [ ] Prescribed verification checks (`final_checks`) from `.tzro/verification.yaml` execute on a copy of the final workspace
- [ ] Independent grading tests from `grading/` execute in an isolated environment using native test runners (`go test -json`, `unittest`, `tsc` + `node --test`)
- [ ] Required private tests (`required_grade_tests`) must pass for the fixture to be graded as passed
- [ ] Verified Completion Time separates failed elapsed time from passed tasks: unsuccessful time never counts toward verified speedup
- [ ] Full-suite correctness gate requires Tzro to verify 9/9 fixtures AND at least one control condition to verify 9/9 fixtures before comparing times
- [ ] Evaluates speed reduction percentage against the faster eligible control ($100 \times (1 - \text{treatment}/\text{control})$), requiring $\ge 20\%$ reduction for `screen_pass` status
- [ ] Offline runs (`--offline-client`) are explicitly labeled `offline: true` and produce status `offline_only`
- [ ] Complete evidence package is persisted in `--output` directory (report, readiness, contract, and per-cell workspace snapshots, events, logs, receipts, and results)

## Edge Cases to Probe

- Missing required toolchain during readiness check (reports clear problems list in `readiness.json`)
- Agent attempts to edit a protected file outside `editable_files` (cell fails verification)
- Agent exceeds `--task-timeout` (context cancelled, process group terminated, cell fails)
- One test assertion fails in private grading (cell marked `passed: false`, `verified_completion_s: null`)
- Re-running benchmark with an existing run ID without amending the ledger contract (launch guard rejects reservation)
- Launch count reaches `--max-launches` cap (guard halts further task launches)
- Interrupted cell execution retains artifact directory, events, and workspace for post-mortem inspection

## Anti-Patterns to Watch For

- [ ] Leaking API keys or credentials into `events.ndjson`, `stderr.log`, or `launch-receipt.json`
- [ ] Crediting failed tasks with fast completion time or averaging failed task time into speed metrics
- [ ] Comparing Tzro against an incomplete control condition that failed tasks (violating 9/9 gate)
- [ ] Leaking private grading test files into the subject directory before or during agent execution
- [ ] Permitting silent model substitutions (e.g. using a cheaper/faster model than declared in contract)
- [ ] Allowing synthetic/offline runs to claim causal product speed improvements
- [ ] Continuing execution after contract hash mismatch (input drift)
