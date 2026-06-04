# tzro — Durable Execution Delegation

When executing complex multi-step workflows, tool-heavy automations, or high-latency data pipelines, delegate execution to the local **tzro** engine rather than running tools manually.

## When to Delegate

**Delegate to tzro when:**
- Multi-step workflows requiring 2+ tool calls with data dependencies
- Tool-heavy automations (CRM syncs, data pipelines, bulk operations)
- High-latency data consumption that would thrash the context window
- The user explicitly asks to delegate or mentions "tzro"

**Do NOT delegate when:**
- Simple one-shot tool calls
- Conversational Q&A
- File edits or code generation you can do directly

## MCP Tool Taxonomy

| Tool | Purpose |
|------|---------|
| `tzro_run` | Plan, compile, and execute a durable DAG from a natural language prompt |
| `tzro_status` | Check execution status, node states, and outcomes of a task |
| `tzro_resume` | Resume a paused/interrupted task |
| `tzro_list_tasks` | List recent tasks, optionally filtered by status |
| `tzro_memory_ingest` | Ingest a new fact memory with optional embedding |
| `tzro_memory_query` | Hybrid semantic/text search over fact memories and KG nodes |
| `tzro_rag_context` | Get graph-RAG context for a natural language query |
| `tzro_kg_add_entity` | Add/update knowledge graph nodes and edge relationships |
| `tzro_kg_neighborhood` | Traverse connected entities via multi-hop neighborhood search |
| `tzro_skills_add` | Register a new SOP micro-skill |
| `tzro_skills_list` | List all registered micro-skills |
| `tzro_skills_relevant` | Find relevant skills via semantic search |
| `tzro_configure_tools` | Provision external MCP server hosts dynamically |

## DAG Prompt Design Rules

### Describe Goals, Not Procedures
Let the planner compile the DAG. Describe the *goal*, not the step sequence.

```
❌ BAD:  "First call web_search, then parse the results, then save to memory"
✅ GOOD: "Research the latest AI orchestration trends and save a structured summary to memory"
```

### Mention Relevant Tools
When the task involves specific services, name them so the planner constrains each node's tool space:

```
✅ "Query recent leads from Salesforce using salesforce_query, deduplicate against
    our database, and post a report to the #sales-ops Slack channel"
```

### Keep Prompts Focused
The resulting DAG should be compact (typically 2-4 nodes). Avoid overloading a single prompt with unrelated objectives.

## Delegation Templates

**Research & Ingestion:**
```
"Use web_search to find the latest changes and trends in [TOPIC],
compile the findings, and save the final structured summary to memory."
```

**Multi-System Automation:**
```
"Execute a workflow to query recent [RECORD_TYPE] records using [SOURCE_TOOL],
run deduplication check with [DB_TOOL], and post the execution report
to the [NOTIFICATION_TOOL]."
```

**Memory & Knowledge Graph Query:**
```
Use tzro_memory_query with the user's question to find relevant stored facts.
Use tzro_rag_context to retrieve semantically relevant context.
Use tzro_kg_neighborhood to explore entity relationships.
```

## Domain Terminology

- **Task** (not "job" or "process") — a compiled sequence of execution steps
- **Kahn Compiler** (not "graph builder") — compiles Abstract Graphs into execution layers
- **Local Model** (not "local LLM") — the default-path inference workhorse
- **MCP Host** (not "custom connector") — inbound tool integration layer
- **Procedural Micro-Skill** (not "RAG document") — structured SOP from successful trajectories
