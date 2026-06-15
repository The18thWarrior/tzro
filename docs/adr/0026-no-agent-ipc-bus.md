# No Agent IPC Message Bus

We evaluated and rejected building a dedicated Agent Message Bus (AMB) for bidirectional inter-agent communication. The motivating scenario — a "Coding Agent" delegating work to a "Web Research Agent" via structured IPC — is an artifact of the ReAct/monolithic-agent-loop paradigm, where a single LLM holds full task context and makes autonomous routing decisions. tzro's DAG-first architecture already eliminates this need through existing mechanisms:

- **Inter-step data flow** → DAG edges with variable binding (`{{nodes.X.output}}`)
- **Dynamic mid-execution adaptation** → Edge Thoughts and Activation Thresholds
- **External tool integration** → MCP Host (stdio-based tool servers)
- **Background daemon coordination** → Shared persistent state (memory store, knowledge graph, notification table) — the Observer and Sentinel communicate indirectly through durable artifacts, which is more resilient than direct messaging
- **Real-time observability** → StreamBus (fire-and-forget telemetry fan-out)

## Considered Options

- **Full Agent Message Bus** with addressable mailboxes, correlation-based request-response, and Unix Domain Socket transport for external processes. Rejected because no concrete use case survived scrutiny — every scenario mapped to an existing mechanism.
- **Lightweight cross-task signaling** (one running Task notifying another). Deferred as a narrower future feature if a real use case emerges.

## Consequences

- The glossary term **Agent** retains its current narrow definition: an in-process autonomous process with a `Start`/`Stop` lifecycle. No "Participant" abstraction is needed.
- The `internal/stream/` package remains focused on telemetry. No `internal/ipc/` package.
- If a genuine IPC need emerges (e.g., cross-task coordination that can't use shared state), this decision should be revisited with the concrete scenario as the design driver — not a theoretical multi-agent framework.
