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
| `tzro_code` | Generate or update code for a single file using the local model. | `spec` (string), `filepath` (string), `mode` (string, optional: "full"|"diff") |
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

### 8. Code Generation
| Tool | Purpose | Key Parameters |
| :--- | :--- | :--- |
| `tzro_code` | Offload code generation or surgical file updates to the local engine. | `spec` (string), `filepath` (string), `mode` (string, optional) |

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

### 5. Local Code Generation (`tzro_code`)
Use `tzro_code` to offload the expensive writing of code to the local engine.
- **Spec**: Provide a detailed specification (jdoc) including signatures, behavior, and constraints.
- **Modes**:
  - `mode: "full"` (default for new files): Rewrites the whole file. Limited to 500 lines.
  - `mode: "diff"` (default for existing files > 200 lines): Uses structured JSON hunks for surgical edits. Required for files > 500 lines.
- **Context**: The tool automatically reads the target file and 5 sibling files for context.
- **Verification**: You (the frontier model) should review the generated code for correctness, as the local model may produce drafts that need minor polish.

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
