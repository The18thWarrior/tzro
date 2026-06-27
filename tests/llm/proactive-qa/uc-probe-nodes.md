# Use Case: Autonomous Codebase Exploration via Probe Nodes

**Actor**: Autonomous AI Coding Agent (e.g., Antigravity, Claude) executing locally.
**Route**: mcp://stdio (tzro_run)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

A local AI coding agent wants to explore an unfamiliar codebase or perform high-latency directory analysis without wasting cloud tokens or hitting context limits. The agent offloads the exploration task to a local Probe Node which runs a step-by-step reasoning thought chain offline, executing local filesystem tools and generating a compacted final report.

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

## Edge Cases to Probe

- Requesting tool calls not present in the Whitelist, verifying that the execution loop rejects them and returns a clean error in `tool_output` without halting the process.
- Executing a probe node with a step budget of 1, verifying that it immediately forces synthesis after the first step (min budget adapts to stepBudget/2).
- Probe signals `<SYNTHESIZE_READY>` at step 2 of a 20-step budget — verify the signal is ignored and exploration continues until at least step 8.
- The local model failing to produce valid JSON or returning a malformed structure, verifying that the parser recovers or reports a clean failure.
- Halting the executor daemon during a running probe, verifying that the SQLite state remains persisted and can be resumed.
- Probe reads a 20K-line file — verify content-aware truncation produces a bounded synthesis context without losing function signatures.

## Anti-Patterns to Watch For

- [ ] The Probe Node calls unauthorized system tools (e.g. executing bash commands or modifying files) outside its whitelisted set.
- [ ] The thought chain execution loop loops infinitely or exceeds the specified step budget.
- [ ] Large tool outputs (e.g., viewing a 20,000 line file) cause context windows to overflow without truncation or compaction.
- [ ] Synthesis context exceeds the 160K character budget despite truncation being enabled.
- [ ] Code truncation destroys function signatures or doc comments that are needed for understanding.
- [ ] Raw JSON parse errors or local model stack traces are returned as the final synthesis outcome to the user.
- [ ] Persisting steps fails due to SQLite database lockouts during concurrent runs.
