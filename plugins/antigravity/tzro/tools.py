"""MCP bridge client for tzro — standalone module replacing the hermes dependency.

Provides StdioMCPClient, task execution loop, status/resume handlers, and
synthesis report formatting. Used by connection.py and agent.py.
"""

import json
import logging
import os
import subprocess
import time
from typing import Any, Dict, List, Optional

logger = logging.getLogger("google.antigravity.tzro")

# ---------------------------------------------------------------------------
# StdioMCPClient — lightweight MCP stdio client for tzro-mcp binary
# ---------------------------------------------------------------------------

class StdioMCPClient:
    """Manages a subprocess running the tzro-mcp binary and communicates via JSON-RPC over stdio."""

    def __init__(self, command: str, args: Optional[List[str]] = None, env: Optional[Dict[str, str]] = None):
        self.command = command
        self.args = args or []
        self.env = {**os.environ, **(env or {})}
        self.process: Optional[subprocess.Popen] = None
        self._request_id = 0

    def start(self) -> None:
        """Start the tzro-mcp subprocess."""
        cmd = [self.command] + self.args
        logger.info("Starting MCP subprocess: %s", " ".join(cmd))
        self.process = subprocess.Popen(
            cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=self.env,
            text=True,
            bufsize=1,
        )
        # Perform MCP initialization handshake
        self._request_id += 1
        init_request = {
            "jsonrpc": "2.0",
            "id": self._request_id,
            "method": "initialize",
            "params": {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "clientInfo": {
                    "name": "tzro-client",
                    "version": "1.0.0"
                }
            }
        }
        self.process.stdin.write(json.dumps(init_request) + "\n")
        self.process.stdin.flush()

        # Read initialize response
        init_response_line = self.process.stdout.readline()
        if not init_response_line:
            raise RuntimeError("MCP subprocess closed stdout during initialization")
        
        # Send initialized notification
        initialized_notification = {
            "jsonrpc": "2.0",
            "method": "notifications/initialized"
        }
        self.process.stdin.write(json.dumps(initialized_notification) + "\n")
        self.process.stdin.flush()

    def stop(self) -> None:
        """Terminate the subprocess."""
        if self.process:
            try:
                self.process.terminate()
                self.process.wait(timeout=5)
            except Exception:
                self.process.kill()
            finally:
                self.process = None

    def call_tool(self, name: str, arguments: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        """Send a JSON-RPC tool call and return the parsed response."""
        if not self.process or self.process.poll() is not None:
            raise RuntimeError("MCP subprocess is not running")

        self._request_id += 1
        request = {
            "jsonrpc": "2.0",
            "id": self._request_id,
            "method": "tools/call",
            "params": {
                "name": name,
                "arguments": arguments or {},
            },
        }

        request_line = json.dumps(request) + "\n"
        self.process.stdin.write(request_line)
        self.process.stdin.flush()

        response_line = self.process.stdout.readline()
        if not response_line:
            raise RuntimeError("MCP subprocess closed stdout unexpectedly")

        response = json.loads(response_line.strip())

        if "error" in response:
            raise RuntimeError(f"MCP error: {response['error']}")

        return response.get("result", {})


# ---------------------------------------------------------------------------
# Factory
# ---------------------------------------------------------------------------

def get_mcp_client(
    binary_path: Optional[str] = None,
    env: Optional[Dict[str, str]] = None,
) -> StdioMCPClient:
    """Create and start a StdioMCPClient pointing to the tzro-mcp binary.

    Searches for the binary in:
      1. Explicit ``binary_path`` argument
      2. ``TZRO_MCP_PATH`` environment variable
      3. ``./bin/tzro-mcp`` relative to ``TZRO_DIR``
      4. ``./tzro-mcp`` in the current working directory
    """
    if binary_path is None:
        binary_path = os.environ.get("TZRO_MCP_PATH")

    if binary_path is None:
        tzro_dir = os.environ.get("TZRO_DIR", os.getcwd())
        candidates = [
            os.path.join(tzro_dir, "bin", "tzro-mcp"),
            os.path.join(tzro_dir, "tzro-mcp"),
        ]
        for candidate in candidates:
            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                binary_path = candidate
                break

    if binary_path is None:
        raise FileNotFoundError(
            "Could not locate tzro-mcp binary. Set TZRO_MCP_PATH or ensure "
            "bin/tzro-mcp is in the TZRO_DIR directory."
        )

    client = StdioMCPClient(binary_path, env=env)
    client.start()
    return client


# ---------------------------------------------------------------------------
# Task execution loop
# ---------------------------------------------------------------------------

async def execute_task_loop(
    ctx: Any,
    client: StdioMCPClient,
    task_id: str,
    poll_interval: float = 1.0,
    max_iterations: int = 300,
) -> Dict[str, Any]:
    """Poll a running task, intercept client tool requests, and resume until completion.

    This is the core execution loop for Native Plugin Mode — it bridges the
    tzro engine's client tool dispatch back to the host agent's tools.
    """
    import asyncio

    for _ in range(max_iterations):
        # Check task status
        status_res = client.call_tool("tzro_status", {"taskId": task_id})
        status_text = status_res.get("content", [{}])[0].get("text", "{}")
        status_data = json.loads(status_text)

        current_status = status_data.get("status", "unknown")

        if current_status in ("completed", "failed"):
            return status_data

        # Check for pending client tool requests
        if current_status == "waiting_for_client":
            pending_res = client.call_tool("tzro_client_tool_list", {})
            pending_text = pending_res.get("content", [{}])[0].get("text", "[]")
            pending_requests = json.loads(pending_text)

            for req in pending_requests:
                request_id = req.get("requestId")
                tool_name = req.get("toolName")
                tool_args = req.get("arguments", {})

                logger.info("Executing client tool '%s' for request %s", tool_name, request_id)

                try:
                    # Dispatch to the host agent's tools
                    result = await _dispatch_tool(ctx, tool_name, tool_args)
                    client.call_tool("tzro_client_tool_submit", {
                        "requestId": request_id,
                        "result": json.dumps(result) if not isinstance(result, str) else result,
                    })
                except Exception as e:
                    logger.error("Client tool '%s' failed: %s", tool_name, str(e))
                    client.call_tool("tzro_client_tool_submit", {
                        "requestId": request_id,
                        "result": "",
                        "error": str(e),
                    })

        await asyncio.sleep(poll_interval)

    raise TimeoutError(f"Task {task_id} did not complete within {max_iterations} poll iterations")


async def _dispatch_tool(ctx: Any, tool_name: str, arguments: Dict[str, Any]) -> Any:
    """Dispatch a tool call to the host agent context."""
    tools_source = None
    if hasattr(ctx, "agent") and hasattr(ctx.agent, "tools"):
        tools_source = ctx.agent.tools
    elif hasattr(ctx, "tools"):
        tools_source = ctx.tools

    if tools_source:
        for tool in tools_source:
            name = None
            if hasattr(tool, "name"):
                name = tool.name
            elif hasattr(tool, "schema"):
                name = tool.schema.get("name")
            elif isinstance(tool, dict):
                name = tool.get("name")

            if name == tool_name:
                if callable(tool):
                    return await tool(**arguments)
                elif hasattr(tool, "execute"):
                    return await tool.execute(**arguments)

    raise ValueError(f"Tool '{tool_name}' not found in host agent context")


# ---------------------------------------------------------------------------
# Status & Resume handlers
# ---------------------------------------------------------------------------

async def handle_status(ctx: Any, task_id: str) -> str:
    """Check task status via a fresh MCP client connection."""
    client = get_mcp_client()
    try:
        result = client.call_tool("tzro_status", {"taskId": task_id})
        text = result.get("content", [{}])[0].get("text", "{}")
        return text
    finally:
        client.stop()


async def handle_resume(ctx: Any, task_id: str) -> str:
    """Resume a paused task via a fresh MCP client connection."""
    client = get_mcp_client()
    try:
        # Register client tools first
        agent_tools = []
        tools_source = None
        if hasattr(ctx, "agent") and hasattr(ctx.agent, "tools"):
            tools_source = ctx.agent.tools
        elif hasattr(ctx, "tools"):
            tools_source = ctx.tools

        if tools_source:
            for tool in tools_source:
                schema = None
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
            client.call_tool("tzro_register_client_tools", {"tools": agent_tools})

        result = client.call_tool("tzro_resume", {"taskId": task_id})
        run_text = result.get("content", [{}])[0].get("text", "{}")
        run_data = json.loads(run_text)

        if run_data.get("status") in ("completed", "failed"):
            final_status = run_data
        else:
            final_status = await execute_task_loop(ctx, client, task_id)

        return format_synthesis_report(task_id, final_status)
    finally:
        client.stop()


# ---------------------------------------------------------------------------
# Report formatting
# ---------------------------------------------------------------------------

def format_synthesis_report(task_id: str, status_data: Dict[str, Any]) -> str:
    """Format a human-readable synthesis report from task execution results."""
    status = status_data.get("status", "unknown")
    lines = [
        f"## tzro Task Report",
        f"",
        f"**Task ID:** `{task_id}`",
        f"**Status:** {status}",
    ]

    # Include node summaries if available
    nodes = status_data.get("nodes", [])
    if nodes:
        lines.append("")
        lines.append("### Node Execution Summary")
        lines.append("")
        for node in nodes:
            node_id = node.get("nodeId", "?")
            node_status = node.get("status", "?")
            emoji = "✅" if node_status == "completed" else "❌" if node_status == "failed" else "⏳"
            lines.append(f"- {emoji} **{node_id}**: {node_status}")

    # Include terminal synthesis output
    synthesis = status_data.get("synthesis") or status_data.get("output", "")
    if synthesis:
        lines.append("")
        lines.append("### Output")
        lines.append("")
        lines.append(synthesis)

    # Include errors if any
    error = status_data.get("error", "")
    if error:
        lines.append("")
        lines.append("### Error")
        lines.append("")
        lines.append(f"```\n{error}\n```")

    return "\n".join(lines)
