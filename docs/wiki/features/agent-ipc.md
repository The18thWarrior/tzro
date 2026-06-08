# Feature: Agent Inter-Process Communication (IPC)

## Problem & Solution

* **Context:** The `StreamBus` is limited to fire-and-forget telemetry streams. Hosted agents cannot exchange messages, issue requests to other agents, or await sub-task results, which limits the engine to running single monolithic graphs.
* **Value:** The Agent Message Bus (AMB) implements bidirectional IPC, enabling modular agent collaboration. A Coding Agent can delegate web search queries to the Web Search Agent, yield its own system resources, and resume when the result returns.

## Technical Design Summary

* **Core Modules:**
  * `internal/stream/bus.go`: Build the core `AgentMessageBus` routing mechanism.
  * `internal/stream/registry.go`: Implement the mailbox and identity registry.
  * `internal/stream/socket.go`: Add Unix domain socket bindings for external Python/JS-based agents.
* **Data Models / APIs:**
  * The `Envelope` schema specifying message routing parameters (`ID`, `CorrelationID`, `Sender`, `Recipient`, `Payload`, `Timestamp`).
  * Registry API contracts for agent registration.

## References

* **PRD:** [PRD.md](../../../.scratch/agent-ipc/PRD.md)
* **Log Entry:** [Log Link](../log.md#2026-06-08-1915-document--prd-agent-inter-process-communication-ipc)
