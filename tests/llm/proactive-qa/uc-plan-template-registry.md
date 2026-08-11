# Use Case: Plan Template Registry

**Actor**: Developer running tasks through the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer submits a task prompt and expects the engine to select an appropriate structural graph template from the registry instead of generating a DAG from scratch. The template classifier matches the prompt to one of seven canonical categories (explore-only, docgen, research, data-analysis, multi-probe-synthesis, codegen, action-chain), and the selected template provides a pre-built abstract graph that the compiler mutates for the specific task. This avoids the local model needing to construct complex graph topologies from zero.

## Preconditions

- The `tzro` daemon is running with the local model sidecar active.
- The plan template registry is initialized with all seven canonical templates.
- The GBNF classification engine is available for template selection.

## Success Criteria

- [ ] The template registry contains exactly seven canonical categories: explore-only, docgen, research, data-analysis, multi-probe-synthesis, codegen, and action-chain.
- [ ] Each template is a valid abstract graph with correctly typed nodes (probe, action, analyze) and edges.
- [ ] The template classifier uses GBNF-constrained inference to force-select exactly one category from the enum.
- [ ] Selected templates have their node instructions and goals mutated to match the user's specific prompt.
- [ ] The Kahn Compiler accepts template-produced graphs and correctly injects recall nodes, semantic validators, and synthesis nodes.
- [ ] Templates with multiple nodes (e.g., research: explore → write) produce correct topological ordering.
- [ ] The reference card for each template documents its structure for observability.
- [ ] When classification confidence is low, the system falls back to a default template rather than failing.

## Edge Cases to Probe

- Ambiguous prompt that could match both "research" and "data-analysis" — verify deterministic classification.
- Very short prompt (3 words) — verify the classifier still selects a valid template.
- Prompt explicitly naming a tool (e.g., "use web_search") — verify the selected template includes that tool in allowedTools.
- Template mutation producing an empty instructions field — verify fallback to the original template instructions.

## Anti-Patterns to Watch For

- [ ] The classifier routes to the cloud model instead of using local GBNF-constrained inference.
- [ ] Template selection falls through to from-scratch DAG generation without logging a reason.
- [ ] Template graphs contain cycles that the Kahn Compiler cannot sort.
- [ ] Node instructions are overwritten entirely instead of being mutated, losing the structural guidance.
- [ ] The registry is loaded from disk on every classification instead of being held in memory.
