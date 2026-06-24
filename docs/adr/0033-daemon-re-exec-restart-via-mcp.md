# Daemon re-exec restart via MCP tool and REST endpoint

We expose a `POST /api/restart` endpoint on `tzrod` and a corresponding `tzro_restart` MCP tool (plus `tzro restart` CLI command) that triggers an immediate in-place process restart using `syscall.Exec`. The daemon replaces itself with a fresh copy of the same binary (`os.Args[0]`), preserving the PID and pidlock. No drain phase — in-flight tasks are interrupted and recovered on boot via `workflow.RecoverInterruptedWorkflows`.

We chose `syscall.Exec` over signal-driven stop + external relaunch (shifts restart responsibility to the environment, which is fragile) and graceful drain + child spawn (unbounded wait time if nodes are blocked on human approval gates, plus pidlock race window between old and new processes). The re-exec pattern also preserves the inference sidecar — because the PID changes but the llama-server child process stays alive (orphaned to PID 1), the new `tzrod` process adopts it via the existing port file health probe in `LocalModelManager.Start`, avoiding a full model reload.

The MCP tool is classified as L3 (Reversible Action) on the Proactivity Ladder — the action kills in-flight work but all state is durable in SQLite and recoverable on boot, with no external side effects leaving the system boundary.

## Considered Options

- **`syscall.Exec` re-exec (Chosen)**: Same PID, pidlock survives, sidecar adopted, instant restart. Standard Go daemon pattern (Caddy, Consul).
- **Signal-driven stop + external relaunch**: REST endpoint calls `os.Exit`; external process manager (launchd, systemd, the MCP harness) restarts. Rejected — requires environment-specific restart configuration and shifts responsibility to the harness.
- **Graceful drain + `os/exec` child spawn**: Drain in-flight tasks, spawn new `tzrod` child, then exit. Rejected — unbounded drain time (approval gates), pidlock race between old/new processes.
