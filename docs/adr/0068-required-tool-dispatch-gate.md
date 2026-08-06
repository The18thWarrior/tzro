# Required Tool Dispatch Gate for Analyze Nodes

Analyze Nodes can prematurely signal synthesis readiness after schema introspection (`introspect_cache`) without ever querying actual data (`sql_cached_data`). The Synthesis Validation Gate evaluates model confidence ("do I have enough information?") but has no ground-truth check for whether a data query was executed. In Benchmark Run 17, this caused 2 of 6 failures — `lead_count_by_country` and `lead_source_by_owner` both synthesized from column metadata without running a single SQL query.

We add a `RequiredToolDispatch` field to `ProbeConfig` — a string slice of tool names that must appear in the Thought Chain's dispatch history before the Synthesis Validation Gate is allowed to accept synthesis. The Kahn Compiler auto-populates this with `["sql_cached_data"]` for `type: "analyze"` nodes. This is a deterministic structural gate, not an LLM judgment call.

## Considered Options

- **LLM-evaluated synthesis quality gate**: Rejected — the 4B model cannot reliably distinguish "I have schema metadata" from "I have query results." The failure mode is precisely that the model *believes* it has enough information.
- **Minimum step count**: Rejected — the number of steps doesn't guarantee a data query was dispatched. A probe could run 10 `introspect_cache` calls and still never query data.

## Consequences

- Analyze Nodes will run at least one additional Thought Chain step (the data query) before synthesis. Marginal latency increase (~5-10s per task).
- If `sql_cached_data` fails (e.g., malformed SQL), the probe will exhaust its step budget trying to query rather than synthesizing early. This is the correct behavior — failing with "could not query data" is better than synthesizing without data.
