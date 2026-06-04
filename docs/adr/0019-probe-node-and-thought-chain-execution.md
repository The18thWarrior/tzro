# ADR-0019: Probe Node and Thought Chain Execution

A new DAG node type — the **Probe Node** — that runs bounded, goal-directed exploration internally using the **Local Model** and a constrained tool set. From the parent DAG's perspective, it is a single opaque node. Internally, it executes a **Thought Chain**: a sequence of stateless bridge→exec steps with embedding-based thought retrieval and rolling compaction, persisted to SQLite for durability.

## Status

Accepted

## Context

Adding filesystem tools (`read_file`, `list_dir`, `search_files`) to tzro exposes a fundamental limitation: codebase exploration is dynamically shaped. The planner cannot predict directory structure, file count, or file sizes across arbitrary languages, frameworks, and user directories. The static DAG model requires the graph shape to be known at planning time.

Three alternative approaches were evaluated and rejected:

## Considered Options

- **Option A: Scatter Nodes (runtime DAG expansion)**: A new node type that dynamically spawns child nodes based on runtime output (e.g., one `read_file` per directory entry). Rejected — introduces runtime graph mutation to the executor, significantly increasing complexity, and still cannot handle iterative deepening (chunked file reads) without further extensions.

- **Option B: Smart planning without execution changes**: The cloud planner constructs exploration DAGs by predicting project structure. Rejected — assumes knowledge of arbitrary file structures across languages and frameworks. Untenable for non-technical users or unfamiliar project layouts.

- **Option C: ReAct agent loop (separate execution mode)**: A full agent loop where the Local Model iteratively calls tools and accumulates context. Rejected on two grounds: (1) identity drift — tzro is a DAG execution engine, not a general agent framework, and adding a second execution mode undermines that identity; (2) selection bias — agents will default to the flexible ReAct loop for everything, causing the DAG mode to atrophy.

## Decision

Introduce the **Probe Node** as a new DAG node type and the **Thought Chain** as its internal execution pattern.

### Probe Node

- Accepts a natural language `goal` and a set of `allowedTools`
- Executes up to a **step budget** of 20 steps (configurable)
- Can terminate early when the Local Model signals convergence with ≥ 0.9 confidence
- Produces a **goal-directed terminal synthesis** as its output for downstream DAG consumption
- From the parent DAG: one node, one input, one output — no cycles, no graph mutation

### Thought Chain (Internal Execution)

Each step is a stateless Local Model inference call. The model sees:

1. **Goal** — the probe's objective
2. **Semantically retrieved prior thoughts** — top-K relevant thoughts from the chain, retrieved via ONNX embedding cosine similarity over persisted thought entries
3. **Current thought** — the task for this step
4. **Previous tool output** — bounded raw result from the last tool execution

The model produces: a tool call (tool selection + parameters) and a next thought description.

**Rolling compaction**: Every 3 steps, a compaction step merges the last 3 thoughts into the rolling text summary. This summary serves as a fallback and feeds the terminal synthesis.

**Durability**: Each thought is committed to SQLite immediately. If the process crashes at step 12, it resumes from the last committed thought + rolling summary.

**Terminal synthesis**: At budget exhaustion or convergence, a final Local Model call takes the rolling summary + original goal and produces a structured output for the parent DAG.

### Filesystem Tools

Three built-in tools registered in `standalone_tools.go`, sharing a common path validation layer:

- `read_file` — reads file content with `startLine`/`endLine` parameters, capped at 200 lines per call, bypasses the Compaction Pipeline (source code is injected raw)
- `list_dir` — lists directory contents with metadata
- `search_files` — pattern search across files (grep-like) returning file paths, line numbers, and matching context

**Path validation**: All three tools validate paths against an allowlist. Default: project root (resolved from `TZRO_DIR`). Configurable via a new `allowedPaths` array in `.tzro/mcp_config.json`.

## Consequences

- **DAG identity preserved**: The Probe Node is a node type, not a new execution mode. The parent DAG stays static, acyclic, and checkpointable.
- **Fixed context pressure**: The Local Model's context window per step is bounded regardless of exploration depth — goal + retrieved thoughts + current thought + tool output.
- **Compaction overhead**: A 20-step probe executes ~26 Local Model calls (20 bridge + 6 compaction). All local, no cloud cost.
- **New executor capability**: The executor must support running a Thought Chain loop inside a Probe Node, including thought persistence, embedding indexing, rolling compaction, and convergence detection.
- **Micro-Skill extraction**: Successful Probe trajectories can be synthesized into Procedural Micro-Skills, capturing effective exploration patterns for future reuse.
