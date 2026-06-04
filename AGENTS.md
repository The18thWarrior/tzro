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
- **Parallelization**: Layered steps are topologically sorted by the Kahn Compiler and executed in parallel.
- **Micro-Skill Extraction**: Successful trajectories automatically synthesize procedural micro-skills (SOPs) for future runs.

### Offload Policy

The **Offload Policy** governs when an agent should submit work to tzro as a **Task** versus executing directly in its own context window.

#### Mandatory Delegation

The following task patterns **MUST** be delegated to `tzro_run`. Do not execute these in-context:
- **Codebase exploration and directory analysis** — these are handled by **Probe Nodes** with **Thought Chain** execution internally. Delegate with a goal (e.g., `"Explore the project at /path and explain its architecture"`).
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

