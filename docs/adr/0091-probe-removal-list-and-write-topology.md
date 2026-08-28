# Probe Node Removal and list-and-write Topology

Status: accepted. Supersedes ADR-0089 (ReAct Loop in Probe Nodes).

The Probe Node's ReAct loop consistently produced subpar results across 4B, 8B, and 35B models (Agents A1, Ling-tiny, Qwen 3.6 35B A3B). Benchmark analysis of 0.6v5 showed all 4 exploration/documentation tasks scoring below 3.0 with pathologies: 0-step synthesize phases, 1-2K char thin summaries, downstream content repetition (22 duplicated paragraphs), fabrication, and 328K char bloat from 26K input. The Probe's claimed multi-hop navigation niche is already handled deterministically by the Repository Pre-Index (AST-parsed imports, dependency graphs) and the Symbol Extractor at the discovery level.

We delete the Probe Node entirely and introduce a `list-and-write` topology archetype in the Plan Template Registry. The List Node (ADR-0090) becomes the default discovery mechanism for all code/documentation tasks, with conditional Recall injection only when List output exceeds the downstream synthesis node's context budget.

## Considered Options

**Option 1: Improve Probe synthesis** — better prompting, larger step budgets, forced synthesis phases. Rejected because the failure is structural across model sizes. The ReAct loop entangles navigation, extraction, and synthesis in a single agent turn, violating the scaffolding-first principle that deterministic harness control outperforms model-driven control.

**Option 2: Deprecate Probe but keep as fallback.** Rejected because the Probe's infrastructure complexity (Phase Runner integration, Thought Chain lifecycle, Recall injection rules, Dependency-Gated injection logic) creates maintenance burden for a code path that should never be preferred.

**Option 3 (chosen): Delete Probe, replace with `list-and-write` topology.** The List Node handles extraction (GBNF line-ranges, verbatim), existing AST infrastructure handles discovery (imports, dependencies), and a dedicated synthesis pass handles document generation from grounded verbatim input.

## Design Decisions

- **`list-and-write` topology**: `List → [conditional Recall] → Sectioned Synthesis → Tool Sink`. Joins the template registry alongside `codegen`, `data-analysis`, `multi-probe-synthesis` (renamed to `multi-list-synthesis`), and `action-chain`.
- **Conditional Recall injection**: Recall is injected by the Kahn Compiler only when the List Node's output exceeds the downstream synthesis node's allocated context budget. When injected, Recall uses **deterministic structural compaction** — Symbol Extractor for code (exported types + signatures), heading extraction for markdown, schema extraction for config/YAML. No LLM compaction calls.
- **Research Node unchanged**: Web tasks keep their own pipeline (Search → Browse → Evidence Card → Sectioned Synthesis). The web discovery mechanism (search engines, URL ranking) is qualitatively different from file-based discovery.
- **Analyze Node unchanged**: Data/SQL tasks keep their pipeline (Schema Orient → Deterministic Query Path → Analytical Evidence).
- **Planner routing**: `IsExtractionGoal()` (ADR-0090) is broadened or replaced with a more general classification that routes all code/doc discovery tasks to `list` instead of the deleted `probe` type.

## Consequences

- `ProbeAnalyzeStrategy` for `type: "probe"` is deleted. The strategy registry loses the `probe` entry.
- `probe_analyze_strategy.go`, `probe_preload.go`, ADR-0089's ReAct loop infrastructure, and related Thought Chain lifecycle code become dead code and are removed.
- The Kahn Compiler's `Dependency-Gated Recall Injection` logic simplifies — no Probe-specific injection rules, only conditional budget-overflow injection for List nodes.
- Plan Template Registry's `probe-and-write` and `probe-synthesis` archetypes are replaced by `list-and-write` and `list-synthesis`.
- `CONTEXT.md` glossary terms referencing Probe Node in their definitions (Recall Node, Dependency-Gated Recall Injection, Verified Task Execution, Phase Runner, Deterministic Walker, etc.) require updates.
- The Phase Runner remains for Research and Analyze nodes — it is not removed, just decoupled from the deleted Probe lifecycle.
