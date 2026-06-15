# Background Agent Abstraction and Observer Refactor

We introduce a two-level agent abstraction — a minimal `Agent` interface and a `BackgroundAgent` base struct — and refactor the existing Observer Agent to embed it. This establishes a shared hosting pattern for long-lived autonomous processes inside the tzro daemon.

The `Agent` interface is intentionally minimal: `Name() string`, `Start(ctx context.Context)`, `Stop()`. It does not prescribe trigger mechanisms, data sources, or output channels — those are concerns of concrete agent types.

`BackgroundAgent` is the first concrete base. It provides shared infrastructure for daemon-resident agents that run on their own trigger schedule: LLMClient wiring, TelemetryManager subscription, memory/KG access, and Durable Notification output. The Observer Agent and the new Sentinel Agent (ADR-0023) are the first two Background Agents.

The Observer is refactored from a standalone struct into one that embeds `BackgroundAgent`, preserving its existing debounce-based trigger loop and post-execution reflection logic. This refactor also adds deterministic operational checks — stale workflow detection, escalation trend alerts, micro-skill staleness — to the Observer's evaluation pipeline as a pre-pass before the LLM-driven reflection.

We chose the agent hierarchy over three alternatives: (1) extending the Observer with a heartbeat timer (conflates reactive and proactive temporal profiles in one struct), (2) creating the Sentinel as a standalone copy of the Observer pattern (duplicates all infrastructure wiring), (3) defining the interface but wrapping the Observer with a thin adapter instead of refactoring it (leaves two implementation patterns for the same abstraction).

## Considered Options

- **Extend Observer with heartbeat mode**: Add a timer to the existing Observer's select loop. Rejected — the Observer's debounce logic is tuned for event aggregation; mixing periodic proactive evaluation into the same loop conflates two fundamentally different temporal profiles and makes testing harder.
- **Duplicate infrastructure for Sentinel**: Copy the Observer's LLMClient wiring, telemetry subscription, and notification pipeline into a new standalone struct. Rejected — identical dependency graphs indicate a missing abstraction.
- **Thin wrapper on Observer, full base for Sentinel**: Define the Agent interface and BackgroundAgent base, build the Sentinel on it properly, but leave the Observer as-is with a trivial `Name()`/`Start()`/`Stop()` wrapper. Rejected — two implementation patterns for the same abstraction is tech debt with zero justification when both agents are being actively modified.
