# tzro Quickstart Examples

This directory contains the Go SDK quickstart implementation (`main.go` and `main_test.go`) demonstrating how to programmatically initialize `tzro`, register custom tools, compile a Directed Acyclic Graph (DAG) using Kahn's topological sort, and run the execution loop with stream subscription.

---

## 🏃 How to Run the Quickstart

You can run the quickstart example directly using `go run`:

```bash
go run examples/quickstart/main.go
```

To run the associated integration tests:

```bash
go test -v ./examples/quickstart/...
```

---

## 🤖 Agent Delegation via MCP Setup

For AI agents (such as ourselves) pair programming in this repository, executing complex data consumption pipelines or tool-heavy automations manually in-context is highly inefficient and brittle. Instead, agents should delegate these operations to the local `tzro` engine by running it in **MCP Server Mode**.

### 1. Workspace Configuration

Ensure that `tzro` is configured as an MCP server in your project's `.agents/mcp_config.json`:

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

### 2. Suggested Prompts for Agents to Leverage tzro

When delegating tasks to `tzro` via the `tzro_run` MCP tool or the CLI `tzro chat` command, agents must write prompts that guide the **Strategic Planner** to balance design tradeoffs.

Below are suggested prompt templates for common tool-heavy and research tasks:

#### Template A: Complex Research and Ingestion (e.g., AI Orchestration Space)
Use this when you need to gather information from the web, synthesize it, and persist it into the relational memory.

> **Suggested Prompt:**
> *"Use web_search to find the latest changes and trends in the AI orchestration space, compile the findings, and save the final structured summary to memory using the save_memory tool."*

*   **Planner Tradeoff Balanced:** Ensures a linear 3-tier sequence: `web_search` -> `save_memory` -> `terminal_synthesis`.
*   **Variable Binding:** The planner binds the search output `{{nodes.node_search_exec.output}}` dynamically to the input of the `save_memory` node.

#### Template B: Tool-Heavy Multi-System Automation (e.g., Salesforce CRM & Slack Alerting)
Use this when you need to fetch bulk records from a service, run local deduplication/updates, and notify a target channel.

> **Suggested Prompt:**
> *"Execute a workflow to query recent lead records using salesforce_query, run deduplication check with postgres_insert, and post the execution report to the slack_message tool."*

*   **Planner Tradeoff Balanced:** Limits `allowedTools` at each node to prevent action space hallucinations.
*   **Conciseness:** Restricts the graph to a clean 3-level pipeline (`salesforce_query` -> `postgres_insert` -> `slack_message`).

---

### 💡 Core Design Rules for Agent Prompts

When constructing delegation prompts, always design them to enforce the following Planner guidelines:
1.  **Define Edge Boundaries Explicitly:** Name the starting tool, intermediate processing steps, and final destination tool.
2.  **Declare Data Flow / Variable Bindings:** Mention that output from step X should feed into step Y (e.g., *"using the output/IDs from the query"*).
3.  **Restrict allowedTools:** Be specific about the tools to be used at each step so the planner restricts the local model's worker action space to only those tools.
4.  **Avoid Cycles:** Ensure the flow moves forward linearly or in clear parallel branches. Keep graphs under 4 nodes for optimal token efficiency.
