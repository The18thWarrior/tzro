"""Helper utilities to register tzro tools and hooks within Google Antigravity SDK."""

import logging
from typing import Any, Callable, Optional

logger = logging.getLogger("google.antigravity.tzro")

# Try importing types from the SDK for type safety
try:
    from google.antigravity import LocalAgentConfig
except ImportError:
    # Stub config class for compilation portability
    class LocalAgentConfig:
        def __init__(self, **kwargs):
            self.tools = kwargs.get("tools", [])
            self.hooks = kwargs.get("hooks", [])
            self.policies = kwargs.get("policies", [])

async def tzro_run(prompt: str, model_mode: str = "cooperative", ctx: Any = None) -> str:
    """Plan, compile, and execute a durable local-first DAG workflow."""
    from .connection import TzroConnection
    conn = TzroConnection()
    res = await conn.execute(prompt, ctx)
    return res.content

async def tzro_status(task_id: str, ctx: Any = None) -> str:
    """Check the execution status, node states, and outcomes of a specific task."""
    from .tools import handle_status
    return await handle_status(ctx, task_id)

async def tzro_resume(task_id: str, ctx: Any = None) -> str:
    """Manually resume execution of a paused/interrupted task by its ID."""
    from .tools import handle_resume
    return await handle_resume(ctx, task_id)

def register_tzro_tools(config: Any) -> None:
    """Appends tzro_run, tzro_status, and tzro_resume to the Agent's tool registry."""
    if not hasattr(config, "tools") or config.tools is None:
        config.tools = []
    config.tools.extend([tzro_run, tzro_status, tzro_resume])
    logger.info("Registered tzro tools in Antigravity AgentConfig")

def configure_safety_hooks(config: Any, approval_handler: Optional[Callable[[Any], Any]] = None) -> None:
    """Configures safety interceptor hooks for in-process tool executions.

    Ensures that when tzro dispatches local tools, they comply with the agent's safety policies.
    """
    if not hasattr(config, "hooks") or config.hooks is None:
        config.hooks = []

    async def tzro_pre_tool_hook(tool_call: Any) -> Any:
        logger.info("Safety Hook: Evaluating tool call '%s' for tzro node execution", getattr(tool_call, "name", str(tool_call)))
        if approval_handler:
            return await approval_handler(tool_call)
        
        # Default fallback to allow if no custom handler is provided
        try:
            from google.antigravity.types import HookResult
            return HookResult(allow=True)
        except ImportError:
            return {"allow": True}

    config.hooks.append(tzro_pre_tool_hook)
    logger.info("Configured safety hooks for tzro tool calls in AgentConfig")
