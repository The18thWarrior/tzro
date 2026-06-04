"""Hermes Agent plugin for tzro — durable local-first DAG execution.

Registers tzro_run, tzro_status, and tzro_resume as Hermes tools,
enabling the agent to delegate complex multi-step workflows to the
local tzro engine with zero cloud round-trip overhead.

Install location: ~/.hermes/plugins/tzro/
"""

import logging
from typing import Any, Callable, Dict, Optional

from .connection import TzroExecutor
from .tools import handle_status, handle_resume

logger = logging.getLogger("hermes.tzro")

# Try importing the Hermes plugin base class; stub if unavailable
try:
    from hermes.plugin import Plugin  # type: ignore[import-not-found]
except ImportError:

    class Plugin:
        """Stub base class for compilation portability outside Hermes runtime."""

        def on_load(self, ctx: Any) -> None:
            pass

        def on_unload(self) -> None:
            pass


class TzroPlugin(Plugin):
    """Hermes plugin that exposes tzro durable execution as agent tools.

    Lifecycle:
        1. on_load: Registers tzro_run, tzro_status, tzro_resume tools
        2. Tool invocation: The agent calls tzro_run with a natural language prompt
        3. Execution: TzroExecutor compiles a DAG and runs it, dispatching
           client tools back through ctx.dispatch_tool() in-process
        4. on_unload: Cleans up any active MCP connections
    """

    def __init__(self, binary_path: Optional[str] = None):
        self.binary_path = binary_path
        self._executor: Optional[TzroExecutor] = None

    def on_load(self, ctx: Any) -> None:
        """Called by Hermes when the plugin is loaded. Registers tools."""
        logger.info("Loading tzro plugin for Hermes Agent")

        tools = self._build_tool_definitions()

        if hasattr(ctx, "register_tools"):
            ctx.register_tools(tools)
            logger.info("Registered %d tzro tools with Hermes", len(tools))
        elif hasattr(ctx, "tools") and isinstance(ctx.tools, list):
            ctx.tools.extend(tools)
            logger.info("Appended %d tzro tools to Hermes context", len(tools))
        else:
            logger.warning(
                "Could not register tools — Hermes context does not expose "
                "register_tools() or a tools list"
            )

    def on_unload(self) -> None:
        """Called by Hermes when the plugin is unloaded. Cleans up resources."""
        logger.info("Unloading tzro plugin")
        self._executor = None

    def _build_tool_definitions(self) -> list:
        """Build the tool definition dicts for Hermes registration."""
        binary_path = self.binary_path

        async def tzro_run(prompt: str, ctx: Any = None) -> str:
            """Plan, compile, and execute a durable local-first DAG workflow.

            Compiles the prompt into a topologically-sorted DAG, executes it
            with checkpointing, and dispatches client tool calls directly
            through the Hermes runtime — zero cloud round-trips.
            """
            executor = TzroExecutor(binary_path=binary_path)
            result = await executor.execute(prompt, ctx)
            return result["report"]

        async def tzro_status(task_id: str, ctx: Any = None) -> str:
            """Check the execution status, node states, and outcomes of a task."""
            return await handle_status(task_id)

        async def tzro_resume(task_id: str, ctx: Any = None) -> str:
            """Resume execution of a paused or interrupted task."""
            return await handle_resume(ctx, task_id)

        return [
            {
                "name": "tzro_run",
                "description": (
                    "Plan, compile, and execute a durable local-first DAG workflow. "
                    "Compiles the natural language prompt into a topologically-sorted "
                    "DAG and executes it with checkpointing. Client tool calls are "
                    "dispatched directly in-process, bypassing cloud round-trips."
                ),
                "parameters": {
                    "type": "object",
                    "properties": {
                        "prompt": {
                            "type": "string",
                            "description": "The natural language task to execute",
                        }
                    },
                    "required": ["prompt"],
                },
                "execute": tzro_run,
            },
            {
                "name": "tzro_status",
                "description": (
                    "Check the execution status, node states, and outcomes "
                    "of a specific tzro task by its ID."
                ),
                "parameters": {
                    "type": "object",
                    "properties": {
                        "task_id": {
                            "type": "string",
                            "description": "The task ID to check",
                        }
                    },
                    "required": ["task_id"],
                },
                "execute": tzro_status,
            },
            {
                "name": "tzro_resume",
                "description": (
                    "Resume execution of a paused or interrupted tzro task."
                ),
                "parameters": {
                    "type": "object",
                    "properties": {
                        "task_id": {
                            "type": "string",
                            "description": "The task ID to resume",
                        }
                    },
                    "required": ["task_id"],
                },
                "execute": tzro_resume,
            },
        ]


def register(ctx: Any, binary_path: Optional[str] = None) -> TzroPlugin:
    """Convenience function to create, load, and return the tzro plugin.

    Usage in Hermes plugin config or startup script:
        from tzro import register
        plugin = register(ctx)
    """
    plugin = TzroPlugin(binary_path=binary_path)
    plugin.on_load(ctx)
    return plugin
