# Unified daemon-mediated state mutations

We enforce a strict client-server daemon-mediated model for all CLI and TUI state modifications. The developer CLI (`tzro`) and TUI client will communicate exclusively over HTTP/REST and SSE to the running `tzro` daemon (`tzrod`) for any operations that mutate the system state (Tasks, Workflows, Memories, and the Relational Knowledge Graph). 

Direct, out-of-band writes from the CLI binary to the SQLite database file (`tzro.db`) are strictly prohibited, though read-only offline inspection is permitted when the daemon is stopped.

## Key Design Rules

1. **Telemetry & StreamBus Integrity**: Every state change (e.g. creating a task, adding memory, running workflows) must publish corresponding event logs over the **StreamBus** (`stream.GlobalBus`). This guarantees that all developer actions are fully auditable, color-streamed in real-time, and synchronized on open GUI/TUI dashboards.
2. **Observer Agent Visibility**: The **Observer Agent** must be able to reflection-audit all trajectories, manual interventions, and behavior changes. Because the Observer loop listens exclusively to in-memory Go channels (`observer.ObserverChan`) managed by the daemon process, all writes must flow through the daemon's REST endpoints to ensure proper channel dispatch and micro-skill synthesis.
3. **Concurrency & Lock Safeguards**: Allowing multi-process concurrent writes to the same local SQLite database (`tzro.db`) invites transaction blockages and `database is locked` runtime failures, especially during parallel Kahn step checkpoints. Mediating all writes through the single `tzrod` daemon process eliminates writer contention entirely.
4. **Offline Read-Only Inspection**: If the `tzrod` daemon is stopped, the CLI/TUI can run in offline mode (`--offline`), but it is restricted to **read-only operations** (e.g., listing past tasks, viewing synthesized micro-skills, and inspecting memories). Any attempt to write or mutate state in offline mode will gracefully exit with instructions to start the daemon.

## Considered Options

- **Multi-Process SQLite Concurrent Writes**: Allow both the daemon and the CLI client to execute direct transactional writes to SQLite concurrently. Rejected — bypasses the StreamBus event bus, blinds the Observer Agent from seeing the manual writes, and risks write lock failures under parallel execution threads.
- **REST-Only Client (No Offline Mode)**: Restrict the CLI/TUI entirely to connected mode; if the daemon is offline, all commands fail. Rejected — developers need the flexibility to inspect their memory databases, micro-skill SOPs, and past task histories offline without booting up the full daemon and sidecar processes.
- **Unified Daemon-Mediated mutations (Chosen)**: Support offline read-only local database inspection, but direct all state mutations through the active `tzrod` server daemon APIs, ensuring absolute event tracking and runtime safety.
