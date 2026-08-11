# Use Case: Autonomous Codebase Exploration via Probe Nodes

**Actor**: Autonomous AI Coding Agent (e.g., Antigravity, Claude) executing locally.
**Route**: mcp://stdio (tzro_run)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

A local AI coding agent wants to explore an unfamiliar codebase or perform high-latency directory analysis without wasting cloud tokens or hitting context limits. The agent offloads the exploration task to a local Probe Node which runs a step-by-step reasoning thought chain offline, executing local filesystem tools and generating a compacted final report. The DAG supports neural edge traversal — dynamically spawning additional nodes mid-execution when accumulated context is insufficient.

## Preconditions

- The `tzro` daemon is running and has access to the local filesystem.
- The GGUF local model server is online and registered as the tactician.
- The client agent is connected to `tzro` via MCP stdio.

## Success Criteria

- [ ] Developer sees the compilation engine accept a task containing a Probe Node.
- [ ] User can verify in the logs that the local model receives a system prompt instructing it to behave as a Probe Node.
- [ ] Local model produces GBNF-constrained JSON responses matching the `ThoughtChainStep` schema at every step.
- [ ] The execution loop processes and executes only tools specified in the `AllowedTools` whitelist (e.g. `list_dir`, `read_file`, `search_files`).
- [ ] Reasoning steps, tool names, arguments, and outputs are successfully persisted to the SQLite database for durability.
- [ ] The execution loop automatically triggers a rolling compaction summary of recent steps every N steps to avoid context window explosion.
- [ ] Compaction respects the configured `CompactionLevel` — "preserve" passes through raw output, "moderate" summarizes prose but preserves code/tables, "aggressive" performs heavy summarization.
- [ ] The Probe Node successfully terminates early and returns a final synthesis if confidence reaches 0.9 or higher.
- [ ] The Probe Node enforces a minimum step budget before accepting a `<SYNTHESIZE_READY>` signal — premature synthesis signals are ignored and exploration continues.
- [ ] The Probe Node successfully triggers a forced synthesis of all findings if the maximum step budget is exhausted without converging.
- [ ] The synthesis pass applies content-aware truncation to tool outputs: code is truncated at bracket nesting boundaries preserving signatures, tabular data retains sample rows, and prose uses middle-out elision.
- [ ] Staging and committing changes handles Probe Node configurations correctly.
- [ ] Adaptive futility thresholds dynamically scale with step budget (stepBudget/4, minimum 5) to abort early when ALL initial steps return errors with zero successful calls.
- [ ] Futility abort logs diagnostic details for each failed step including step number, tool name, and error message.
- [ ] Output fingerprint convergence tracks the first 200 characters of each successful tool output; after 3 consecutive duplicate outputs, minStepBudget is lowered to allow synthesis instead of redundant exploration.
- [ ] KV cache prefix sharing hoists the system prompt (goal + tool schemas) outside the step loop so the llama-server's --cache-reuse window avoids ~500-1000 tokens of redundant KV computation per step.
- [ ] Probe thought chain steps route through the router sidecar (fast, small model) for tool-selection decisions, not the worker sidecar.
- [ ] When the router sidecar is unavailable, probe steps transparently fall back to the worker sidecar.
- [ ] The `InferMessages` method on ProbeInferenceEngine enables pre-segmented message arrays for maximum KV cache prefix reuse.
- [ ] When a node has a non-zero activation threshold, the executor generates an Edge Thought on each incoming edge after the source node completes.
- [ ] Edge Thoughts produce a goal confidence score (0.0–1.0) and a goal-achieved boolean via GBNF-constrained local inference.
- [ ] When edge thought confidence ≥ activation threshold, the target node executes normally.
- [ ] When edge thought confidence < activation threshold, a new node is dynamically spawned between source and target to gather additional context.
- [ ] When edge thought signals goal achieved, the target node and all downstream nodes are skipped.
- [ ] Failure dampening tracks consecutive spawned-node failures and suppresses further spawning after 3 consecutive failures.
- [ ] The mutation budget caps total spawned nodes per task, preventing runaway DAG expansion.
- [ ] Incremental Kahn sorting correctly re-sorts only pending/new nodes after dynamic mutations — completed nodes remain frozen.
- [ ] Content-aware truncation detects code content and truncates at bracket nesting boundaries, preserving function/method signatures and doc comments.
- [ ] Content-aware truncation detects tabular data (markdown tables, CSV) and retains header rows plus sample data rows.
- [ ] Content-aware truncation applies middle-out elision for prose content, preserving beginning and end while summarizing the middle.
- [ ] Truncation enforces a configurable character budget (default 160K for synthesis context).
- [ ] Symbolic callgraph extractor traverses function declarations, callsites, and references across multi-language codebases (Go, Python, TypeScript, JavaScript, Rust, Java).
- [ ] Exploration queue handles candidate traversal locations with deduplication to ensure resilient probe execution.
- [ ] SQL query extractor parses inline SQL queries and table references during code exploration.
- [ ] Two-Pass Tool Extraction (ADR-0064): Pass 1 runs free-text reasoning on the worker model, Pass 2 runs GBNF-constrained action extraction on the router model.
- [ ] When the probe has not met its minimum step budget, the GBNF grammar in Pass 2 constrains output to `tool_call` only — the model physically cannot output `synthesize`.
- [ ] Recall compaction produces a deduplicated, fact-first context summary injected before synthesis.
- [ ] DRY (Don't Repeat Yourself) sampling in the local model prevents repetitive phrase loops during synthesis by detecting and penalizing repeated n-grams.
- [ ] Foreground compute preemption interrupts background tasks when a user-initiated task arrives, ensuring responsive execution.
- [ ] Research phase provisioning automatically adds web_search and web_browse tools when the task is classified as research-oriented.
- [ ] Goal-driven query seeding populates initial tool arguments when the model produces empty args in Pass 2.

## Edge Cases to Probe

- Requesting tool calls not present in the Whitelist, verifying that the execution loop rejects them and returns a clean error in `tool_output` without halting the process.
- Executing a probe node with a step budget of 1, verifying that it immediately forces synthesis after the first step (min budget adapts to stepBudget/2).
- Probe signals `<SYNTHESIZE_READY>` at step 2 of a 20-step budget — verify the signal is ignored and exploration continues until at least step 8.
- The local model failing to produce valid JSON or returning a malformed structure, verifying that the parser recovers or reports a clean failure.
- Halting the executor daemon during a running probe, verifying that the SQLite state remains persisted and can be resumed.
- Probe reads a 20K-line file — verify content-aware truncation produces a bounded synthesis context without losing function signatures.
- Edge thought with activation threshold 0.7 and source output at confidence 0.3 — verify a new node is spawned.
- Three consecutive spawned nodes fail — verify failure dampening suppresses the 4th spawn and the target node runs with available context.
- Mutation budget exhausted — verify the executor stops spawning and proceeds with existing nodes.
- Edge thought signals goal achieved on the first edge — verify all downstream nodes are skipped and the task produces a synthesis from completed nodes only.
- Probe with stepBudget 8 — futility threshold should be 5 (minimum clamp), verify abort after 5 consecutive errors.
- Probe with stepBudget 40 — futility threshold should be 10 (40/4), verify extended recovery window.
- Probe reads the same file 4 times in a row — verify output fingerprint convergence triggers after 3 duplicates and lowers minStepBudget.
- Router sidecar crashes during probe step 3 of 15 — verify transparent fallback to worker for remaining steps.

## Anti-Patterns to Watch For

- [ ] The Probe Node calls unauthorized system tools (e.g. executing bash commands or modifying files) outside its whitelisted set.
- [ ] The thought chain execution loop loops infinitely or exceeds the specified step budget.
- [ ] Large tool outputs (e.g., viewing a 20,000 line file) cause context windows to overflow without truncation or compaction.
- [ ] Synthesis context exceeds the 160K character budget despite truncation being enabled.
- [ ] Code truncation destroys function signatures or doc comments that are needed for understanding.
- [ ] Raw JSON parse errors or local model stack traces are returned as the final synthesis outcome to the user.
- [ ] Persisting steps fails due to SQLite database lockouts during concurrent runs.
- [ ] Edge thought evaluation calls the cloud model instead of the local model, defeating the zero-cost guarantee.
- [ ] Spawned nodes inherit incorrect dependency edges, creating cycles in the DAG.
- [ ] Failure dampening counter is not reset after a successful spawn, permanently suppressing future activations.
- [ ] Mutation budget is not initialized, allowing unbounded node spawning.
