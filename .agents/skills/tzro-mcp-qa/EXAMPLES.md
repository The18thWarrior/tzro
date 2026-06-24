# tzro MCP QA Examples

This document provides concrete walkthroughs and examples of triggering and validating tasks in Native Plugin Mode.

## Example 1: Standard Client Tool Dispatch

This example demonstrates how to set up a mock client tool, register it, trigger a task that depends on it, run the event loop, and verify completion.

### 1. Test Harness Setup
```python
import os
import json
import asyncio
from plugins.antigravity.tzro.tools import StdioMCPClient, handle_resume

# Define a mock context with a tool
class MockAgentContext:
    def __init__(self):
        self.called_tools = []
        self.tools = [{
            "name": "send_alert",
            "description": "Send an alert notification to a system channel",
            "parameters": {
                "type": "object",
                "properties": {
                    "system": {"type": "string"},
                    "severity": {"type": "string"}
                },
                "required": ["system", "severity"]
            }
        }]

    async def dispatch_tool(self, name, arguments):
        self.called_tools.append((name, arguments))
        return json.dumps({"status": "sent", "timestamp": 123456})

async def run_qa():
    # Path to tzro-mcp binary
    mcp_path = "./bin/tzro-mcp"
    ctx = MockAgentContext()
    
    # Run the resume flow for the task ID
    report = await handle_resume(ctx, "task-alert-verification")
    print(report)

if __name__ == "__main__":
    asyncio.run(run_qa())
```

### 2. Output Verification
When executing successfully, the console should output:
```markdown
## tzro Task Report

**Task ID:** `task-alert-verification`
**Status:** completed

### Node Execution Summary

- ✅ **node1**: completed

### Output

{"status": "sent", "timestamp": 123456}
```

---

## Example 2: Verifying Probe Node & Thought Chain Execution

This example demonstrates how to compile and execute a task graph containing a **Probe Node** for file system exploration, and inspect the SQLite database to verify its execution.

### 1. Setup the Probe Task in SQLite
```python
import sqlite3
import json
import time

# Create a graph payload containing a Probe Node
graph = {
    "taskId": "task-probe-qa",
    "maxCycles": 10,
    "createdAt": int(time.time()),
    "nodes": [
        {
            "id": "explore_node",
            "type": "probe",
            "status": "pending",
            "probeConfig": {
                "goal": "Locate all Go test files under cmd/mcp",
                "allowedTools": ["list_dir", "search_files"],
                "stepBudget": 5,
                "compactEvery": 3
            }
        }
    ],
    "edges": []
}

# Insert into the database cache
conn = sqlite3.connect("tzro.db")
c = conn.cursor()
c.execute(
    "INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
    ("graph_task-probe-qa", json.dumps(graph), "", int(time.time()))
)
conn.commit()
conn.close()
```

### 2. Inspecting the Database
After the executor runs the Probe Node, query the database to verify:
1. The rolling summaries exist (every 3 steps):
   ```sql
   SELECT step_range, summary FROM summaries WHERE probe_id = 'explore_node';
   ```
2. The individual thoughts were saved correctly:
   ```sql
   SELECT step_number, thought, tool_name, confidence FROM thought_steps WHERE probe_id = 'explore_node' ORDER BY step_number;
   ```
3. The node output in the `disk_cache` contains the final goal-directed synthesis.
