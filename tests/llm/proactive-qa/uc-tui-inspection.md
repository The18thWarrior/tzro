# Use Case: TUI Client Local DB and Server Inspection

**Actor**: Developer running tzro locally on their system.
**Route**: Terminal/TUI (Offline SQLite DB Inspection & Server connection)
**Backend**: http://localhost:36888/api/config
**Priority**: P1

---

## Intent

A developer wants to view, query, and inspect the state of local SQLite databases and monitor connections to the active background server daemon using the rich styled terminal UI client, facilitating local-first debugging of agents.

## Preconditions

- The `tzro` CLI binary is built and available in the current working directory or system PATH.
- The `tzro.db` database exists in the workspace.
- The developer has shell terminal access.

## Success Criteria

- [ ] Developer sees the Bubble Tea interactive dashboard launch on executing `tzro`.
- [ ] Developer can view active tasks, memory hops, and connected nodes in tabular views.
- [ ] Developer can search local graph memories through query filtering.
- [ ] Developer can switch to direct SQLite DB inspection mode by running `tzro --offline`.
- [ ] Developer sees a warning output on stderr if the server is unreachable, falling back to read-only DB inspection.
- [ ] Developer can scroll, select, and filter task items interactively using keyboard navigation keys.
- [ ] Developer can press `ctrl+c` or `q` to gracefully exit the TUI dashboard back to their terminal.

## Edge Cases to Probe

- Launching TUI with a non-existent or corrupted SQLite database path passed via `--db`.
- Launching TUI when the background server daemon is offline vs when it is fully online.
- Rapidly switching views/panels using hotkeys while large database transactions are occurring.
- Resizing the terminal screen to extremely small sizes (e.g., 20x10) to verify layout adaptability.

## Anti-Patterns to Watch For

- [ ] Terminal screen crashes with raw stack traces or Go panic messages.
- [ ] Text overflows or overlaps, rendering tabular columns unreadable in standard terminal sizes.
- [ ] UI hangs or freezes indefinitely during network timeouts or SQLite locked states.
- [ ] Raw JSON unmarshaling errors printed directly to the TUI display.
