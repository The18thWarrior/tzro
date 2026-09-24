# Use Case: Execute System 1 Graph Calls

**Actor**: A cloud-based System 2 planner agent that compiles a declarative DAG of tool, decision, and extraction nodes and submits it to tzro for local execution.
**Route**: CLI `tzro execute [graph.json | -]`
**Backend**: `pkg/executor` — Engine, BuiltinDispatcher, pointer resolution, yield protocol
**Priority**: P0

---

## Intent

The planner agent needs to run a multi-step local workflow — combining shell commands, probe lookups, file skeletonization, decision queries, and entity extraction — without making round-trip cloud calls for each step. The agent compiles a graph of nodes with explicit dependencies and submits it to the executor, which resolves the DAG in topological order with bounded concurrency, wires data between nodes via `$ref` pointers, and returns a structured result or a yield envelope when confidence thresholds are not met.

## Preconditions

- `tzro` binary is built and available on `PATH`
- A valid graph JSON file exists with version, task_id, nodes, and optional returns
- For tool nodes: workspace root is accessible and writable
- For decision nodes: a Decider implementation is available (Laya daemon or stub)
- For extract nodes: an Extractor implementation is available (GLiNER worker or stub)

## Success Criteria

- [ ] Graph with a single tool node executes the tool and returns status `"completed"` with stdout/stderr/exit_code
- [ ] Graph with chained dependencies executes nodes in correct topological order — downstream nodes see upstream outputs
- [ ] `$ref` pointers in node args resolve to upstream node stdout, stderr, exit_code, status, or data fields
- [ ] Graph with `max_concurrency` limit never runs more than N nodes simultaneously
- [ ] Decision node with confidence above `min_confidence` proceeds with status `"completed"`
- [ ] Decision node with confidence below `min_confidence` produces a `YieldEnvelope` with reason `"acceptance_criteria_unmet"`
- [ ] Group node expands items against a template and collects results from all sub-evaluations
- [ ] Failed node with `allow_failed_dependencies: false` cascades `"blocked"` status to all transitive dependents
- [ ] Circular dependency in graph is detected and returns an error rather than hanging
- [ ] `returns` field in graph maps to the correct node output values in the final result
- [ ] `tzro execute -` accepts graph JSON on stdin and produces the same result as file input

## Edge Cases to Probe

- Empty graph (zero nodes) — should return completed with empty outputs
- Node referencing a non-existent dependency ID — should error cleanly
- `$ref` pointer to a field that does not exist in upstream output — should error with clear message
- Very large fan-out group node (50+ items) — should respect max_concurrency cap
- Decision node where Decider returns an error — should mark node as failed, not panic
- Graph where all nodes fail — should return overall status reflecting failures

## Anti-Patterns to Watch For

- [ ] Executor hangs indefinitely on circular graphs instead of detecting the cycle
- [ ] Nodes execute before their dependencies complete
- [ ] `$ref` pointers silently resolve to nil/empty instead of erroring on missing references
- [ ] Yield envelope omits the list of completed nodes, making resume impossible
- [ ] Group node does not collect sub-results — returns empty data for expanded items
- [ ] Exit code from shell tool is silently swallowed instead of propagated to node output
- [ ] Engine panics on nil Decider/Extractor when no decision/extract nodes exist in the graph
