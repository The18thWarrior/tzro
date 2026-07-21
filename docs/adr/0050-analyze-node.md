# ADR-0050: Analyze Node

## Status

Accepted

## Context

ADR-0049 introduced the **Data Profiler** and **Cache Bridge Node** to handle tabular data files efficiently. The `read_file` tool now detects tabular formats, profiles them, caches the parsed data on disk, and returns a structured envelope with a `cacheId`. Downstream nodes can use `introspect_cache`, `read_cached_data`, and `jq_cached_data` to query the cached data without re-reading the file.

However, a critical gap remained between the **Strategic Planner** and the cache system:

1. **Tool Hallucination**: When a task requires data aggregation (counting, grouping, filtering, ranking), the planner scans its **Tool Inventory** for an appropriate tool. Finding nothing for aggregation, the 4B **Local Model** reaches into its training data and emits hallucinated tool names (`postgres_insert`, `sql_query`, `db_query`). This is rational behavior — the operation the planner needs genuinely doesn't exist in the inventory it was given.

2. **Invisible Capability**: The cache tools (`introspect_cache`, `jq_cached_data`, `read_cached_data`) exist in the executor layer but are invisible to the planner. They are injected by the **Kahn Compiler** during SCT expansion, not planned by the **Strategic Planner**. The planner has no abstract concept for "analyze data" — only concrete tool names.

3. **Repair Probe Failure**: The validation pipeline (ADR-0019) correctly catches hallucinated tools and replaces them with **Probe Nodes**. However, the repair probe receives filesystem exploration tools (`read_file`, `list_dir`, `search_files`) and a codebase exploration system prompt — not data analysis tools or guidance. Even when the SCT compiler auto-expands the probe's `AllowedTools` to include cache tools, the probe's **Thought Chain** system prompt says "list_dir for structure, search_files for patterns, read_file for content" — no mention of cache tools or JQ patterns.

4. **Docgen Succeeds, Datanal Fails**: Documentation generation tasks succeed because their operations map 1:1 to the planner's tool inventory (`read_file`, `list_dir`, `search_files`). Data analysis tasks fail because the aggregation operation has no tool-level representation.

### Considered Options

- **Option A: Add cache tools to the planner's Tool Inventory.** Rejected — exposes implementation details (`cacheId`, JQ filters, `introspect_cache`) to the planner. The planner operates at a strategic abstraction level and should not know about the caching mechanism. This also creates a coupling between the planner prompt and the cache implementation.

- **Option B: Inject CacheExplorationGuide into repair probes.** Rejected by the user as prompt engineering — even "structurally valid" prompt engineering is fragile to model changes and context length pressure.

- **Option C: New abstract node type `"analyze"`.** Selected — follows the same architectural pattern as `"probe"` (abstract intent, compiler-expanded execution). The planner emits `type: "analyze"` when the task requires data analysis. The **Kahn Compiler** deterministically provisions cache tools and `ProbeConfig`. The executor runs a data-analysis-specific **Thought Chain**. The planner never sees cache internals.

## Decision

### 1. New Node Type: `analyze`

The `analyze` node type is added to the `GraphNode.Type` enum alongside `action`, `probe`, `synthesis`, etc. The planner emits it when the task involves aggregation, filtering, counting, grouping, ranking, or summarizing data from an upstream source.

The planner sets only:
- `type: "analyze"`
- `action: ""` (empty — no specific tool)
- `instructions`: Natural language analysis goal (e.g., "Count leads by country, return top 5")

The planner does NOT set `allowedTools` or `probeConfig` — these are auto-provisioned by the compiler.

### 2. Compiler Expansion (SCT)

The **Kahn Compiler** handles `analyze` nodes alongside `probe` nodes in `ExpandToSCTGraph`:

- Auto-creates a `ProbeConfig` with:
  - `AllowedTools: ["introspect_cache", "sql_cached_data"]` [Updated by ADR-0051: `jq_cached_data` and `read_cached_data` replaced with `sql_cached_data`]
  - `StepBudget: 15` (slightly less than probe's 20 — data analysis needs fewer steps)
  - `CompactEvery: 3`
  - `CompactionLevel: "preserve"`
- Injects a downstream **Recall Node** (same pattern as probe → recall from ADR-0038)
- Does NOT create a `semantic_validator → deterministic` pair (analyze is a Thought Chain, not a single-shot action)

### 3. Executor: Data Analysis Thought Chain

The executor routes `analyze` nodes through the same `RunProbe` Thought Chain loop as `probe` nodes, but with a different system prompt selected via `isAnalyzeConfig()`:

- **Detection**: If the `ProbeConfig.AllowedTools` contain any cache tool (`introspect_cache`, `sql_cached_data`), the config is classified as an analyze config. [Updated by ADR-0048]
- **System Prompt**: `buildAnalyzeSystemPrompt` replaces `buildProbeSystemPrompt`. It teaches the model:
  - Data analysis strategy (check context for cacheId → introspect → SQL query → synthesize) [Updated by ADR-0051: SQL replaces JQ]
  - Standard SQL patterns (GROUP BY, ORDER BY, COALESCE, WHERE, COUNT, LIMIT)
  - Graceful degradation (if no cacheId, synthesize from raw text)
- **No write_file access**: Analyze nodes are read-only data consumers. Output saving is handled by downstream action nodes.

### 4. Graceful Degradation

When upstream data is non-tabular (no `cacheId` in accumulated context), the analyze node's Thought Chain degrades to synthesis over raw text — effectively behaving as a synthesis node. This ensures the `analyze` type works for any data format without special-casing.

### 5. Validation Exemption

`analyze` nodes are exempt from `findInvalidTools` validation (same as `probe`, `synthesis`, `deterministic`). They don't reference external tools in their `Action` field.

### 6. Repair Path Upgrade

When `repairGraphWithProbe` replaces nodes with hallucinated tools, it now detects data analysis context via `isDataAnalysisRepair()`:
- Checks hallucinated tool names (e.g., `postgres_insert`, `sql_query`)
- Checks instruction keywords (count, group, aggregate, filter, etc.)

If detected, it emits a `type: "analyze"` repair node instead of a `type: "probe"` repair node.

## Consequences

### Positive

- **Eliminates tool hallucination for datanal tasks**: The planner has a clean abstraction — "when you need to analyze data, emit `analyze`" — without knowing about cache internals.
- **Follows established architectural patterns**: Same expansion model as `probe` → `recall` (ADR-0038), same SCT compilation flow, same Thought Chain execution.
- **Completes the ADR-0049 pipeline**: Data Profiler (ADR-0049) handles ingestion; Analyze Node (this ADR) handles consumption.
- **Graceful degradation**: Works for any data type — tabular data gets cache tools, non-tabular data gets synthesis.
- **No prompt engineering**: The system prompt is structural (defined by node type), not injected conditionally.

### Negative

- **New node type adds complexity**: The executor, compiler, and validation layers all need to handle one more type. Mitigated by reusing the probe infrastructure.
- **Planner prompt surface area grows**: Adding `analyze` guidance increases the system prompt length. Mitigated by keeping the guidance concise (3 sentences).
- **~~JQ pattern brittleness~~**: Resolved by ADR-0051 — SQL replaced JQ as the query language, eliminating jq syntax failures entirely.

## References

- ADR-0019: Probe Node and Thought Chain
- ADR-0038: Recall Node for Discovery-Synthesis Alignment
- ADR-0049: Data Profiler and Cache Bridge Node
