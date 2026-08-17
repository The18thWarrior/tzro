# Handoff: Model/Scaffolding Split — Research Pipeline & Deterministic Walker

## Context

Session analyzed benchmark runs 20–30 and discovered that the 4B local model's step-level routing decisions (which tool to call, what parameters to use) succeed **~10% of the time**. The entire PhaseRunner system is carried by deterministic scaffolding: ExplorationQueue, ToolFixup, forced tool calls, cloud re-synthesis. The model is genuinely good at **code generation from specs** (quality 5.0) but cannot plan, route, or generate tool parameters.

### Key Findings (Established as Ground Truth)

1. **The model never emits valid `web_search` queries.** Across runs 20–30 (11 benchmark runs), the local model produced 0 valid search queries. Every successful search was rescued by scaffolding.
2. **`read_file` only works because of ExplorationQueue.** The model emits empty paths; ToolFixup redirects to the next file from a pre-computed queue.
3. **Edge thoughts are net-negative.** The compactor LLM inflates content in 50% of tasks instead of compressing it.
4. **Codegen works because it's a completion task**, not a planning task. The spec constrains the output space; the model fills in code. No routing decisions needed.
5. **Premature-synthesis retries are 100% wasted.** The model never changes its mind after being told to retry — it always ends at forced-call.

### What Was Changed (This Session)

Three edits (uncommitted, see walkthrough for details):

1. **Eliminated premature-synthesis retry loop** in `internal/executor/phase_runner.go` — saves ~120 LLM calls/run
2. **Added `web_search` ToolFixup** to `buildWebProbePhaseRunner` in `internal/executor/probe_phases.go` — seeds queries from goal text, tracks discovered URLs
3. **Disabled LLM reasoning compression** in `internal/executor/probe_compaction.go` — deterministic-only, prevents inflation

All tests pass. Build is clean.

---

## Remaining Work: Two Tracks

### Track 1: Research Query Diversification & URL Loop

**Problem**: `extractSearchQueryFromGoal()` in `internal/executor/probe_tools.go:73` is stateless — it always produces the same query from the goal. Every forced `web_search` call in a task gets the identical query string. No diversification, no refinement based on results.

**What the old Probe system (runs 20–23) did better**: It had a URL extraction loop that accumulated URLs from search results and used them for `web_browse` redirection. The URL tracking was wired back in via `ToolPostProcess` in this session's changes, but query diversification was not.

**What needs to happen**:
- Generate 2–3 diverse search queries per goal **upfront** (deterministically or via a single cheap LLM call)
- Rotate through them on successive forced `web_search` calls instead of repeating the same one
- Consider extracting query variants from the goal (e.g., split compound goals, extract entity names)
- Look at how the old `Executor/RQ` system in runs 22–23 extracted URLs — reference log patterns: `[Probe] Extracted 5 URLs from web_search results at step 1`

**Key files**:
- `internal/executor/probe_tools.go` — `extractSearchQueryFromGoal()`
- `internal/executor/probe_phases.go` — `buildWebProbePhaseRunner()` (the ToolFixup we just added)
- `internal/executor/research_phases.go` — reference implementation with URL tracking

### Track 2: Full Deterministic Walker (Model/Scaffolding Split)

**Problem**: Pass 1 + Pass 2 still fire on every step even though the model's output is ignored ~90% of the time. Each step burns 2 LLM calls (worker reasoning + GBNF extraction) before the forced-call path takes over.

**Architecture to move toward**:

| Component | Model's Role | Scaffolding's Role |
|---|---|---|
| **Codegen** | Generate code from spec (keep as-is) | Provide spec, context, compilation gate |
| **File exploration** | None needed | ExplorationQueue walks files deterministically |
| **Web research** | None needed | Goal-derived queries, URL extraction loop |
| **Data analysis** | None needed | QueryIntent extraction, cache introspection |
| **Synthesis** | Summarize accumulated tool outputs (keep) | Provide compacted context |
| **Verification** | None (cloud VTE handles this) | VTE pre-check, cloud verification |

**What a deterministic walker would look like**:
- Replace the `for step` loop with a simple queue consumer
- For file probes: iterate ExplorationQueue → `read_file` each entry → done
- For web probes: execute N goal-derived queries → `web_search` each → extract URLs → `web_browse` top K → done
- For data probes: `introspect_cache` → `QueryIntent` extraction → `query_builder` → done
- Remove Pass 1 / Pass 2 entirely for these phases
- Keep model invocation only for synthesis and codegen

**Key files**:
- `internal/executor/phase_runner.go` — main loop (lines 270–370)
- `internal/executor/two_pass.go` — `extractToolAction()` (could be eliminated)
- `internal/executor/analyze_phases.go` — already heavily scaffolded, closest to deterministic

### Also Outstanding: VTE Re-Synthesis Regression

Run 30 stopped using `cloud re-synthesis` and switched to `re-explore` for research tasks. This is a separate code change that should be investigated. The VTE rejection handling logic decides between these two paths. Search for `Re-explore` and `REJECTED` in the VTE implementation files.

---

## Suggested Skills

- **`brainstorming`** — for designing the deterministic walker architecture
- **`plan`** — for the implementation plan of Track 2
- **`tdd`** — for test-driving the deterministic walker
- **`improve-codebase-architecture`** — for identifying which PhaseRunner components to keep vs eliminate
- **`analyze-benchmark-run`** — for evaluating run 31 results after changes

## Benchmark Data Locations

- Run 30: `.scratch/benchmark/results-full-30/`
- Run 29: `.scratch/benchmark/results-full-29/`
- Earlier runs (20–28): `.scratch/benchmark/results-full-{N}/`

## Conversation Artifacts (This Session)

- Benchmark analysis: `<appDataDir>/brain/1642bc8f-f96a-43df-9fe1-0a2d4d3a9641/benchmark_analysis_full30.md`
- Execution flow comparison: `<appDataDir>/brain/1642bc8f-f96a-43df-9fe1-0a2d4d3a9641/execution_flow_comparison.md`
- Implementation plan: `<appDataDir>/brain/1642bc8f-f96a-43df-9fe1-0a2d4d3a9641/implementation_plan.md`
- Walkthrough: `<appDataDir>/brain/1642bc8f-f96a-43df-9fe1-0a2d4d3a9641/walkthrough.md`
