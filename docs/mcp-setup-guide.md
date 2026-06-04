# tzro MCP Setup Guide

This guide details how to build and configure the `tzro-mcp` server target to run as a Model Context Protocol (MCP) server.

By running `tzro-mcp`, any MCP-compatible AI client (such as Claude Desktop, Cursor, or own-brand agent harnesses) can dynamically discover and execute durable `tzro` workflows, query and ingest relational memory, traverse the knowledge graph, and coordinate client-side tool execution.

---

## 🏗️ 1. Build the MCP Server Binary

First, compile the `tzro-mcp` Go binary. Run the following command from the root of the repository:

```bash
# Build the binary and save it to the local binaries directory
go build -o bin/tzro-mcp ./cmd/tzro-mcp
```

Verify that the binary compiled successfully:

```bash
./bin/tzro-mcp --help
```

---

## ⚙️ 2. Configure Client Applications

MCP clients spawn the `tzro-mcp` server as a background subprocess communicating over standard I/O (`stdio`).

### Claude Desktop

To integrate `tzro` with the official Claude Desktop app, edit your configuration file:

- **Mac OS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

Add `tzro` to the `mcpServers` object:

```json
{
  "mcpServers": {
    "tzro": {
      "command": "/absolute/path/to/tzro/bin/tzro-mcp",
      "args": [],
      "env": {
        "PORT": "8080"
      }
    }
  }
}
```

> [!IMPORTANT]
> Always use the **absolute path** to the compiled `tzro-mcp` binary in the configuration.

### Cursor

To configure `tzro` in Cursor:

1.  Open Cursor and navigate to **Settings** -> **Features** -> **MCP**.
2.  Click **+ Add New MCP Server**.
3.  Fill in the form:
    - **Name:** `tzro`
    - **Type:** `command`
    - **Command:** `/absolute/path/to/tzro/bin/tzro-mcp`
4.  Click **Save**. Cursor will start the server and list the registered tools under the active tool list.

### Google Antigravity

For Google Antigravity, MCP configurations can be registered either globally or at the workspace level. For details, refer to the [Antigravity MCP Documentation](https://antigravity.google/docs/mcp).

#### Workspace/Project Configuration
Create a file at `.agents/mcp_config.json` inside your workspace directory and add the `tzro` server definition:

```json
{
  "mcpServers": {
    "tzro": {
      "command": "/absolute/path/to/tzro/bin/tzro-mcp",
      "args": [],
      "env": {
        "TZRO_DIR": "/absolute/path/to/tzro",
        "ANTIGRAVITY_AGENT": "$ANTIGRAVITY_AGENT",
        "ANTIGRAVITY_TRAJECTORY_ID": "$ANTIGRAVITY_TRAJECTORY_ID",
        "ANTIGRAVITY_LS_ADDRESS": "$ANTIGRAVITY_LS_ADDRESS",
        "ANTIGRAVITY_CSRF_TOKEN": "$ANTIGRAVITY_CSRF_TOKEN"
      }
    }
  }
}
```

#### Global Configuration
Alternatively, add the same configuration block to your global Antigravity config file:

*   **File Location:** `~/.gemini/config/mcp_config.json`

---

## 🛠️ 3. Offered MCP Tools

Once connected, `tzro-mcp` exposes a wide suite of capabilities:

| Tool Name                     | Description                                                                | Key Arguments                                           |
| :---------------------------- | :------------------------------------------------------------------------- | :------------------------------------------------------ |
| **`tzro_run`**                | Compile and execute a durable DAG workflow from a natural language prompt. | `prompt` (string)                                       |
| **`tzro_status`**             | Check the execution status, node states, and outcomes of a specific task.  | `taskId` (string)                                       |
| **`tzro_list_tasks`**         | List recent planning and execution tasks, filtered by status.              | `limit` (int)                                           |
| **`tzro_configure_tools`**    | Dynamically register third-party stdio MCP daemons inside tzro.            | `servers` (map)                                         |
| **`tzro_memory_query`**       | Query fact memories and knowledge graph entities semantically.             | `query` (string)                                        |
| **`tzro_memory_ingest`**      | Ingest a new fact memory into the database.                                | `content` (string), `type` (string)                     |
| **`tzro_kg_neighborhood`**    | Multi-hop knowledge graph traversal starting from a node.                  | `entityId` (string), `maxHops` (int)                    |
| **`tzro_kg_add_entity`**      | Add or update nodes and edge relationships in the graph.                   | `node` (object), `edge` (object)                        |
| **`tzro_rag_context`**        | Retrieve graph-RAG context for prompt generation.                          | `query` (string)                                        |
| **`tzro_skills_add`**         | Sync procedural micro-skills (SOPs) into the database.                     | `name` (string), `sopContent` (string)                  |
| **`tzro_hook_approve`**       | Resume execution of a paused human-in-the-loop task node.                  | `taskId` (string), `nodeId` (string)                    |
| **`tzro_client_tool_submit`** | Submit outputs of client-side executed tools and resume.                   | `taskId` (string), `nodeId` (string), `output` (string) |
| **`tzro_model_list`**         | List available GGUF models with download status and active indicator.      | *(none)*                                                |
| **`tzro_model_set`**          | Change the active local worker model by catalog ID, path, or download URL. | `modelId` or `ggufModelPath` or `downloadUrl`           |

### 💡 Suggested Prompts for Agents to Leverage tzro

When invoking `tzro_run` via an MCP client, formulate prompts that guide the **Strategic Planner** to balance tradeoffs (e.g., limiting `allowedTools`, defining clear dependencies, and utilizing proper variable binding).

Here are prompt templates you can use:
- **Complex Research and Ingestion:**
  > *"Use web_search to find the latest changes and trends in the AI orchestration space, compile the findings, and save the final structured summary to memory using the save_memory tool."*
- **Tool-Heavy Multi-System Automation:**
  > *"Execute a workflow to query recent lead records using salesforce_query, run deduplication check with postgres_insert, and post the execution report to the slack_message tool."*

---

## 🔍 4. Verification and Stdio Safety

### Handshake Test

To test the server manually from the command line, run the binary and paste an MCP initialize JSON-RPC request:

```bash
./bin/tzro-mcp
```

Paste the following JSON block and press `Enter`:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "clientInfo": { "name": "test-client", "version": "1.0.0" }
  }
}
```

The server should respond with its protocol metadata and capabilities JSON. Type `Ctrl+C` to exit.

### Safeguarding the Stdio Pipe

> [!WARNING]
> Because standard I/O is used for JSON-RPC message framing, any output written to `os.Stdout` (such as standard library debug logs or print statements) will corrupt the protocol stream and disconnect the client.
>
> - All internal debug logging, hook telemetry, and initialization warnings in `tzro` are explicitly redirected to `os.Stderr`.
> - If you are writing custom hooks or extensions, **never** print to stdout. Use `fmt.Fprintf(os.Stderr, ...)` or a logging library.
