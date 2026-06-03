# ADR-0007: Stdio-Spawned Persistent Daemon MCP Hosts

We choose to implement local **Stdio-based process spawning** as the core integration mechanism for third-party tools, orchestrating them as persistent, stateful daemon child processes configured via a unified JSON file. This eliminates the need for developers to write custom Go integrations by allowing standard off-the-shelf Node/Python MCP servers to be dropped in with zero code modifications.

## Status

Accepted

## Considered Options

- **Option A: Dynamic HTTP/SSE Web Gateways**: Requires developers to host, run, and secure external web services separately. Rejected for first-phase local developer ease.
- **Option B: On-Demand Process Spawning**: Spawns and kills processes per tool call. Rejected due to severe cold-start latency (500ms to 2.5s) per step execution.

## Consequences

- **Unified Configuration**: Developers copy-paste standard Claude/Cursor MCP configs into `tzro`'s JSON file to drop in PostgreSQL, Slack, or GitHub integrations.
- **Zero Cold-Start Lag**: Keeping standard I/O pipes open in Go goroutines reduces individual step tool call routing latency to under 10 milliseconds.
- **Local Process Management**: Core engine must handle OS process groups, process interrupts, resource leak protection, and standard I/O buffer scanning.
- **Secure Environment Inheritance**: Processes inherit the parent's environment variables and load localized `.env` files, avoiding hardcoding API keys in standard configurations.
