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

### Execution Modes

tzro executes tasks using **DAG mode** (Directed Acyclic Graph). Workflows are planned upfront, compiled via Kahn sorting, and executed in parallel where possible. For open-ended exploration, it relies on **Probe Nodes** which run bounded reasoning chains dynamically.

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

**Probe Nodes** were built specifically to prevent this. They run bounded reasoning chains on the Local Model — zero cloud tokens. The agent's only job is to delegate with a goal and consume the compacted synthesis.

#### Mandatory Delegation

The following task patterns **MUST** be delegated to `tzro_run`. Do not execute these in-context:
- **Codebase exploration and directory analysis** — use a Probe Node by delegating with a goal like: `tzro_run({prompt: "Explore the project at /path and explain its architecture"})`.
- **Web research and multi-source information gathering** — delegate structured search→save pipelines.
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
   # If installed globally:
   tzrod
   # If running from local repo:
   TZRO_DIR=$(pwd) ./bin/tzrod
   ```
2. Submit the task prompt using the MCP `tzro_run` tool with the appropriate mode:
   ```json
   ```json
   // Structured workflows or reactive exploration
   {"prompt": "Explore the codebase at /path and explain its architecture"}
   ```
   Or use the CLI: `tzro chat "Your request"`.
3. Monitor progress offline:
   ```bash
   tzro task status <taskId> --offline
   ```
4. Read the final cohesive response from the `terminal_synthesis` node once completed.

### Planner Tradeoffs & Design Rules
When designing prompts for the Strategic Planner (DAG mode), ensure the prompt guides the engine to balance these key design rules:
- **Strategy vs. Execution**: Let the planner compile the DAG steps logically. Do not attempt to pre-execute steps.
- **Variable Binding**: Guide the planner to use strict variable binding (`{{nodes.node_id.output.property}}`) to pass data downstream cleanly.
- **Restricted Tool Spaces**: Explicitly limit node `allowedTools` to the 1-2 tools strictly required for that node's focus area to prevent execution hallucinations.
- **Conciseness**: Keep the DAG compact (typically 2-4 nodes). Ensure dependencies are cycle-free.
- **Exploration → Use Probe Nodes**: When a task involves open-ended exploration where the next step depends on what was just discovered (codebase analysis, directory traversal, log investigation, data profiling), prompt the planner to use a **Probe Node**. It handles reactive step-at-a-time reasoning natively.

### Suggested Prompts to Leverage tzro
When delegating tasks to `tzro` (using CLI or the `tzro_run` tool), use or adapt the following prompt structures:

#### Template A: Structured Research Pipeline (DAG mode)
Use when the workflow shape is known: search → process → save.
> **Example:**
> ```json
> {"prompt": "Use web_search to find the latest trends in the AI orchestration space, compile the findings, and save the final structured summary to memory using save_memory.", "mode": "dag"}
> ```
- **Why DAG:** The 3-step pipeline (search → save → synthesize) is fully known upfront. Parallelization possible.

#### Template B: Multi-System Automation (DAG mode)
Use when fetching from multiple services and piping results through a known pipeline.
> **Example:**
> ```json
> {"prompt": "Query recent lead records using salesforce_query, run deduplication check with postgres_insert, and post the execution report to slack_message.", "mode": "dag"}
> ```
- **Why DAG:** Fixed tool sequence with no reactive decisions. Each node's `allowedTools` is constrained.

#### Template C: Codebase Exploration (Probe Node) ⭐
Use when the task requires navigating an unknown structure where each step depends on what was just discovered.
> **Example:**
> ```json
> {"prompt": "Explore the project at /path/to/repo. Read the top-level structure, then follow the most important files to understand the architecture. Produce a structured summary covering purpose, major components, key packages, and design patterns."}
> ```
- **Why Probe Nodes:** Exploration is inherently reactive — you list a directory, see what's there, then decide which file to read next. Probe Nodes handle this natively.

