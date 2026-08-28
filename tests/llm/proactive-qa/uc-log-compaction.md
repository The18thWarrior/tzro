# Use Case: Log and Output Compaction

**Actor**: AI coding agent processing verbose build, test, or stack trace output
**Route**: CLI — `command | tzro compact`
**Backend**: Compactor engine (stack frame elision, JSON array flattening)
**Priority**: P1

---

## Intent

An agent has just run a test suite or build command that produced verbose output (Go test logs, npm test results, stack traces, large JSON arrays). The agent pipes this output through `tzro compact` to strip redundant runtime stack frames, flatten uniform JSON arrays into compact markdown tables, and reduce the output to its essential signal — achieving ~80% token reduction on typical build/test logs.

## Preconditions

- Tzro binary is installed and on PATH
- Verbose output is available via stdin pipe

## Success Criteria

- [ ] `go test ./... | tzro compact` reduces Go test output by stripping redundant goroutine stacks
- [ ] JSON arrays with uniform structure are flattened into markdown tables
- [ ] Repeated stack frames (runtime.goexit, testing.tRunner) are collapsed
- [ ] Essential information is preserved: test names, pass/fail status, error messages
- [ ] Output is valid markdown suitable for agent consumption
- [ ] Compact handles mixed content (text + JSON + stack traces) in a single stream
- [ ] Empty stdin produces empty output without hanging

## Edge Cases to Probe

- Very short input (single line) — should pass through unchanged
- Input that is already compact — should not corrupt or expand it
- Binary content in the stream — should handle gracefully
- Extremely large input (>10MB of logs) — should stream efficiently

## Anti-Patterns to Watch For

- [ ] Compaction removes error messages or test failure details
- [ ] Compaction hangs waiting for more input after EOF
- [ ] JSON table flattening loses column data or truncates values
- [ ] Stack trace elision removes user code frames (only runtime frames should be stripped)
