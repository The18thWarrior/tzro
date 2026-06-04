"""Integration test for the tzro native plugin in a mock agent context."""

import os
import sys
import json
import shutil
import sqlite3
import tempfile
import time
import asyncio
import unittest

# Ensure the repository root is in python path
repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, repo_root)

from plugins.hermes.tzro.tools import StdioMCPClient, handle_resume

class MockContext:
    def __init__(self):
        self.called_tools = []
        
    async def dispatch_tool(self, name, arguments):
        self.called_tools.append((name, arguments))
        if name == "send_slack":
            return json.dumps({"status": "delivered", "message": f"Slack message sent to {arguments.get('channel')}"})
        return "mocked response"

class TestPluginIntegration(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.temp_dir = tempfile.mkdtemp()
        self.db_path = os.path.join(self.temp_dir, "tzro.db")
        
        # Locate tzro-mcp executable
        self.mcp_path = os.path.join(repo_root, "tzro-mcp")
        if not os.path.exists(self.mcp_path):
            self.mcp_path = os.path.join(repo_root, "bin", "tzro-mcp")
            
        if not os.path.exists(self.mcp_path):
            self.skipTest(f"tzro-mcp executable not found at {self.mcp_path}")

    def tearDown(self):
        shutil.rmtree(self.temp_dir)

    async def test_client_tool_dispatch_resume(self):
        # 1. Start StdioMCPClient briefly to boot database and run migrations
        env = os.environ.copy()
        env["TZRO_DIR"] = self.temp_dir
        
        client = StdioMCPClient(self.mcp_path, env=env)
        client.start()
        
        # Wait a moment for migrations to complete
        time.sleep(1.0)
        
        # 2. Insert mock task graph into SQLite disk_cache
        graph = {
            "taskId": "task-test-integration",
            "maxCycles": 5,
            "createdAt": int(time.time()),
            "nodes": [
                {
                    "id": "node1",
                    "type": "deterministic",
                    "action": "send_slack",
                    "instructions": '{"tool_arguments": {"channel": "#alerts", "message": "hello slack"}}',
                    "allowedTools": ["send_slack"],
                    "status": "pending"
                }
            ],
            "edges": []
        }
        
        conn = sqlite3.connect(self.db_path)
        c = conn.cursor()
        c.execute(
            "INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
            ("graph_task-test-integration", json.dumps(graph), "", int(time.time()))
        )
        conn.commit()
        conn.close()
        
        # Clean shutdown of initial client to free file locks
        client.stop()
        
             # 3. Setup mock context and override helpers at runtime
        # to ensure it targets the temp directory
        import plugins.hermes.tzro.tools as tzro_tools
        
        original_get_mcp_client = tzro_tools.get_mcp_client
        def mock_get_mcp_client():
            client = StdioMCPClient(self.mcp_path, env=env)
            client.start()
            return client
            
        tzro_tools.get_mcp_client = mock_get_mcp_client
        
        ctx = MockContext()
        ctx.tools = [{
            "name": "send_slack",
            "description": "Send a message to a slack channel",
            "parameters": {
                "type": "object",
                "properties": {
                    "channel": {"type": "string"},
                    "message": {"type": "string"}
                },
                "required": ["channel", "message"]
            }
        }]
        
        # 4. Trigger resume, which should run the loop, discover client_tool_request,
        # execute send_slack locally, submit the result, and finish the task.
        try:
            report = await handle_resume(ctx, "task-test-integration")
            
            # Assertions
            self.assertIn("task-test-integration", report)
            self.assertIn("completed", report)
            self.assertEqual(len(ctx.called_tools), 1)
            self.assertEqual(ctx.called_tools[0][0], "send_slack")
            self.assertEqual(ctx.called_tools[0][1].get("channel"), "#alerts")
        finally:
            tzro_tools.get_mcp_client = original_get_mcp_client

if __name__ == "__main__":
    unittest.main()
