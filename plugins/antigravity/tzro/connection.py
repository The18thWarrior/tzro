"""Tzro Connection client for Google Antigravity SDK."""

import json
import logging
from typing import Any, Optional

from .tools import get_mcp_client, execute_task_loop, format_synthesis_report

logger = logging.getLogger("google.antigravity.tzro")

# Try to import Connection from google.antigravity if it exists, otherwise define a stub
try:
    from google.antigravity.types import Connection, ConnectionResult
except ImportError:
    # Stub connection base class for runtime portability
    class Connection:
        pass
    class ConnectionResult:
        def __init__(self, content: str, raw_data: dict):
            self.content = content
            self.raw_data = raw_data

class TzroConnection(Connection):
    """Custom Connection that compiles prompts into DAGs and delegates execution

    to the local tzro engine, dispatching tools locally to the agent in-process.
    """
    def __init__(self, config: Optional[Any] = None):
        self.config = config
        self.mcp_client = None

    async def execute(self, prompt: str, ctx: Any) -> ConnectionResult:
        """Executes a natural language prompt via the local tzro engine,

        forwarding tool calls to the local agent context.
        """
        logger.info("TzroConnection initiating workflow for prompt: %s", prompt)
        
        # Start the mcp client bridge to tzro-mcp subprocess
        self.mcp_client = get_mcp_client()
        try:
            # Register agent tools with the tzro engine
            agent_tools = []
            tools_source = None
            if hasattr(ctx, "agent") and hasattr(ctx.agent, "tools"):
                tools_source = ctx.agent.tools
            elif hasattr(ctx, "tools"):
                tools_source = ctx.tools
                
            if tools_source:
                for tool in tools_source:
                    if hasattr(tool, "schema"):
                        schema = tool.schema
                    elif isinstance(tool, dict):
                        schema = tool
                    else:
                        continue
                    agent_tools.append({
                        "name": schema.get("name"),
                        "description": schema.get("description", ""),
                        "inputSchema": schema.get("parameters", {})
                    })
            
            if agent_tools:
                logger.info("Registering %d client tools with tzro engine", len(agent_tools))
                self.mcp_client.call_tool("tzro_register_client_tools", {"tools": agent_tools})
                
            # Submit task run
            run_res = self.mcp_client.call_tool("tzro_run", {
                "prompt": prompt,
                "timeout": 1
            })
            run_text = run_res["content"][0]["text"]
            run_data = json.loads(run_text)
            
            task_id = run_data.get("taskId")
            if not task_id:
                raise RuntimeError(f"Failed to initialize tzro task: {run_text}")
                
            logger.info("tzro task started successfully with Task ID: %s", task_id)
            
            # Run the execution loop to intercept client tool requests and resume tasks
            if run_data.get("status") in ("completed", "failed"):
                final_status = run_data
            else:
                final_status = await execute_task_loop(ctx, self.mcp_client, task_id)
                
            # Format and return the result
            report = format_synthesis_report(task_id, final_status)
            return ConnectionResult(content=report, raw_data=final_status)
            
        except Exception as e:
            logger.error("TzroConnection execution error: %s", str(e))
            raise
        finally:
            if self.mcp_client:
                self.mcp_client.stop()
                self.mcp_client = None
