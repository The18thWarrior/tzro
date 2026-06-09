# Feature: Dynamic Workflow Orchestration (formerly Reactive Agent Daemons)

## Problem & Solution

* **Context:** Current background daemons in the `proactivity` scheduler execute fixed, parameterless Go closures or post simple alerts. BackgroundAgents (Observer, Sentinel) can reason with the Local Model but cannot autonomously spawn multi-step task work to diagnose failures, pre-warm assets, or clean environments.
* **Value:** By extending the existing Workflow orchestrator with an LLM-driven dynamic mode, BackgroundAgents can spawn goal-oriented, budget-constrained Workflows that coordinate multiple child Tasks — using the existing DAG executor, checkpointing, and Edge Thought machinery. No new execution engine or abstraction required.

## Technical Design Summary

* **Core Changes:**
  * `internal/workflow/orchestrator.go`: Add dynamic orchestration mode where the Local Model decides the next child Task after each completion, alongside the existing static (pre-defined task graph) mode.
  * `internal/proactivity/scheduler.go`: Enable BackgroundAgents to spawn Workflows through the existing Proactivity Ladder and AttentionScheduler.
  * Tool registration: Add per-tool **Tool Proactivity Level** annotations for deterministic escalation gating.
* **Safety Model:**
  * Gate-and-escalate: background Workflows run at their approved Proactivity Ladder level. If a tool dispatch exceeds the ceiling, the harness deterministically suspends the Workflow and enqueues an escalation request. The LLM does not decide safety policy.
  * Tool level defaults: built-in tools hardcoded, MCP Host tools default L3, harness-forwarded tools default L1.
* **Preemption:** Foreground tasks cancel the active child Task's context. The Workflow auto-resumes from the last checkpoint when the foreground clears.
* **Completion:** Self-terminating — the orchestrator's LLM loop decides goal-met, or the runtime enforces budget exhaustion.

## Key Decisions (ADR-0027)

* "Reactive Agent Daemon" concept collapsed into existing Workflow — no new abstraction.
* Dynamic Workflows use the existing DAG executor (single-node Tasks with Activation Thresholds initially).
* Between-Task orchestrator LLM calls use BackgroundAgent's `LLMClient`, not the task executor's inference path.
* WASM scripting story dropped — served by existing Sandboxed Micro-Skills.

## References

* **PRD:** [PRD.md](../../../.scratch/dynamic-workflow-orchestration/PRD.md)
* **ADR:** [ADR-0027](../../adr/0027-dynamic-workflow-orchestration-over-reactive-daemons.md)
* **Original PRD:** [PRD.md](../../../.scratch/reactive-daemons/PRD.md) *(superseded)*
* **Log Entry:** [Log Link](../log.md#2026-06-08-1956-grill-with-docs--reactive-agent-daemons--dynamic-workflow-orchestration)