#### Template D: Open-Ended Web Research (Probe Node) ⭐
Use when research is open-ended and the next query depends on what was just discovered.
> **Example:**
> ```json
> {"prompt": "Research the current state of local-first AI orchestration. Find key players, compare approaches, and identify emerging patterns. Save all findings to memory."}
> ```
- **Why Probe Nodes:** The model reads search results, identifies gaps, formulates follow-up queries, and decides when enough information has been gathered — all reactively.

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















<!-- BEGIN TZRO INSTRUCTIONS -->

# tzro — Durable Local-First Execution Engine & Delegation Instructions

`tzro` compiles natural language prompts into topologically-sorted DAG (Directed Acyclic Graph) workflows, executes them durably with checkpointing, and provides relational memory, knowledge graph, and micro-skill extraction. This file contains instructions for AI coding agents to interact with `tzro` via the Model Context Protocol (MCP) server or the CLI.

---

## ⚖️ Durable Execution Delegation (Offload Policy)

When executing any task that does not explicitly require frontier model knowledge or complex reasoning, coding agents **must** delegate execution to the local `tzro` engine rather than running tools manually in-context.

### Why Delegation Matters — A Cautionary Example
This policy exists because of a concrete failure: an agent asked to "explore the codebase and explain its architecture" kept everything in its own context window. It made **30+ sequential cloud tool calls** (`list_dir`, `view_file`, `grep`), consuming frontier model tokens for work the Local Model handles natively. Directory listing, file reading, and pattern searching are **routing decisions** (which file to read next), not **frontier reasoning** (code generation, architectural judgment). A 4B Local Model routes just fine.

**Probe Nodes** (ADR-0019) were built specifically to prevent this. They run a bounded Thought Chain on the Local Model with `read_file`, `list_dir`, and `search_files` — zero cloud tokens. Your only job: delegate with a goal, consume the compacted synthesis.

---

### The Offload Decision Rule

Ask one question:
> **"Do I need frontier-model-exclusive reasoning (code generation, complex architectural judgment, interactive user dialogue) about intermediate outputs to decide the next step?"**

*   **Yes** → Keep execution **in-context** (run cloud/local tool calls directly).
*   **No** → **Offload** to `tzro_run` as a background task.

### Trigger Policies
*   **Evaluation Trigger**: If you are about to make **3 or more sequential external tool calls**, pause and evaluate whether they should be offloaded as a DAG.
*   **Reassessment Trigger**: If you have made **5 or more in-context tool calls**, reassess whether the remaining work should be batched into a `tzro_run` task.

### Rules for Delegation

#### 1. Mandatory Delegation Patterns
The following task patterns **MUST** be delegated to `tzro_run`:
*   **Codebase exploration and directory analysis** — handled by **Probe Nodes** with Thought Chain execution internally. Delegate with a goal (e.g., `"Explore the project at /path and explain its architecture"`).
*   **Web research and multi-source information gathering** — delegate as DAG workflows using `web_search` and `save_memory`.
*   **Memory ingestion pipelines** — bulk ingest operations across multiple sources.

#### 2. Do NOT Delegate
*   **Targeted local file operations** (reading a single known file to make a code edit, viewing a specific function) — these feed frontier reasoning directly and must remain in-context.
*   **Simple one-shot tool calls** or conversational Q&A.
*   **Management-plane MCP calls** (`tzro_list_tasks`, `tzro_status`, `tzro_model_list`, `tzro_skills_list`, `tzro_observer_events`, `tzro_observer_memories`) — call these directly to check system status.

---

### Wait Protocol
After calling `tzro_run`, you **MUST**:
1.  **Stop calling other tools.** Do not continue exploring or working in parallel.
2.  **Schedule a wakeup timer** using the `schedule` tool (e.g., set a one-shot reminder for 30-60 seconds) while waiting for the task to run.
3.  When notified, check task status via `tzro_status` (or subscribe to the task resource URI).
4.  Resume only when the task status returns `completed` (or `failed`).
5.  Consume **only** the `terminal_synthesis` output. Do not read individual node `rawOutput` fields — that defeats the compaction benefit.

---

## 🎛️ MCP Tool Taxonomy

