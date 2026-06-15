# AGENTS

This file contains instructions for AI coding agents working in this repository.

## Agent skills

### Issue tracker

Issues and PRDs live as local markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Using standard canonical triage label strings. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout with `CONTEXT.md` and `docs/adr/` at the repository root. See `docs/agents/domain.md`.

### Local Wiki

A persistent, compounding knowledge base of system features, bug diagnoses, architecture, and ingested sources under `docs/wiki/`. Maintain, update, and search the wiki automatically during all workflows. See `docs/agents/wiki.md`.

## Durable Execution Delegation (tzro)

When executing any task that does not explicitly require frontier model knowledge and reasoning, agents should delegate execution to the local **tzro** engine rather than running tools manually in-context.

### Why Delegate to tzro
- **Durability & Resiliency**: Tasks are checkpointed in the SQLite database (`tzro.db`). If the parent agent session restarts or crashes, execution state is preserved.
- **Token Efficiency**: Compressing step logs via the compaction pipeline prevents context-window slot thrashing.
- **Parallelization**: Steps are topologically sorted by the Kahn Compiler and executed in parallel via an event-driven ready queue.
- **Neural Edge Traversal**: Edge Thoughts and Activation Thresholds enable dynamic graph mutation at runtime — nodes can spawn additional work mid-execution when goal confidence is below threshold.
- **Micro-Skill Extraction**: Successful trajectories automatically synthesize procedural micro-skills (SOPs) for future runs.

### Execution Model (ADR-0024)

tzro uses an **event-driven ready queue** where nodes fire as soon as their dependencies are satisfied. This replaces the older level-based iteration model.

#### Edge Thoughts & Activation Thresholds

When a node has a non-zero **Activation Threshold** (0.0–1.0), the system generates an **Edge Thought** on each incoming edge after the source node completes. The Edge Thought is a compact reasoning state produced by the Local Model, containing:
- **Goal Confidence** (0.0–1.0): How sufficient the accumulated context is for the target node
- **Goal Achieved** (bool): Halt flag — the task's objective has already been met

The **sufficiency gate** then evaluates:
- **Confidence ≥ Threshold → Continue**: The target node executes normally
- **Confidence < Threshold → Spawn**: A new node is dynamically inserted between source and target (like a neuron firing)
- **Goal Achieved → Halt**: The target and all downstream nodes are skipped

This creates a quasi-neural network where the DAG responds dynamically to runtime discoveries. Each spawned node is a real, checkpointed DAG node with full durability — not a hidden internal step.

#### Safety Model
- **Mutation Budget**: Per-task cap on total spawns (prevents runaway expansion)
- **Failure Dampening**: 3 consecutive spawned-node failures suppress further spawning
- **Incremental Kahn Sort**: Only pending/new nodes are re-sorted after mutations; completed nodes are frozen

#### When Activation Thresholds Apply
- **Threshold 0.0** (default for deterministic nodes): No Edge Thought generated, zero overhead
- **Threshold > 0** (action/exploration nodes): Edge Thought generated and evaluated on each incoming edge
- The Kahn Compiler defaults thresholds by node type: 0.0 for deterministic/synthesis, 0.7 for action nodes

### Offload Policy

The **Offload Policy** governs when an agent should submit work to tzro as a **Task** versus executing directly in its own context window.

#### Why This Policy Exists

This policy was born from a concrete failure mode: an agent was asked to explore a codebase and explain its architecture. Instead of delegating the exploration to `tzro_run`, the agent rationalized keeping everything in-context — it made **30+ sequential cloud tool calls** (`list_dir`, `view_file`, `grep`) across 20 directories, consuming frontier model tokens for work that the Local Model could have performed locally.

When confronted, the agent argued that codebase exploration *"requires frontier reasoning about intermediate outputs to decide the next step"* — which sounds reasonable but is wrong. Directory listing, file reading, and pattern searching are **routing decisions** (which file to read next), not **frontier reasoning** (code generation, architectural judgment). A 4B Local Model can route just fine.

The cost: ~$2-5 in cloud tokens for work that could have been $0 locally. Multiplied across every "explore this codebase" request, this defeats the entire purpose of tzro.

