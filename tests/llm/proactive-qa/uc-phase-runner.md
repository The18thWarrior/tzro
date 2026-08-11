# Use Case: Structured Multi-Phase Node Execution

**Actor**: Developer running tasks through the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer runs a complex task where individual nodes need to execute multiple structured phases in sequence — for example, a data analysis node that must first discover available datasets, then query them, then synthesize findings. The Phase Runner manages these phases as a first-class execution primitive, with per-phase step budgets, allowed tool sets, recovery strategies (skip, backtrack, fail), and transition logic. Each phase produces a structured result that flows into the next phase's context.

## Preconditions

- The `tzro` daemon is running with at least one inference sidecar active.
- The task contains nodes configured with multi-phase execution (e.g., analyze nodes with discover/query/synthesize phases).

## Success Criteria

- [ ] Each phase runs within its own step budget, independent of other phases.
- [ ] Phase results carry structured data (summary, artifacts, tools called, steps used, backtracks) to the next phase.
- [ ] Allowed tools are scoped per-phase — a phase cannot call tools outside its whitelist.
- [ ] Minimum tool call requirements prevent premature synthesis (e.g., discover phase must read at least 1 file before allowing synthesis).
- [ ] Phase transitions are driven by explicit transition functions, not implicit step counting.
- [ ] When a phase's step budget is exhausted, the configured exhaustion strategy fires: skip (move to next), backtrack (return to a prior phase), or fail (abort the node).
- [ ] Backtrack re-entry is capped by maxRetries to prevent infinite loops between phases.
- [ ] Error strategies handle per-step failures: retry the step, transition to a different phase, or abort.
- [ ] Phase context keys enable phase-aware mock routing in tests.
- [ ] Tool dispatchers can be injected via context for testing without touching production tool infrastructure.
- [ ] The Phase Runner enforces a global minimum tool call gate: at least 1 tool must be called across all phases before synthesis is allowed.

## Edge Cases to Probe

- Phase 1 exhausts its step budget with 0 tool calls — verify exhaustion strategy fires (skip or backtrack) instead of silently proceeding.
- Backtrack from phase 3 to phase 1, which then backtracks again — verify maxRetries cap prevents infinite loops.
- Phase transition function returns an unknown phase name — verify clean error without crashing the node.
- All phases complete with 0 total tool calls — verify the global tool call gate prevents empty synthesis.
- Phase result artifacts exceed compaction budget — verify downstream phases receive compacted summaries.

## Anti-Patterns to Watch For

- [ ] Phases share a single global step budget instead of having independent per-phase budgets.
- [ ] Backtrack loops infinitely between two phases without hitting any retry cap.
- [ ] Phase transitions bypass the Phase Runner and directly manipulate node state.
- [ ] Tool dispatch errors in one phase silently propagate to the next phase without logging.
- [ ] The Phase Runner calls the cloud model for tool-selection decisions that should be local.
- [ ] Phase results are dropped between phases, forcing re-computation.
