# Plan Template Selection and Mutation

The docgen benchmark hardening (2026-07-16) revealed that the Strategic Planner produces broken graph structures for documentation tasks when generating from scratch — multiple action nodes instead of probes, incorrect tool allowlists, missing dynamic bindings. Hardcoded DAG plans were introduced as a benchmark workaround but bypass the planner entirely, creating maintenance debt and coupling the planning pipeline to specific task IDs.

We introduce a **Plan Template Registry** where the router classifies the task category (using the existing `CategoryDocgen`, `CategoryCodegen`, etc. taxonomy) and selects a structural starter template. The planner then receives this template and makes **targeted mutations** (adjusting instructions, allowedTools, ProbeConfig fields, adding/removing nodes) rather than generating a complete graph from scratch. This follows the "editing is cheaper than creating" principle already established by the Semantic Validator (parameter extraction into pre-structured schemas) and Edge Thoughts (DAG mutation rather than DAG generation).

## Considered Options

- **Full planner generation with improved prompt rules**: The planner generates graphs from scratch with better docgen-specific instructions. Simpler but the local 4B model still produces structurally broken plans ~30% of the time for complex tasks. The improved prompt rules help but don't eliminate the structural failure mode.
- **Classification → deterministic template (no mutation)**: Router selects a fixed template with no planner involvement. Zero risk of broken shapes but too rigid — can't adapt node instructions, tool lists, or probe config to the specific task.
- **Template as few-shot examples**: All templates included in the planner's system prompt as examples. One inference call but less constrained — the planner might still deviate into broken shapes.
- **Router classification → template selection → planner mutation**: Router classifies and selects the template deterministically. Planner receives the template and a mutation-focused prompt to customize it for the specific task. Accepted — bounded mutation gives structural safety with task-specific flexibility.

## Consequences

- The `Plan()` function's hardcoded task-ID matching and cloud planning override are removed. Template selection is driven by task category, not task ID strings.
- Planner system prompt shifts from "generate a complete execution graph" to "here is the starter graph — adjust it for this specific request." This constrains the planner's output space significantly, improving local model reliability.
- New `internal/templates/` package (or equivalent) holds the template registry. Initial templates: `explore-only` (single probe), `explore-and-write` (probe → write_file with dynamic bindings), `multi-probe-synthesis` (multiple probes → recall → synthesis).
- The `DirectSynthesis` ProbeConfig option (see below) can be set by the planner during template mutation when it determines the task can be satisfied with a single-shot inference against pre-compiled context.
- The existing SCT compiler validation pipeline applies to the mutated graph — no new validation needed.
