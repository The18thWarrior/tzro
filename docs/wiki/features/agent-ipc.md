# Feature: Agent Inter-Process Communication (IPC)

**Status:** Shelved

## Problem & Solution

* **Context:** The `StreamBus` is limited to fire-and-forget telemetry streams. Hosted agents cannot exchange messages, issue requests to other agents, or await sub-task results, which limits the engine to running single monolithic graphs.
* **Original Value Proposition:** The Agent Message Bus (AMB) would implement bidirectional IPC, enabling modular agent collaboration.

## Shelved Rationale

Design review (2026-06-08) revealed that the motivating scenario — named agents (e.g., "Coding Agent", "Web Research Agent") delegating work to each other via structured IPC — is an artifact of the ReAct/monolithic-agent-loop paradigm. tzro's DAG-first architecture already covers every identified coordination need:

| Need | Existing Mechanism |
|---|---|
| Inter-step data flow | DAG edges with variable binding |
| Dynamic mid-execution adaptation | Edge Thoughts + Activation Thresholds |
| External tool integration | MCP Host (stdio tool servers) |
| Background daemon coordination | Shared persistent state (memory, KG, notifications) |
| Real-time observability | StreamBus (fire-and-forget telemetry) |

No concrete use case survived scrutiny. If a genuine need emerges (e.g., cross-task coordination not solvable via shared state), the feature should be re-evaluated with that specific scenario as the design driver.

## References

* **ADR:** [ADR-0026](../../../docs/adr/0026-no-agent-ipc-bus.md)
* **PRD:** [PRD.md](../../../.scratch/agent-ipc/PRD.md)
* **Log Entry:** [Log Link](../log.md#2026-06-08-1915-document--prd-agent-inter-process-communication-ipc)
