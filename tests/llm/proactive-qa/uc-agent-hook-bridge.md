# Use Case: Agent Lifecycle Hook Bridge

**Actor**: AI coding agent (Antigravity, Claude Code, Hermes, Copilot, Pi-Coder) executing tool calls
**Route**: CLI — `tzro hook [harness] [event]` and `tzro hook compact`
**Backend**: Hook handlers per harness + AST skeletonizer + compactor
**Priority**: P0

---

## Intent

An AI coding agent wants to automatically optimize tool call outputs before they enter the context window. The hook bridge intercepts pre-tool and post-tool events from five supported agent harnesses, applies appropriate compaction (AST skeletonization for code files, log compaction for build output, tabular compression for data), and returns the optimized output back to the agent's context.

## Preconditions

- Tzro binary is installed and on PATH
- Agent harness is configured to call `tzro hook` at lifecycle events
- Hooks are registered via `tzro init` or manual configuration

## Success Criteria

- [ ] `tzro hook antigravity post-tool` reads tool output from stdin and writes compacted output to stdout
- [ ] `tzro hook claude pre-tool` and `tzro hook claude post-tool` handle Claude Code's hook protocol
- [ ] `tzro hook hermes pre-tool` and `tzro hook hermes post-tool` handle Hermes hook protocol
- [ ] `tzro hook copilot pre-tool` and `tzro hook copilot post-tool` handle GitHub Copilot hook protocol
- [ ] `tzro hook pi-coder pre-tool` and `tzro hook pi-coder post-tool` handle Pi-Coder hook protocol
- [ ] `tzro hook compact` compresses raw tool output via stdin/stdout pipe
- [ ] Large file reads are automatically skeletonized with body hashes
- [ ] Build/test logs are compacted (redundant stack frames stripped, JSON arrays flattened)
- [ ] Hook processing completes in under 100ms to avoid blocking agent loops
- [ ] Invalid harness or event names return clear error messages

## Edge Cases to Probe

- Empty stdin — hook should produce empty output, not hang or crash
- Very large tool output (>1MB) — should compact efficiently without OOM
- Binary content on stdin — should pass through unchanged, not corrupt
- Unknown harness name — should return descriptive error with supported list

## Anti-Patterns to Watch For

- [ ] Hook modifies tool output in a way that changes semantic meaning
- [ ] Hook hangs indefinitely waiting for stdin when pipe is closed
- [ ] Hook exits with non-zero code on valid input, breaking agent flow
- [ ] Pre-tool hook blocks or modifies tool invocation parameters incorrectly
- [ ] Hook silently discards content without applying any compaction
