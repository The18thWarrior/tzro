---
name: tzro
description: >
  Delegate complex multi-step workflows, tool-heavy automations, and high-latency
  data pipelines to the local tzro durable execution engine. ACTIVATE this skill when
  the user asks to run a multi-tool workflow, delegate to tzro, query tzro memory or
  knowledge graph, manage micro-skills, or any request that benefits from durable
  checkpointed DAG execution. Trigger on mentions of: 'tzro', 'delegate', 'durable
  workflow', 'DAG', 'multi-step automation', 'knowledge graph', 'micro-skill',
  'compaction', 'Kahn compiler', or 'local-first execution'.
---

# tzro — Durable Local-First Execution Engine

## Overview

tzro compiles natural language prompts into topologically-sorted DAG workflows, executes them durably with checkpointing, and provides relational memory, knowledge graph, and micro-skill extraction. Use it to offload complex, multi-step work from the agent's main context.

**When to delegate to tzro:**
- Multi-step workflows requiring 2+ tool calls with data dependencies
- Tool-heavy automations (CRM syncs, data pipelines, bulk operations)
- High-latency data consumption that would thrash the context window
- Any request the user explicitly asks to delegate to tzro

**When NOT to delegate:**
- Simple one-shot tool calls
- Conversational Q&A
- File edits or code generation that you can do directly
- Local workspace operations (file reads, directory listings, code edits)
- Management-plane MCP calls (`tzro_list_tasks`, `tzro_status`, `tzro_model_list`, `tzro_skills_list`, `tzro_observer_events`) — call these directly

## Offload Policy

The **Offload Policy** governs when you should submit work to tzro as a **Task** versus executing directly in your own context window.

### Trigger
If you are about to make **3 or more sequential external tool calls**, pause and evaluate whether they should be offloaded as a DAG.

### Decision Rule
Ask one question: **"Do I need to reason about intermediate outputs to decide the next step?"**
- **Yes** → Keep in-context. You are in a frontier reasoning chain.
- **No** → Offload to `tzro_run`. The steps are independent and benefit from parallelization, compaction, and checkpointing.

### Scope
The policy applies to **data-plane tool calls** — `web_search`, `save_memory`, `recall_memory`, `query_knowledge_graph`, `explore_entity`, API queries, and MCP Host integrations. It does **not** apply to local workspace operations or management-plane MCP introspection calls (which have no DAG-level tool equivalents).

### Consuming Results
After a delegated Task completes, consume **only** the `terminal_synthesis` output. Do not read individual node `rawOutput` fields. If you need specific intermediate details, use `tzro_memory_query` or `tzro_rag_context` to retrieve them through the compacted retrieval path.

## Prerequisites

Before using tzro, verify the engine is running:

1. **Check if the MCP server is available** — the tzro MCP tools (prefixed `mcp_tzro_`) should be in your tool list. If they are, you're ready.
2. **If MCP is unavailable**, start the coordinator daemon:
   ```bash
   TZRO_DIR=$(pwd) ./bin/tzrod
   ```
   Then use the CLI fallback: `./bin/tzro chat "your prompt"`

## MCP Tool Taxonomy

The 21 tzro MCP tools are organized into functional groups. Read `references/mcp-tools.md` for full parameter details.

### Execution (Core)
| Tool | Purpose |
|------|---------|
| `tzro_run` | Plan, compile, and execute a durable DAG from a natural language prompt |
| `tzro_status` | Check execution status, node states, and outcomes of a task |
| `tzro_resume` | Resume a paused/interrupted task |
| `tzro_list_tasks` | List recent tasks, optionally filtered by status |

### Memory & Retrieval
| Tool | Purpose |
|------|---------|
| `tzro_memory_ingest` | Ingest a new fact memory with optional embedding |
| `tzro_memory_query` | Hybrid semantic/text search over fact memories and KG nodes |
| `tzro_rag_context` | Get graph-RAG context for a natural language query |

### Knowledge Graph
| Tool | Purpose |
|------|---------|
| `tzro_kg_add_entity` | Add/update nodes and edge relationships |
| `tzro_kg_neighborhood` | Traverse connected entities via multi-hop neighborhood search |

