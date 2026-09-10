# Use Case: Log and Output Compaction with Evidence Guarantees

**Actor**: AI coding agent or developer running verbose build, test, or execution commands
**Route**: CLI — `tzro compact [--run "<command>"]` or piped via `stdin`
**Backend**: Evidence contract compactor (`pkg/compactor/evidence.go`) and SQLite artifact store
**Priority**: P0

---

## Intent

An agent or developer needs to run a test suite, build command, or process verbose log streams without drowning in thousands of tokens of repetitive output or losing root-cause diagnostics. Using `tzro compact --run "<cmd>"` or piping stdout via `tzro compact`, the engine executes commands directly or processes logs, captures verified exit codes, caps inline diagnostics to at most 10 lines of root-cause signal, and saves full failure payloads into the local store with an expansion hash for deep inspection.

## Preconditions

- Tzro binary is installed and available on PATH
- Project directory contains testable code or commands producing stdout/stderr
- Local SQLite database initialized for storing artifact expansions

## Success Criteria

- [ ] Running `tzro compact --run "go test ./..."` captures process exit code with verified `observed` confidence
- [ ] Passing test runs emit a concise one-line pass summary with duration and zero token waste
- [ ] Failing test runs display an inline diagnostic capped at at most 10 high-signal lines highlighting the root failure
- [ ] Complete raw failure logs exceeding the inline cap are saved to the artifact store
- [ ] An expansion pointer (`#art_<id>` or hash) is surfaced in the output for subsequent retrieval via `tzro expand`
- [ ] Piped input (`cat test.log | tzro compact`) collapses repeated runtime frames and goroutine stacks
- [ ] JSON arrays with uniform structures are automatically converted into compact Markdown tables
- [ ] Essential signal (assertion errors, failing test identifiers, exit codes) is strictly preserved
- [ ] Output is clean, agent-readable markdown without ANSI escape code noise
- [ ] Exit code of the underlying command is preserved when executed via `--run`

## Edge Cases to Probe

- Command exits with non-zero exit code but produces empty stdout and stderr
- Log stream containing mixed unstructured text, multi-line stack traces, and nested JSON
- Extremely massive test logs (>50 MB) piped into stdin without buffer overflow or memory spikes
- Command that times out or is canceled mid-execution
- Single-line output passed through without unnecessary truncation or overhead

## Anti-Patterns to Watch For

- [ ] Inline failure output exceeds 10 lines, flooding the agent context window
- [ ] Passing test suites emit dozens of lines of redundant package status messages
- [ ] Root cause assertion message is stripped or elided in favor of generic framework frames
- [ ] Exit code confidence is marked `unverified` when the process was directly executed via `--run`
- [ ] Artifact expansion hash is missing from failure reports, preventing full log retrieval
- [ ] Compactor hangs indefinitely waiting for EOF on interactive commands