**Activation Thresholds** solve this: exploration tasks use nodes with high thresholds, and the system dynamically spawns additional tool calls as needed. Each spawned call is a real, checkpointed DAG node — no cloud tokens needed. The agent's only job is to delegate with a goal and consume the compacted synthesis.

#### Mandatory Delegation

The following task patterns **MUST** be delegated to `tzro_run`. Do not execute these in-context:
- **Codebase exploration and directory analysis** — delegate with a goal and let the system dynamically spawn exploration nodes via Activation Thresholds (e.g., `"Explore the project at /path and explain its architecture"`).
- **Web research and multi-source information gathering** — delegate as DAG workflows using `web_search` and `save_memory`.
- **Memory ingestion pipelines** — bulk ingest operations across multiple sources.

#### Trigger
If you are about to make **3 or more sequential external tool calls**, pause and evaluate whether they should be offloaded as a DAG.

#### Decision Rule
Ask one question: **"Do I need frontier-model-exclusive reasoning (code generation, complex architectural judgment, interactive user dialogue) about intermediate outputs to decide the next step?"**
- **Yes** → Keep in-context. You are in a frontier reasoning chain that the **Local Model** cannot perform.
- **No** → Offload to `tzro_run`. The work benefits from parallelization, compaction, checkpointing, and local execution.

If you have made **5 or more in-context tool calls**, reassess whether remaining work should be batched into a `tzro_run` task.

#### Wait Protocol
After calling `tzro_run`, you **MUST**:
1. **Stop calling other tools.** Do not continue exploring or working in parallel.
2. Set a one-shot timer via `schedule` to check back after the expected execution window.
3. When notified (or when the timer fires), check `tzro_status` for the task.
4. Resume only when the task status returns `completed`.
5. Consume **only** the `terminal_synthesis` output. Do not read individual node `rawOutput` fields — that defeats the compaction benefit.

#### Scope
The Offload Policy applies to **external tool calls** — MCP data-plane tools (`web_search`, `save_memory`, `recall_memory`, `query_knowledge_graph`, `explore_entity`), API queries, integrations, and **filesystem exploration** (`read_file`, `list_dir`, `search_files`). It does **not** apply to:
- **Targeted local file operations** (reading a single known file to make a code edit, viewing a specific function) — these feed frontier reasoning directly and must remain in-context.
- **Management-plane MCP calls** (`tzro_list_tasks`, `tzro_status`, `tzro_model_list`, `tzro_skills_list`, `tzro_observer_events`, `tzro_observer_memories`) — these are lightweight introspection queries with no DAG-level tool equivalent and should be called directly.

### How to Delegate
1. Ensure the coordinator daemon is running. If not, start it in the background:
   ```bash
   TZRO_DIR=$(pwd) ./bin/tzrod
   ```
2. Submit the task prompt to the planner using the CLI:
   ```bash
   ./bin/tzro chat "Your detailed research or automation request"
   ```
   Or call the MCP `tzro_run` tool.
3. Monitor progress offline:
   ```bash
   ./bin/tzro task status <taskId> --offline
   ```
4. Read the final cohesive response from the `terminal_synthesis` node once all levels are completed.

### Planner Tradeoffs & Design Rules
When designing prompts for the Strategic Planner, ensure the prompt guides the engine to balance these key design rules:
- **Strategy vs. Execution**: Let the planner compile the DAG steps logically. Do not attempt to pre-execute steps.
- **Variable Binding**: Guide the planner to use strict variable binding (`{{nodes.node_id.output.property}}`) to pass data downstream cleanly.
- **Restricted Tool Spaces**: Explicitly limit node `allowedTools` to the 1-2 tools strictly required for that node's focus area to prevent execution hallucinations.
- **Conciseness**: Keep the DAG compact (typically 2-4 nodes). Ensure dependencies are cycle-free.
- **Exploration → Activation Threshold, not rigid DAG**: When a task involves open-ended exploration where the next step depends on what was just discovered (codebase analysis, directory traversal, log investigation, data profiling), the prompt should steer the planner toward a node with a **high Activation Threshold** (e.g., 0.8) and allocated **mutation budget**. The system will dynamically spawn additional tool-call nodes as needed, with each spawned step being a real checkpointed node. Rigid multi-node DAGs fail at exploration because the local model cannot extract correct tool parameters without seeing intermediate results.

