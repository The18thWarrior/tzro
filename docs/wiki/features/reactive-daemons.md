# Feature: Reactive Agent Daemons

## Problem & Solution

* **Context:** Current background daemons in the `proactivity` scheduler execute fixed, parameterless Go closures or post simple alerts. They lack the autonomous reasoning capacity to run their own LLM loops or call tools reactively in response to environment events.
* **Value:** Reactive Agent Daemons enable the background scheduler to spin up long-lived, LLM-guided processes that observe system events (e.g., repeating tool failures) and run automated, multi-step diagnostics using mock and MCP tools before presenting suggestions to the user.

## Technical Design Summary

* **Core Modules:**
  * `internal/proactivity/daemons.go`: Introduce the `ReactiveDaemon` contract.
  * `internal/proactivity/scheduler.go`: Implement the asynchronous execution harness supporting preemption-aware background contexts.
  * `internal/agent/agent.go`: Integrate tool calling and model invocation hooks for hosted agents.
* **Data Models / APIs:**
  * Defines the `ReactiveDaemon` contract and preemption event interface.
  * Outlines resource budget schemas (tokens/hour, tools/hour) inside daemon registration configs.

## References

* **PRD:** [PRD.md](../../../.scratch/reactive-daemons/PRD.md)
* **Log Entry:** [Log Link](../log.md#2026-06-08-1915-document--prd-reactive-agent-daemons)
