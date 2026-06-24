#!/usr/bin/env python3
"""QA Harness Check Utility.

Validates that the StdioMCPClient and execute_task_loop correctly boot,
register host client tools, handle execution pause/resume, and update task status.
"""

import os
import sys
import json
import shutil
import sqlite3
import tempfile
import time
import asyncio

# Add repository root to python path
repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "..", ".."))
sys.path.insert(0, repo_root)

try:
    from plugins.antigravity.tzro.tools import StdioMCPClient, handle_resume, get_mcp_client
except ImportError:
    print("Error: Could not import antigravity tzro tools. Ensure Python path includes the repository root.")
    sys.exit(1)

class MockTool:
    def __init__(self, name, description, parameters, callback):
        self._name = name
        self.description = description
        self.parameters = parameters
        self.callback = callback

    @property
    def name(self):
        return self._name

    @property
    def schema(self):
        return {
            "name": self._name,
            "description": self.description,
            "parameters": self.parameters
        }

    async def execute(self, **kwargs):
        return await self.callback(self._name, kwargs)

class MockAgentContext:
    def __init__(self):
        self.called_tools = []
        self.tools = [
            MockTool(
                name="send_slack",
                description="Send a message to a slack channel",
                parameters={
                    "type": "object",
                    "properties": {
                        "channel": {"type": "string"},
                        "message": {"type": "string"}
                    },
                    "required": ["channel", "message"]
                },
                callback=self.dispatch_tool
            )
        ]
        
    async def dispatch_tool(self, name, arguments):
        self.called_tools.append((name, arguments))
        if name == "send_slack":
            print(f"  [MockAgent] send_slack invoked for channel {arguments.get('channel')}")
            return json.dumps({"status": "delivered", "message": f"Sent alert to {arguments.get('channel')}"})
        return "mocked response"

async def run_harness_check():
    print("=== Starting tzro MCP Harness Validation ===")
    
    # 1. Locate tzro-mcp binary
    mcp_path = os.environ.get("TZRO_MCP_PATH")
    if not mcp_path:
        candidates = [
            os.path.join(repo_root, "bin", "tzro-mcp"),
            os.path.join(repo_root, "tzro-mcp"),
        ]
        for candidate in candidates:
            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                mcp_path = candidate
                break
                
    if not mcp_path:
        print(f"Error: Could not locate tzro-mcp binary. Please build it first: go build -o bin/tzro-mcp cmd/mcp/main.go")
        sys.exit(1)
        
    print(f"Found tzro-mcp binary at: {mcp_path}")
    
    # 2. Setup temp directory and environment for isolation
    temp_dir = tempfile.mkdtemp()
    db_path = os.path.join(temp_dir, "tzro.db")
    env = os.environ.copy()
    env["TZRO_DIR"] = temp_dir
    env["TZRO_MCP_PATH"] = mcp_path
    
    print(f"Running isolated tests in temp directory: {temp_dir}")
    
    # Override the client factory for testing
    import plugins.antigravity.tzro.tools as tzro_tools
    original_get_mcp_client = tzro_tools.get_mcp_client
    
    def test_get_mcp_client():
        client = StdioMCPClient(mcp_path, env=env)
        client.start()
        return client
        
    tzro_tools.get_mcp_client = test_get_mcp_client
    
    try:
        # Start and stop client once to run database migrations
        print("Initializing database schema...")
        init_client = test_get_mcp_client()
        time.sleep(1.0)
        init_client.stop()
        
        # 3. Create mock graph with client tool node
        task_id = "test-harness-task-1"
        graph = {
            "taskId": task_id,
            "maxCycles": 5,
            "createdAt": int(time.time()),
            "nodes": [
                {
                    "id": "node1",
                    "type": "deterministic",
                    "action": "send_slack",
                    "instructions": '{"tool_arguments": {"channel": "#qa-channel", "message": "harness check passed"}}',
                    "allowedTools": ["send_slack"],
                    "status": "pending"
                }
            ],
            "edges": []
        }
        
        # Write directly to the SQLite temp DB cache
        conn = sqlite3.connect(db_path)
        c = conn.cursor()
        c.execute(
            "INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
            (f"graph_{task_id}", json.dumps(graph), "", int(time.time()))
        )
        conn.commit()
        conn.close()
        
        print(f"Task graph injected successfully into SQLite cache: {task_id}")
        
        # 4. Trigger resume and run tool loops
        print("Triggering handle_resume execution loop...")
        ctx = MockAgentContext()
        report = await handle_resume(ctx, task_id)
        
        print("\n=== Result Report ===")
        print(report)
        print("=====================\n")
        
        # Assertions
        assert "completed" in report, "Task failed to transition to completed"
        assert len(ctx.called_tools) == 1, "Mock tool was not executed by the harness"
        assert ctx.called_tools[0][0] == "send_slack", f"Unexpected tool executed: {ctx.called_tools[0][0]}"
        assert ctx.called_tools[0][1].get("channel") == "#qa-channel", "Incorrect tool arguments forwarded"
        
        print("Success: All harness assertions passed successfully!")
        
    except AssertionError as ae:
        print(f"Assertion Failure: {ae}")
        sys.exit(1)
    except Exception as e:
        print(f"Test Execution Error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
    finally:
        # Clean up
        tzro_tools.get_mcp_client = original_get_mcp_client
        shutil.rmtree(temp_dir)
        print("Cleanup completed.")

if __name__ == "__main__":
    asyncio.run(run_harness_check())
