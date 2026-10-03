# Use Case: Laya System 1 Decision Engine

**Actor**: The graph execution engine that needs fast, local, non-autoregressive yes/no, choice, or scoring decisions without cloud round-trips.
**Route**: Internal — `pkg/laya` consumed by `pkg/executor` via the `Decider` interface
**Backend**: `pkg/laya` — DaemonClient, StateAssembler, LayaDeciderAdapter
**Priority**: P0

---

## Intent

During graph execution, decision nodes need sub-50ms answers to structured questions — "Is this file relevant?", "Which candidate is best?", "Rate the confidence of this match." The Laya daemon runs ModernBERT-large locally via `ggmlc` and answers these questions in a single forward pass over compacted state, avoiding autoregressive token generation entirely. The executor should not know or care about the daemon's internals — it calls `Decide()` and gets back an answer with a confidence score.

## Preconditions

- Laya daemon binary (`ggmlc` or Python worker) is installed and accessible
- ModernBERT-large GGUF weights are available at the expected path
- Sufficient memory (~870 MB static footprint) is available
- Graph execution engine is configured with a `Decider` via `WithDecider`

## Success Criteria

- [ ] Daemon starts on first `Evaluate()` call and emits a `{"status":"ready"}` handshake within 30 seconds
- [ ] `noul` (yes/no) question returns an answer of `"yes"` or `"no"` with a confidence score
- [ ] `choice` question with 3+ options returns one of the provided options as the answer
- [ ] `score` question returns a numeric answer with a confidence score
- [ ] State compaction reduces input to ≤450 tokens before sending to the daemon
- [ ] Phase 1 compaction preserves essential state while eliding verbose log entries
- [ ] Phase 2 strict compaction activates only when Phase 1 exceeds the token budget
- [ ] Daemon auto-restarts if the process crashes mid-evaluation (self-healing)
- [ ] Broken pipe errors trigger restart and retry, not a permanent failure
- [ ] `Close()` terminates the daemon process cleanly
- [ ] Adapter correctly maps between `executor.DecisionInput` and `laya.DecisionRequest`

## Edge Cases to Probe

- State map exceeding 450 tokens even after strict compaction — should return an assertion error
- Daemon process exits unexpectedly between evaluations — should restart on next call
- Empty state map — should still produce a valid decision
- Very long prompt string (1000+ chars) — should be compacted before dispatch
- Context cancellation during daemon startup — should not leave zombie processes
- Concurrent `Evaluate()` calls — should serialize safely via mutex

## Anti-Patterns to Watch For

- [ ] Daemon hangs during startup and blocks indefinitely (no 30-second timeout)
- [ ] State compaction mutates the caller's original map (missing deep copy)
- [ ] Self-healing restart loop without backoff (daemon crash → restart → crash → ...)
- [ ] Zombie daemon processes left behind when parent exits without calling `Close()`
- [ ] Token estimation wildly inaccurate (off by >30%) causing compaction to miss the budget
- [ ] Adapter silently drops the `Scores` map from decision responses