The `tzro` MCP server registers **26 tools** split into distinct functional domains.

### 1. Core Execution
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_run` | Plan, compile, and execute a durable DAG from a prompt. | `prompt` (string), `timeout` (int) |
| `tzro_status` | Check execution status, node states, and outcomes of a task. | `taskId` (string) |
| `tzro_resume` | Manually resume a paused/interrupted task (e.g., after approval). | `taskId` (string) |
| `tzro_list_tasks` | List recent tasks, optionally filtered by status. | `status` (string, optional) |

### 2. Local Inference & Cost Arbitrage
Run prompts through the local on-device LLM. Zero cost, zero latency to external APIs, fully private.
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_completion` | Structured text generation (summarization, translation, boilerplate). Supports optional JSON schema (GBNF grammar) constraints. | `systemPrompt` (string), `userPrompt` (string), `jsonSchema` (string, optional) |
| `tzro_classification` | Force-classify text into one of a set of categories using GBNF grammar constraints (guarantees output matches enum). | `input` (string), `categories` (array of strings), `context` (string, optional) |

### 3. Client Tools & Human-in-the-Loop
Allows the local planner to dispatch tools back to the client or pause for approvals.
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_register_client_tools` | Register dynamic tool definitions that the planner can use. | `tools` (array of tool definitions) |
| `tzro_client_tool_list` | List pending tool execution requests awaiting client-side outcomes. | None |
| `tzro_client_tool_submit` | Submit tool results or errors to resume a paused workflow. | `requestId`, `taskId`, `nodeId`, `output`, `error` |
| `tzro_hook_list` | List human-in-the-loop workflow approval requests awaiting action. | None |
| `tzro_hook_approve` | Approve a paused step and resume task execution. | `taskId` (string), `nodeId` (string) |

### 4. Memory & Retrieval
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_memory_ingest` | Ingest a fact memory into the database (fact, preference, insight, strategy). | `content` (string), `type` (string), `source` (string) |
| `tzro_memory_query` | Query memories/KG nodes using hybrid semantic/text similarity. | `query` (string), `limit` (int) |
| `tzro_rag_context` | Get graph-RAG context retrieved semantically for a query. | `query` (string), `maxChars` (int) |

### 5. Knowledge Graph
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_kg_add_entity` | Add/update nodes and edge relationships in the KG. | `node` (object, optional), `edge` (object, optional) |
| `tzro_kg_neighborhood` | Traverse connected entities in the KG starting from a node ID. | `entityId` (string), `maxHops` (int), `limit` (int) |

### 6. Micro-Skills (SOPs)
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_skills_add` | Register a new Standard Operating Procedure (SOP) micro-skill. | `name` (string), `triggerDescription` (string), `sopContent` (string) |
| `tzro_skills_get` | Get full details of a specific SOP skill by its ID. | `id` (string) |
| `tzro_skills_list` | List all registered micro-skills and SOPs. | `limit` (int) |
| `tzro_skills_relevant` | Find relevant micro-skills via semantic search. | `prompt` (string), `limit` (int) |

### 7. Configuration & Observability
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_configure_tools` | Provision external MCP server hosts dynamically for planning. | `hosts` (map of server configs) |
| `tzro_web_search` | Execute multi-engine web search with tiered fallback. | `query` (string), `maxResults` (int) |
| `tzro_model_list` | List available GGUF models in the catalog with download status. | None |
| `tzro_model_set` | Change the active local LLM model (downloads and swaps sidecars). | `modelId`, `ggufModelPath`, or `downloadUrl` |
| `tzro_observer_events` | Retrieve recent observer verification and audit logs. | `limit` (int) |
| `tzro_observer_memories` | List memories dynamically synthesized by the Observer Agent. | `limit` (int) |

---

## 📡 MCP Resource Templates & Subscriptions

For real-time observability and streaming task metrics, the `tzro` MCP server exposes two URI templates. Clients can subscribe to these URIs to receive push notifications when task states update.

### Resource URI Templates
*   **Task Output**: `tzro://tasks/{taskId}/output{?format}`
    *   *Description*: Retrieves the status, metrics, and consolidated output for a task.
    *   *Parameters*: `format` (string, optional) — Set to `raw` to include full, uncompacted output; defaults to compact mode (zeros out large intermediate `rawOutput` values).
