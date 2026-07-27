# Structured Execution Envelope for MCP Task Output

When compiled DAGs complete execution, the executor now assembles a deterministic JSON **Execution Envelope** wrapping the synthesis text with structured metadata (tools used, files read/modified, node counts, duration). This envelope is persisted on a new `StructuredOutput` field on `NodeState` and hoisted to a top-level `result` key in all MCP response surfaces (`tzro_run`, `tzro_status`, resource URIs).

## Considered Options

### Assembly Location
- **MCP response-level** — Transform at the presentation layer. Rejected: would require duplicating assembly logic across three response surfaces (tzro_run, tzro_status, resource URI) plus CLI.
- **Storage-level (chosen)** — The executor assembles the envelope post-synthesis and persists it. Every consumer gets the structured shape for free.

### Storage Surface
- **Replace `Output` or `RawOutput`** — Rejected: breaks existing consumers (CLI display, Response Resolver interpolation, Observer Agent reflection).
- **New `StructuredOutput` field (chosen)** — Additive. `Output` (display-formatted) and `RawOutput` (interpolation source) stay untouched. `StructuredOutput` is `omitempty` — only present on nodes that produce an envelope.

### File Path Collection
- **Persistent dispatch table** — Rejected: unnecessary long-term storage for data only consumed once at task completion.
- **In-memory accumulator (chosen)** — Per-task `[]ToolDispatch` slice on the `ExecutionEngine`, populated at tool call sites (action node dispatch and probe thought chain loop). Scanned for file-related tools (`read_file`, `write_file`, `peek_file`, `search_files`) during envelope assembly. GC'd after task completion.

### Graphs Without `terminal_synthesis`
Probe-only graphs (where the Kahn Compiler skips synthesis injection) still get an Execution Envelope. The `synthesis` field is sourced from the effective terminal node — the last completed node of type `probe`, `recall`, or `synthesis`.

## Consequences

- `NodeState` gains a third output field (`StructuredOutput`) alongside `Output` and `RawOutput`. This is intentional: each serves a different consumer (display, interpolation, machine-parseable structure).
- The executor's `ExecuteGraph` method gains a `map[string][]ToolDispatch` accumulator and a post-completion envelope assembly step.
- Schema migration: `ALTER TABLE node_states ADD COLUMN structured_output TEXT DEFAULT ''`.
- All three MCP response surfaces extract and hoist the envelope to a `result` key, giving consuming agents first-class structured access without digging through node arrays.
