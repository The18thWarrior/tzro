# Plan Template Selection and Mutation

The docgen benchmark hardening (2026-07-16) revealed that the Strategic Planner produces broken graph structures when generating from scratch — multiple action nodes instead of probes, incorrect tool allowlists, missing dynamic bindings. Benchmark run 21 analysis confirmed the root cause (FM3): the 4B Local Model generates structurally inconsistent DAGs between runs for the same tasks because it plans from scratch every time.

We introduce a **Plan Template Registry** — a set of named, structural graph shapes stored as Go structs in `internal/templates/`. When the Complexity Tier routes a task to the local planner, a GBNF-constrained LLM classification selects one of 8 template categories, and the local model receives the serialized template as a starting point for mutation. The cloud planner retains full freeform graph generation for complex (T2) tasks.

## Scope

Templates apply **only to local-routed tasks**. The cloud planner is unrestricted — it generates Abstract Graphs from scratch with no template involvement. Templates are universally applied within the local path, including benchmark runs.

## Template Categories

| Template | Abstract Graph | When Selected |
|----------|-----------|---------------|
| `explore-only` | `probe` | Codebase exploration, architecture questions |
| `explore-and-write` | `probe → action(write_file)` | Documentation generation, report writing |
| `research` | `probe(sourceHint=web)` | Web research tasks |
| `research-and-write` | `probe(sourceHint=web) → action(write_file)` | Research reports |
| `data-analysis` | `action(read_file) → analyze` | Tabular data analysis tasks |
| `multi-probe-synthesis` | `probe₁ + probe₂` | Multi-source exploration |
| `codegen` | `probe → action(tzro_code)` | Code generation tasks |
| `action-chain` | `action₁ → action₂ → action₃` | Multi-step tool workflows, sequential tool dispatch |

Templates are Abstract Graphs (pre-compilation). The Kahn Compiler auto-injects Recall Nodes, semantic validators, and synthesis nodes — templates do not include these.

## Mutation Model

The local model receives the serialized template JSON and a compact mutation prompt. It has **full mutation authority** — it can add/remove nodes, change edges, modify tools, adjust instructions, and alter ProbeConfig. The template is a cognitive scaffold that reduces the reasoning burden from "design + serialize" to "edit + serialize." The existing validation pipeline (`findInvalidTools`, `CompileAndSort`, `repairGraphWithProbe`, cloud escalation via `PlanWithEscalation`) enforces structural invariants post-mutation.

The mutation prompt replaces the current ~150-line system prompt with a ~50-line prompt containing: (1) the template JSON as starting plan, (2) a compact Node Type Reference Card listing all available node types, (3) the tool inventory, (4) relevant skills, and (5) the shallow repo map.

## Considered Options

- **Full planner generation with improved prompt rules**: The planner generates graphs from scratch with better instructions. The 4B model still produces structurally broken plans for complex tasks because generating a complete graph requires both design reasoning and JSON serialization simultaneously.
- **Classification → deterministic template (no mutation)**: Router selects a fixed template with deterministic field injection only. Zero risk of broken shapes but too rigid — can't adapt graph structure to task-specific needs. The 4B model CAN produce graphs; it just needs a starting point.
- **Template + constrained mutation (instructions only)**: Model may only edit instructions and ProbeConfig.Goal. Too restrictive — tasks that need a slight structural adjustment (adding a node, changing an edge) get no adaptation.
- **GBNF classification → template selection → full mutation**: Router classifies via GBNF-constrained inference, selects template, model mutates freely. Accepted — reduces cognitive load while preserving full planning flexibility. Existing validation catches bad mutations.
- **Deterministic keyword matching for classification**: No LLM call — detect SourceHint, tool mentions, path count. Fast but brittle on novel prompts. Rejected in favor of GBNF classification.
- **Add a `freeform` escape category**: When no template fits, fall back to from-scratch planning. Rejected — the model would over-classify into freeform as the path of least resistance, defeating the purpose. Cloud fallback via `PlanWithEscalation` is the implicit escape hatch.

## Consequences

- The `planWithBackend()` function's system prompt is replaced with a template-based mutation prompt. The existing `PlanWithEscalation` validation and cloud escalation pipeline remains unchanged.
- New `internal/templates/` package holds 8 Go struct templates and a `NodeTypeReferenceCard` constant.
- GBNF classification adds one inference call to the local planning path (router model, fast).
- The cloud planner (`planWithCloud`) is completely unaffected.
- Template effectiveness requires a separate evaluation comparing template-based local plans vs. freeform local plans on real-world tasks, since benchmark evaluation criteria may constrain graph shapes independently.
- The `IntentType` field on `ExecuteOptions` (currently unused in `planWithBackend`) could feed the classifier as a hint in future iterations.
