# Use Case: System 1 Decision Engine (Jev-Style-0.8B)

**Actor**: The graph execution engine needing fast, local, non-autoregressive yes/no, choice, or scoring decisions without cloud round-trips.
**Route**: Internal — `pkg/decision` consumed by `pkg/executor` via the `Decider` interface
**Backend**: `pkg/decision` — LocalDaemonProvider (`bin/jev-score`), RemoteHTTPProvider, StateSquasher, DeciderAdapter
**Priority**: P0

---

## Intent

During graph execution, decision nodes need sub-30ms answers to structured questions — "Is this file relevant?", "Which candidate is best?", "Rate the confidence of this match." The Jev-Style daemon runs Qwen3.5-0.8B locally via `libllama` (`bin/jev-score`) and answers these questions in a single forward pass over compacted state, avoiding autoregressive token generation entirely. The executor should not know or care about the daemon's internals — it calls `Decide()` and gets back an answer with calibrated confidence.

## Preconditions

- Decision daemon binary (`bin/jev-score` linking `libllama`) or remote HTTP provider is configured
- Qwen3.5-0.8B GGUF weights (`Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf`) are available at the expected path
- Sufficient memory (~800 MB static footprint) is available
- Graph execution engine is configured with a `Decider` via `WithDecider`

## Success Criteria

- [ ] Daemon starts on first `Evaluate()` call and emits a `{"status":"ready"}` handshake within 30 seconds
- [ ] `noul` (yes/no) question returns an answer of `"yes"` or `"no"` with a confidence score
- [ ] `choice` question with 2+ options returns one of the provided options as the answer with full probability map
- [ ] `score` question returns a calibrated score with confidence
- [ ] State compaction reduces input to ≤2,048 tokens before sending to the daemon
- [ ] Phase 1 compaction preserves essential state while capping diagnostics at 50 lines (head/tail)
- [ ] Phase 2 strict compaction activates only when Phase 1 exceeds the token budget
- [ ] Daemon auto-restarts if the process crashes mid-evaluation (self-healing)
- [ ] Broken pipe errors trigger restart and retry, not a permanent failure
- [ ] `Close()` terminates the daemon process cleanly without zombie processes
- [ ] Adapter correctly maps between `executor.DecisionInput` and `decision.DecisionRequest`
- [ ] Dual provider: supports remote HTTP provider (`POST /v1/systemone` or `/v1/decide`) with Bearer auth

## Edge Cases to Probe

- State map exceeding 2,048 tokens even after strict compaction — emergency compact truncates large strings
- Daemon process exits unexpectedly between evaluations — restarts on next call
- Empty state map — produces a valid decision
- Very long prompt string (1000+ chars) — compacted before dispatch
- Context cancellation during daemon startup — cleanly terminates child process
- Concurrent `Evaluate()` calls — serialize safely via mutex

## Anti-Patterns to Watch For

- [ ] Daemon hangs during startup and blocks indefinitely (no 30-second timeout)
- [ ] State compaction mutates the caller's original map (missing deep copy)
- [ ] Self-healing restart loop without backoff
- [ ] Zombie daemon processes left behind when parent exits without calling `Close()`
- [ ] Adapter silently drops the `Scores` map from decision responses
