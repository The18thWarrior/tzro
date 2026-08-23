# ADR-0088: DAG Parity Optimization — Immutable Goal Injection, Domain-Aware Compaction, Terminal Sinks, and Fast-Path Short-Circuiting

## Status
Accepted

## Context
In model-parity benchmark testing on a 35B parameter local model (`qwen3.6-35b-a3b-nvfp4`), the DAG execution model achieved an **18.2x token reduction** (506k tokens vs 9.22M tokens in ReAct) and **80% fewer tool calls**. However, quality regressions occurred in broad documentation tasks (`comprehensive_readme`, `inference_module_docs`) and unstructured web research. Furthermore, simple single-pass queries (e.g., CSV row counts) incurred a ~2-minute pipeline coordination overhead compared to ReAct's ~28s direct response.

Root-cause analysis identified four architectural seams:
1. **Prompt Dilution in Template Mutation**: When the Strategic Planner mutates abstract plan templates, fine-grained constraints (rounding, specific headings, error handling details) were lost from node `instructions`. Downstream synthesis nodes never saw the original user prompt.
2. **Over-Pruning in Probe Compaction**: Generic text summarization stripped out exact AST signatures, struct definitions, and quantitative metric tables.
3. **Missing Terminal Artifact Sinks**: In documentation generation tasks, synthesis markdown generated in memory was not deterministically bound to a `write_file` action node on disk.
4. **Latency Floor on Trivial Tasks**: All tasks traversed multi-stage classification, template mutation, and pre-index scanning even when a single tool action was sufficient.

## Decisions

1. **Immutable Goal Prompt Injection**:
   - The verbatim user prompt (`Task.GoalPrompt`) is preserved at task intake and injected immutably into the execution context of all downstream Synthesis, Analyze, and Codegen nodes under a dedicated `## Primary User Specification` prompt section. Node-level `instructions` serve only as local step guidance and cannot override the primary specification.

2. **Domain-Aware Structured Compaction**:
   - Compaction within Probe and Recall nodes is made type-aware:
     - **Code Exploration**: Preserves exported AST signatures, interface definitions, and file paths verbatim as **Code Skeletons**.
     - **Web Research**: Extracts and preserves verbatim **Evidence Cards** with exact URLs, dates, and quantitative quotes.
     - **Data Analysis**: Preserves column schemas and aggregate query results verbatim as **Analytical Evidence**.

3. **Compiler-Injected Terminal Tool Sinks**:
   - For all `docgen` tasks and tasks specifying an output file target, the **Kahn Compiler** automatically appends a deterministic `write_file` **Action Node** at the graph boundary, dynamically binding its content to `{{nodes.terminal_synthesis.output}}`.

4. **Fast-Path T0 Short-Circuit**:
   - When the intake classifier evaluates a task as **Complexity Tier T0** (single deterministic tool or simple calculation), the engine bypasses template mutation and compiles a direct single-node execution graph, reducing execution latency from ~150s to <30s.

## Consequences

### Positive
- **Prompt Fidelity**: Synthesis nodes retain 100% of user prompt constraints regardless of graph complexity.
- **Evidence Grounding**: Prevents loss of function signatures and exact metrics, closing the qualitative gap with unconstrained ReAct without token context bloat.
- **Deterministic File Generation**: Guarantees that documentation tasks write files to disk.
- **Sub-30s T0 Latency**: Eliminates the 2-minute pipeline startup penalty for simple CRM and single-file tasks.

### Negative
- Synthesis prompt size increases slightly (~200–500 tokens) due to verbatim goal prompt injection.
- The compiler requires explicit category and output path detection to inject terminal sinks correctly.