*   **Granular Node Output**: `tzro://tasks/{taskId}/nodes/{nodeId}/output{?format}`
    *   *Description*: Retrieves the status and output of a specific node within a task.

### How to use Subscriptions
1.  Call the MCP `resources/subscribe` method with the desired URI (e.g. `tzro://tasks/3c812c-9a4f/output`).
2.  The MCP server will send a `notifications/resources/updated` notification whenever the task progresses, has state changes, or completes.
3.  Retrieve the updated payload by calling `resources/read` on that URI.

---

## 📝 DAG Prompt Design Rules

When composing prompts for `tzro_run` (which translates prompts into Kahn-compiled DAG execution nodes), adhere to these principles:

### 1. Strategy vs. Execution
Describe the *goal*, not the sequence of operations. Do not attempt to pre-execute steps.
*   ❌ **BAD**: *"First call web_search, then parse the results, then save to memory"*
*   ✅ **GOOD**: *"Research the latest AI orchestration trends and save a structured summary to memory"*

### 2. Variable Binding
The compiler automatically injects outputs from parent nodes into child nodes using JSONPath syntax (`{{nodes.node_id.output.property}}`). Let the planner resolve data bindings naturally by writing descriptive, continuous goals.

### 3. Constraint-Based Prompting
When your task requires specific tools or services, name them to constrain the planner's node `allowedTools` list. This prevents tool hallucination.
*   ✅ *"Query recent leads from Salesforce using salesforce_query, deduplicate against our database, and post a report to the #sales-ops Slack channel"*

### 4. Exploration → Use Probe Nodes (Not Rigid DAGs)
If a task involves navigating an unknown codebase, analyzing a directory, or searching logs where the next step depends on what you discover, **force a Probe Node**. Rigid DAGs fail because the compiler cannot extract paths/parameters without seeing intermediate results.
*   ✅ **Example**: *"Explore the project at /path/to/repo using a Probe Node. Read the top-level structure, then follow the most important files to understand the architecture. Use read_file, list_dir, and search_files."*
*   *Key constraint*: Explicitly name `allowedTools` (e.g., `read_file`, `list_dir`, `search_files`) to keep the Thought Chain focused.

---

## 🛠️ Delegation Templates

### Template A: Complex Research & Ingestion
```
"Use web_search to find the latest changes and trends in [TOPIC], compile the findings, and save the final structured summary to memory using the save_memory tool."
```

### Template B: Multi-System Automation
```
"Execute a workflow to query recent [RECORD_TYPE] records using [SOURCE_TOOL], run deduplication check with [DB_TOOL], and post the execution report to [NOTIFICATION_TOOL]."
```

### Template C: Codebase Exploration & Analysis (Probe Node)
```
"Explore the codebase at [PATH] using a Probe Node. Walk the directory structure to identify key entrypoints, read main modules, and output a high-level architectural overview. Use read_file, list_dir, and search_files."
```

### Template D: Memory & Graph RAG Retrieval
```
Use tzro_memory_query with "[QUESTION]" to search fact memories.
Use tzro_rag_context to retrieve semantic graph-RAG context.
Use tzro_kg_neighborhood to explore entity connections.
```

---

## 📖 Domain Language Glossary

To ensure alignment across schemas, tools, and code, use the following terms:
*   **Task**: A compiled sequence of execution steps (do not use "job" or "process").
*   **Kahn Compiler**: The compiler that topologically sorts abstract graphs into parallel execution layers.
*   **Local Model**: The local inference worker (GGUF, Llama sidecar) used for cost arbitrage.
*   **MCP Host**: The integration layer connecting external tools.
*   **Procedural Micro-Skill**: Standard Operating Procedures (SOPs) extracted from successful runs.

<!-- END TZRO INSTRUCTIONS -->
