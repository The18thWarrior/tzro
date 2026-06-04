"""tzro plugin for NousResearch Hermes Agent.

Provides durable local-first DAG execution via the tzro engine,
dispatching client tools directly through the Hermes runtime context.
"""

from .plugin import TzroPlugin, register

__all__ = ["TzroPlugin", "register"]
