"""High-level orchestrator for tzro execution within the Hermes Agent runtime.

Ties together the MCP client bridge, client tool registration, and the
task execution loop. Shaped for the Hermes plugin lifecycle — no base
class inheritance required (unlike the Antigravity SDK Connection pattern).
"""

import json
import logging
from typing import Any, Dict, List, Optional

from .tools import get_mcp_client, execute_task_loop, format_synthesis_report

logger = logging.getLogger("hermes.tzro")


class TzroExecutor:
    """Manages a single tzro task execution lifecycle within Hermes.

    Handles MCP client startup, tool registration, DAG execution with
    in-process tool dispatch, and clean shutdown.
    """

    def __init__(self, binary_path: Optional[str] = None):
        self.binary_path = binary_path
        self.mcp_client = None

    async def execute(self, prompt: str, ctx: Any) -> Dict[str, Any]:
        """Execute a natural language prompt as a durable DAG workflow.

        Args:
            prompt: Natural language task description.
            ctx: Hermes runtime context providing dispatch_tool().

        Returns:
            Dict with 'report' (formatted markdown) and 'raw' (status data).
        """
        logger.info("TzroExecutor initiating workflow for prompt: %s", prompt)

        self.mcp_client = get_mcp_client(binary_path=self.binary_path)
        try:
            # Register Hermes tools with the tzro engine so the planner
            # can incorporate them into DAG nodes
            await self._register_hermes_tools(ctx)

            # Submit the task
            run_res = self.mcp_client.call_tool(
                "tzro_run", {"prompt": prompt, "timeout": 1}
            )
            run_text = run_res["content"][0]["text"]
            run_data = json.loads(run_text)

            task_id = run_data.get("taskId")
            if not task_id:
                raise RuntimeError(f"Failed to initialize tzro task: {run_text}")

            logger.info("tzro task started: %s", task_id)

            # Run the execution loop — client tool requests are dispatched
            # directly through ctx.dispatch_tool()
            if run_data.get("status") in ("completed", "failed"):
                final_status = run_data
            else:
                final_status = await execute_task_loop(
                    ctx, self.mcp_client, task_id
                )

            report = format_synthesis_report(task_id, final_status)
            return {"report": report, "raw": final_status}

        except Exception as e:
            logger.error("TzroExecutor execution error: %s", str(e))
            raise
        finally:
            if self.mcp_client:
                self.mcp_client.stop()
                self.mcp_client = None

    async def _register_hermes_tools(self, ctx: Any) -> None:
        """Discover tools available in the Hermes context and register them with tzro."""
        agent_tools: List[Dict[str, Any]] = []

        # Hermes exposes available tools via ctx.available_tools or ctx.tools
        tools_source = None
        if hasattr(ctx, "available_tools"):
            tools_source = ctx.available_tools
        elif hasattr(ctx, "tools"):
            tools_source = ctx.tools

        if tools_source:
            for tool in tools_source:
                schema: Optional[Dict[str, Any]] = None
                if hasattr(tool, "schema"):
                    schema = tool.schema
                elif isinstance(tool, dict):
                    schema = tool
                elif hasattr(tool, "to_dict"):
                    schema = tool.to_dict()

                if schema:
                    agent_tools.append(
                        {
                            "name": schema.get("name"),
                            "description": schema.get("description", ""),
                            "inputSchema": schema.get("parameters", schema.get("inputSchema", {})),
                        }
                    )

        if agent_tools:
            logger.info(
                "Registering %d Hermes tools with tzro engine", len(agent_tools)
            )
            self.mcp_client.call_tool(
                "tzro_register_client_tools", {"tools": agent_tools}
            )
        else:
            logger.warning(
                "No tools discovered in Hermes context — "
                "tzro will only use its own registered tools"
            )
