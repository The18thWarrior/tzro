# Dynamic Workflow Orchestration Over Reactive Daemons

We evaluated and rejected building a new `ReactiveDaemon` abstraction. The motivating use case — BackgroundAgents autonomously running multi-step LLM-guided diagnostic loops with tool access — is served by extending the existing Workflow orchestrator with a dynamic, LLM-driven orchestration mode.

## Status

Accepted.

## Considered Options

- **New `ReactiveDaemon` interface extending `Daemon`.** A third abstraction alongside `Daemon` (stateless event handlers) and `BackgroundAgent` (permanent singleton agents). Rejected because the functional requirement — goal-oriented, multi-task coordination with LLM reasoning — is the definition of a Workflow.
- **Spawned BackgroundAgent subtype.** A short-lived BackgroundAgent with a goal and completion condition. Rejected because BackgroundAgents have a `Start`/`Stop` lifecycle for permanent processes, not `Submit`/`Complete` for goal-bounded work. What was described is a Task, not an Agent.
- **Novel ReAct loop in the `agent` package.** A new execution engine for iterative tool-calling loops. Rejected because the existing DAG executor with Edge Thoughts and Activation Thresholds already provides checkpointed, iterative tool execution. A second execution engine adds maintenance burden for no functional gain.
- **Extend Workflow orchestrator with dynamic LLM-driven mode (chosen).** The orchestrator gains a `dynamic` mode where the Local Model decides the next child Task after each completion, rather than following a pre-defined task graph. BackgroundAgents spawn Workflows through the Proactivity Ladder. No new abstractions needed.

## Key Design Decisions

- **Proactivity gating uses gate-and-escalate.** A background Workflow runs at its approved Proactivity Ladder level. If a child Task's tool call exceeds that level, the harness deterministically suspends the Workflow and enqueues an escalation request. The LLM does not decide safety policy.
- **Tool Proactivity Levels are per-tool annotations.** Each tool declares its Proactivity Ladder tier at registration. Defaults: built-in tools are hardcoded, MCP Host tools default to L3, harness-forwarded tools default to L1.
- **Preempted Workflows auto-resume.** Foreground preemption cancels the active child Task's context but leaves the Workflow in `running` status. When the foreground clears, the Workflow resumes from the last checkpoint.
- **Between-Task LLM calls use BackgroundAgent's LLMClient.** The orchestrator's routing decisions are lightweight Local Model calls, not full DAG executor inference.
- **WASM scripting for daemon logic is dropped.** The use case is already served by Sandboxed Micro-Skills as tools within Workflow Tasks.

## Consequences

- The **Workflow** glossary definition is broadened to cover both user-spawned and system-spawned orchestrations of any duration.
- The `internal/workflow/orchestrator.go` gains a dynamic orchestration mode alongside the existing static mode.
- BackgroundAgents gain the ability to spawn Workflows via the `AttentionScheduler` and Proactivity Ladder.
- The "Reactive Agent Daemons" PRD is superseded. No `ReactiveDaemon` interface is created.
- A new **Tool Proactivity Level** concept is added to tool registration, enabling deterministic escalation gating.
- The existing `Daemon` interface (stateless event handlers) remains unchanged for simple, deterministic background reactions.