### Micro-Skills
| Tool | Purpose |
|------|---------|
| `tzro_skills_add` | Register a new SOP micro-skill |
| `tzro_skills_get` | Get full details of a skill by ID |
| `tzro_skills_list` | List all registered micro-skills |
| `tzro_skills_relevant` | Find relevant skills via semantic search |

### Client Tools & Human-in-the-Loop
| Tool | Purpose |
|------|---------|
| `tzro_register_client_tools` | Register dynamic tool definitions for the planner |
| `tzro_client_tool_list` | List pending tool execution requests |
| `tzro_client_tool_submit` | Submit execution outcomes to resume a paused workflow |
| `tzro_hook_list` | List pending approval requests |
| `tzro_hook_approve` | Approve a paused human-in-the-loop step |

### Configuration & Observability
| Tool | Purpose |
|------|---------|
| `tzro_configure_tools` | Provision external MCP server hosts dynamically |
| `tzro_observer_events` | Retrieve observer verification/audit telemetry |
| `tzro_observer_memories` | List memories synthesized by the background Observer Agent |

## DAG Prompt Design Rules

When composing prompts for `tzro_run`, follow these rules to produce optimal DAGs:

### 1. Strategy vs. Execution
Let the planner compile the DAG. Do NOT pre-execute steps or try to control node ordering — describe the *goal*, not the procedure.

```
❌ BAD: "First call web_search, then parse the results, then save to memory"
✅ GOOD: "Research the latest AI orchestration trends and save a structured summary to memory"
```

### 2. Variable Binding
The planner uses strict variable binding (`{{nodes.node_id.output.property}}`) to pass data between nodes. You don't need to specify this — the planner handles it automatically when you describe data flow naturally.

### 3. Restricted Tool Spaces
Explicitly mention which tools/integrations are relevant when the task involves specific services. This helps the planner constrain each node's `allowedTools` to 1-2 tools, preventing execution hallucinations.

```
✅ "Query recent leads from Salesforce using salesforce_query, deduplicate against our database, 
    and post a report to the #sales-ops Slack channel"
```

### 4. Conciseness
Keep prompts focused. The resulting DAG should be compact (typically 2-4 nodes). Avoid overloading a single prompt with unrelated objectives.

## Delegation Templates

### Template A: Research & Ingestion
When gathering information from the web, synthesizing, and persisting to memory:

```
"Use web_search to find the latest changes and trends in [TOPIC], 
compile the findings, and save the final structured summary to memory 
using the save_memory tool."
```

### Template B: Multi-System Automation
When orchestrating across multiple services with deduplication/alerting:

```
"Execute a workflow to query recent [RECORD_TYPE] records using [SOURCE_TOOL], 
run deduplication check with [DB_TOOL], and post the execution report 
to the [NOTIFICATION_TOOL]."
```

### Template C: Memory & Knowledge Graph Query
When the user needs context from previous executions or stored knowledge:

```
# Query memories
Use tzro_memory_query with the user's question to find relevant stored facts.

# Get graph-RAG context
Use tzro_rag_context to retrieve semantically relevant context for augmented responses.

# Traverse knowledge graph
Use tzro_kg_neighborhood to explore entity relationships starting from a known node.
```

## CLI Fallback

If MCP tools are unavailable, use the CLI directly:

```bash
# Submit a task
./bin/tzro chat "Your detailed research or automation request"

# Check task status
./bin/tzro task status <taskId> --offline

# Read final output from the terminal_synthesis node
```

## Domain Language

Read `references/domain-language.md` for the canonical terminology. Key terms:
- **Task** (not "job" or "process") — a compiled sequence of execution steps
- **Kahn Compiler** (not "graph builder") — compiles Abstract Graphs into execution layers
- **Local Model** (not "local LLM") — the default-path inference workhorse
- **MCP Host** (not "custom connector") — inbound tool integration layer
- **Procedural Micro-Skill** (not "RAG document") — structured SOP from successful trajectories
