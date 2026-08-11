# Mandatory Recall Node Injection for Single-Probe DAGs

The Kahn Compiler skipped Recall Node injection when a Probe Node was the sole discovery node in the graph (`discoveryNodesCount <= 1`). This was a latency optimization: the Probe's internal Thought Chain synthesis was treated as sufficient terminal output, avoiding the overhead of a Recall Node refinement pass.

With Verified Task Execution (ADR-0067), the Recall Node became the gateway to VTE's recovery mechanisms: Structural Pre-Check, Verification Gate, Cloud Re-Synthesis, and Item-Level Scatter (ADR-0071). Single-probe DAGs bypassed all of these. Benchmark run 21 showed three research tasks (`compare_llm_frameworks`, `internal_architecture`, `technical_deep_dive_gguf`) where the Generation Guard aborted synthesis mid-stream during the validator node's inference — and no recovery path existed because VTE never ran.

We remove the `discoveryNodesCount <= 1` exception. The Kahn Compiler now injects a Recall Node after every Probe Node unconditionally (Analyze Nodes already had this guarantee via ADR-0053). Single-probe tasks pay the additional Recall Node latency (~2-5s for refinement pass + synthesis), but gain access to the full VTE recovery pipeline.

## Considered Options

1. **Keep the exception, add abort detection in the action node path** — detect `[GENERATION_ABORTED]` in the validator output and retry once. Rejected: treats a symptom rather than closing the verification gap. The retry uses the same 4B model that just degenerated, and doesn't provide cloud re-synthesis.

2. **Keep the exception, add a lightweight VTE path for non-recall tasks** — run Structural Pre-Check and cloud re-synthesis directly on the probe's Thought Chain output. Rejected: duplicates the Recall Node's synthesis pipeline in a second location, creating two VTE code paths to maintain.

3. **Remove the exception, always inject Recall** — accepted. The Recall Node's refinement pass already handles single-probe output efficiently (no multi-source merging needed). The latency cost is bounded. The VTE pipeline stays unified.

## Consequences

- `sct_compiler.go` L238: the condition changes from `(discoveryNodesCount > 1 || node.Type == "analyze")` to `true` (for all probe and analyze nodes). The `discoveryNodesCount <= 1` skip path and its terminal synthesis injection skip (L340-361) are removed.
- Single-probe research tasks now route through the Recall Node, gaining VTE coverage. Guard aborts during the probe's synthesis are caught by `validateSynthesisOutput()` in the recall path and escalated to cloud re-synthesis.
- The CONTEXT.md definition for Recall Node ("injected automatically after a Probe Node") is now accurate for all cases, not just multi-probe DAGs.
- Plan Templates (companion effort) complement this fix by ensuring research tasks use `probe → recall` shapes from the start, preventing the local planner from inventing spurious `write_file` action nodes.
