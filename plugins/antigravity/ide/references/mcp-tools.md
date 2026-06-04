# tzro MCP Tools — Full Reference

All tools are available as lazily-loaded MCP tools under the `tzro` server. Call them via `mcp_tzro_<toolName>`.

---

## Execution

### `tzro_run`
Plan, compile, and execute a durable DAG workflow from a natural language prompt.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `prompt` | string | ✅ | The natural language task to execute |
| `timeout` | integer | | Execution timeout in seconds before switching to async. Default 60 |

**Returns:** Object with `taskId`, `status`, and node execution results. If the task completes within the timeout, includes full synthesis output. Otherwise returns the taskId for polling via `tzro_status`.

**Example:**
```json
{
  "prompt": "Search the web for recent Kubernetes security advisories and save a structured summary to memory",
  "timeout": 120
}
```

---

### `tzro_status`
Check execution status, node states, and outcomes of a specific task.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `taskId` | string | ✅ | The task ID to check |

**Returns:** Task status object with overall state (`running`, `completed`, `failed`, `paused`), per-node execution states, and terminal synthesis output if completed.

---

### `tzro_resume`
Resume execution of a paused/interrupted task.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `taskId` | string | ✅ | The task ID to resume |

---

### `tzro_list_tasks`
List recent planning and execution tasks.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | | Filter by status: `running`, `completed`, `failed`, `paused` |
| `limit` | integer | | Max tasks to return |

---

## Memory & Retrieval

### `tzro_memory_ingest`
Ingest a new fact memory into the SQLite database with optional embedding.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `content` | string | ✅ | The fact content to store |
| `source` | string | | Provenance source identifier |
| `metadata` | object | | Arbitrary metadata properties |

---

### `tzro_memory_query`
Query fact memories and knowledge graph nodes using hybrid semantic/text similarity.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | ✅ | Natural language semantic search query |
| `limit` | integer | | Max memories and nodes to return. Default 10 |

**Returns:** Ranked list of matching memories and KG nodes with similarity scores.

---

### `tzro_rag_context`
Get graph-RAG context retrieved semantically for a query.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | ✅ | The query to retrieve context for |
| `maxChars` | integer | | Max character limit of returned context. Default 2000 |

**Use this** when you need augmented context from the knowledge graph to enrich a response, rather than raw memory search results.

---

## Knowledge Graph

### `tzro_kg_add_entity`
Add or update nodes and/or edge relationships.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `node` | object | | Node entity to add/update |
| `node.id` | string | ✅ (if node) | Unique identifier |
| `node.nodeType` | string | ✅ (if node) | Type: `account`, `contact`, `ticket`, `document`, etc. |
| `node.name` | string | ✅ (if node) | Display name |
| `node.metadata` | object | | Arbitrary properties |
| `node.source` | string | | Provenance source |
| `node.weight` | number | | Importance weight 0.0–1.0 |
| `edge` | object | | Edge relationship to add/update |
| `edge.id` | string | ✅ (if edge) | Unique identifier |
| `edge.edgeType` | string | ✅ (if edge) | Type: `belongs_to`, `assigned_to`, `references`, etc. |
| `edge.sourceId` | string | ✅ (if edge) | Source node ID |
| `edge.targetId` | string | ✅ (if edge) | Target node ID |
| `edge.weight` | number | | Edge weight 0.0–1.0 |
| `edge.metadata` | object | | Arbitrary properties |

---

### `tzro_kg_neighborhood`
Traverse connected entities via multi-hop neighborhood search.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `nodeId` | string | ✅ | Starting node ID |
| `maxHops` | integer | | Max traversal depth. Default 2 |
| `limit` | integer | | Max entities to return |

---

## Micro-Skills

### `tzro_skills_add`
Register a new SOP micro-skill.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | ✅ | Skill name |
| `content` | string | ✅ | Full SOP markdown content |
| `trigger` | string | | Trigger pattern for automatic activation |

---

### `tzro_skills_get`
Get full details of a specific skill.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `skillId` | string | ✅ | The skill ID |

---

### `tzro_skills_list`
List all registered micro-skills and SOPs.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `limit` | integer | | Max skills to return |

---

### `tzro_skills_relevant`
Find relevant micro-skills using semantic search.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | ✅ | Natural language search query |
| `limit` | integer | | Max skills to return |

---

## Client Tools & Human-in-the-Loop

### `tzro_register_client_tools`
Register dynamic client-side tool definitions for the planner.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `tools` | array | ✅ | List of tool definitions |
| `tools[].name` | string | ✅ | Tool name |
| `tools[].description` | string | ✅ | Tool description |
| `tools[].inputSchema` | object | ✅ | JSON Schema parameters |

**Use this** to expose the parent agent's tools to tzro's planner so it can incorporate them into DAG nodes.

---

### `tzro_client_tool_list`
List pending client-side tool execution requests awaiting outcomes.

**Use this** in the client tool execution loop: check for pending requests, execute them locally, then submit results via `tzro_client_tool_submit`.

---

### `tzro_client_tool_submit`
Submit execution outcomes for a client-side tool to resume the paused workflow.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `requestId` | string | ✅ | The pending request ID |
| `result` | string | ✅ | The tool execution result |
| `error` | string | | Error message if execution failed |

---

### `tzro_hook_list`
List pending human-in-the-loop approval requests.

---

### `tzro_hook_approve`
Approve a paused execution step and trigger resumption.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `hookId` | string | ✅ | The approval request ID |
| `approved` | boolean | ✅ | Whether to approve or reject |
| `reason` | string | | Reason for the decision |

---

## Configuration & Observability

### `tzro_configure_tools`
Provision external MCP server hosts dynamically.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `servers` | object | ✅ | Map of server name → MCP server config |
| `servers.<name>.command` | string | ✅ | Command to start the server |
| `servers.<name>.args` | array | | Command arguments |
| `servers.<name>.env` | object | | Environment variables |
| `servers.<name>.useDocker` | boolean | | Run in Docker container |
| `servers.<name>.dockerImage` | string | | Docker image name |
| `servers.<name>.dockerOpts` | array | | Docker run options |

**Example:**
```json
{
  "servers": {
    "hubspot": {
      "command": "npx",
      "args": ["-y", "@hubspot/mcp-server"],
      "env": { "HUBSPOT_API_KEY": "$HUBSPOT_API_KEY" }
    }
  }
}
```

---

### `tzro_observer_events`
Retrieve recent observer verification and audit telemetry logs.

---

### `tzro_observer_memories`
List memories dynamically synthesized by the background Observer Agent.