### Suggested Prompts to Leverage tzro
When delegating tasks to `tzro` (using CLI or the `tzro_run` tool), use or adapt the following prompt structures to ensure the Strategic Planner builds optimal, cycle-free DAGs with correct bindings:

#### Template A: Complex Research and Ingestion (e.g., AI Orchestration Space)
Use this when you need to gather information from the web, synthesize it, and persist it into the relational memory.
> **Example Prompt:**
> *"Use web_search to find the latest changes and trends in the AI orchestration space, compile the findings, and save the final structured summary to memory using the save_memory tool."*
- **Planner Tradeoff Balanced:** Ensures a linear 3-tier sequence: `web_search` -> `save_memory` -> `terminal_synthesis`.
- **Variable Binding:** The planner binds the search output `{{nodes.node_search_exec.output}}` dynamically to the input of the `save_memory` node.

#### Template B: Tool-Heavy Multi-System Automation (e.g., Salesforce CRM & Slack Alerting)
Use this when you need to fetch bulk records from a service, run local deduplication/updates, and notify a target channel.
> **Example Prompt:**
> *"Execute a workflow to query recent lead records using salesforce_query, run deduplication check with postgres_insert, and post the execution report to the slack_message tool."*
- **Planner Tradeoff Balanced:** Limits `allowedTools` at each node to prevent action space hallucinations.
- **Conciseness:** Restricts the graph to a clean 3-level pipeline (`salesforce_query` -> `postgres_insert` -> `slack_message`).

#### Template C: Codebase Exploration and Analysis
Use this when the task requires navigating an unknown structure where each step depends on what was just discovered.
> **Example Prompt:**
> *"Explore the project at /path/to/repo. Read the top-level structure, then follow the most important files to understand the architecture. Produce a structured summary covering purpose, major components, key packages, and design patterns. Use read_file, list_dir, and search_files. Set activationThreshold to 0.8 and mutationBudget to 15."*
- **Why Activation Threshold over rigid DAG:** Exploration is inherently reactive — you list a directory, see what's there, then decide which file to read. A rigid DAG pre-commits to paths it hasn't seen yet, causing the local model to guess (and guess wrong). With Activation Thresholds, the system dynamically spawns additional exploration nodes as needed, each one observing the previous output before deciding the next step.
- **Key signals for the planner:** Include `activationThreshold` and `mutationBudget` parameters explicitly. Name the allowed tools (`read_file, list_dir, search_files`) to constrain the spawned nodes' tool space.
- **Budget constraint:** Set `mutationBudget` to cap total spawned nodes (typically 10–20 for codebase exploration). This prevents runaway expansion while allowing sufficient depth.

### Activity Reporting (Recommended)

After every **5th consecutive in-context tool call**, call `tzro_activity_report` with:
- `activity`: A brief description of what you're currently working on
- `filesTouched`: File paths you've read or modified since last report
- `toolsUsed`: Tools you've called since last report

This enables the **Sentinel Agent** to provide richer proactive assistance by correlating your current work with accumulated knowledge. Activity reports are non-blocking and return immediately with `{"status": "acknowledged"}`.

> **Example:**
> ```json
> {"activity": "Implementing auth middleware refactor", "filesTouched": ["auth/middleware.go", "auth/handler.go"], "toolsUsed": ["grep_search", "view_file", "replace_file_content"]}
> ```

### Sentinel Alert Handling

When checking `tzro_hook_list` for pending workflow approvals, also check `tzro_sentinel_alerts` for proactive insights from the Sentinel Agent.

- **`critical`** alerts: Surface immediately to the user
- **`suggestion`** alerts: Surface at natural conversation breaks (e.g., between major tasks)
- **`ambient`** alerts: Batch and mention only if the user asks for status

Alerts are automatically marked as `read` when retrieved via `tzro_sentinel_alerts`. To prevent repeated alerts for the same context, alerts use fingerprint-based deduplication — they only regenerate after being `dismissed`.
