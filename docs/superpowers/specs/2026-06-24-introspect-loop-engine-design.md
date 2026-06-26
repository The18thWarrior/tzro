# Introspect Loop Execution Engine

**Date**: 2026-06-24
**Status**: Draft
**Author**: JP (brainstorming session)

---

## Problem Statement

The current DAG execution engine excels at tasks where the workflow can be planned upfront — "search, then save, then synthesize" compiles cleanly into a 3-node DAG. But many real tasks are inherently reactive: the next step depends entirely on what the last step discovered. Codebase exploration, open-ended research, and iterative debugging all follow this pattern.

Today, these reactive tasks are handled by Probe nodes with high Activation Thresholds and mutation budgets, which dynamically spawn additional nodes. This works but has limitations:

1. **The planner still compiles a DAG upfront**, even when the "plan" is essentially "explore and react." This wastes a planning inference call on a structure that will be heavily mutated anyway.
2. **Spawned nodes are rigid** — each spawned node is a single tool call with parameters decided at spawn time, without the benefit of reasoning about the result before choosing the next step.
3. **Context management is implicit** — there's no explicit compaction between spawned steps, so accumulated context either grows unboundedly or is lost.

The Introspect Loop is a new execution mode that combines the **durability and auditability of the graph model** with the **adaptive, step-at-a-time reasoning of the ReAct framework**, optimized for a 4B parameter local model with limited context.

---

## Design Overview

### Core Concept

Instead of compiling a multi-node DAG upfront, the engine starts with a single **introspect node**. This node evaluates the current state (user prompt + accumulated context) and decides:

1. **Emit a tool call** → the engine executes the tool, compacts the result into a running summary, and runs another introspect node with the updated context.
2. **Emit a final response** → the task terminates.

Each step (introspect + tool execution) is persisted as real, checkpointed graph nodes in SQLite — giving full durability, replay, and crash recovery — while the "planning" is emergent and single-step, keeping the 4B model's context tight and focused.

### Think-Then-Act Output Format

The introspect node uses a **think-then-act** output format. The model emits free-text reasoning (a scratchpad) followed by a structured action block:

```
The user asked about AI orchestration trends. I've already searched for
general trends and found three major themes. I should now search
specifically for "local-first AI" since that was underrepresented in
the first results.

<ACTION>
{"type": "tool_call", "tool": "web_search", "params": {"query": "local-first AI orchestration 2026"}}
</ACTION>
```

Or for termination:

```
I now have comprehensive findings on all three major themes. The user's
question has been fully answered.

<ACTION>
{"type": "respond", "content": "Here are the key trends in AI orchestration..."}
</ACTION>
```

The scratchpad reasoning serves dual purposes:
- **For the model**: reasoning before committing to a decision improves action quality, especially for small models.
- **For the operator**: the scratchpad is stored in the node record as an audit trail, viewable during replay and debugging.

The scratchpad is compacted away between steps (it does not persist into the next introspect node's context), but the full text is always available in the node's `output.scratchpad` field.

### Context Management: Compacted Rolling Summary

After each tool execution, the engine runs a **compaction pass** using the local model. This compresses the accumulated history (original prompt + prior summary + latest tool result) into a tight summary that becomes the input for the next introspect node.

The loop's context window at any step contains only:
- **Original user prompt** (constant)
- **Compacted summary** of all prior steps (grows slowly, bounded by compaction)

This ensures predictable, bounded context size regardless of how many steps the loop takes.

```
User Prompt → Introspect₀(prompt) → "call tool X"
  → Execute Tool X → Compact(prompt + tool_result) → summary₁
  → Introspect₁(prompt + summary₁) → "call tool Y"
  → Execute Tool Y → Compact(prompt + summary₁ + tool_result) → summary₂
  → Introspect₂(prompt + summary₂) → "here's the answer"
  → Done
```

---

## Architecture

### Execution Mode Selection

The Introspect Loop is a **separate execution mode** alongside the existing DAG engine. Both share tzro's core infrastructure:

- SQLite checkpointing and task/node storage
- Tool registry and execution
- Compaction pipeline
- Memory and knowledge graph
- MCP interface

Selection is via an **explicit flag** on `tzro_run`. No auto-classification. The caller decides.

```
┌──────────────────────────────────────────────────┐
│                   tzro_run                       │
│            mode: "dag" | "loop"                  │
├────────────────────┬─────────────────────────────┤
│   DAG Engine       │     Introspect Loop         │
│   (existing)       │     (new)                   │
│                    │                              │
│   Planner →        │     Introspect →             │
│   Kahn Compile →   │     Tool Exec →              │
│   Parallel Exec →  │     Compact →                │
│   Synthesis        │     Loop or Respond          │
├────────────────────┴─────────────────────────────┤
│              Shared Infrastructure               │
│  SQLite · Tools · Compaction · Memory · KG · MCP │
└──────────────────────────────────────────────────┘
```

### Comparison of Execution Modes

| Dimension | DAG Engine | Introspect Loop |
|---|---|---|
| Planning | Upfront compilation into multi-node DAG | Emergent, one decision at a time |
| Graph shape | Arbitrary DAG (fan-out, fan-in) | Linear chain |
| Parallelism | Yes (topological sort, ready queue) | No (sequential by design) |
| Context strategy | Variable binding (`{{nodes.X.output}}`) | Compacted rolling summary |
| Best for | Known workflows, parallelizable tasks | Exploration, reactive research, iterative debugging |
| Planner cost | One planning inference call | Zero (no upfront planning) |

---

## Node Structure & Persistence

Each loop iteration produces **two nodes** in the existing SQLite `nodes` table:

### Introspect Node

```json
{
  "taskId": "abc-123",
  "nodeId": "introspect_0",
  "type": "introspect",
  "input": {
    "prompt": "Research local-first AI trends and save findings",
    "summary": ""
  },
  "output": {
    "scratchpad": "This is a broad research task. I should start with a general web search to identify the major themes before diving into specifics.",
    "action": {
      "type": "tool_call",
      "tool": "web_search",
      "params": { "query": "local-first AI trends 2026" }
    }
  },
  "tokensUsed": 847,
  "status": "completed",
  "completedAt": 1782341100
}
```

### Tool Node

```json
{
  "taskId": "abc-123",
  "nodeId": "tool_0",
  "type": "tool",
  "input": {
    "tool": "web_search",
    "params": { "query": "local-first AI trends 2026" }
  },
  "output": {
    "result": "...raw tool output..."
  },
  "status": "completed",
  "completedAt": 1782341105
}
```

### Edge Structure

Edges are always sequential:

```
introspect_0 → tool_0 → introspect_1 → tool_1 → introspect_2 → [respond]
```

Compaction happens **between** `tool_N` completing and `introspect_N+1` starting. It is not a separate node — the compacted summary is stored as `introspect_N+1.input.summary`. This keeps the graph clean while preserving full auditability (the raw tool output is always in `tool_N.output.result`).

### Replay

Reading the node chain in order gives a complete audit trail:
1. What context the model saw (introspect input)
2. What it reasoned (scratchpad)
3. What it decided (action)
4. What the tool returned (tool output)
5. What was carried forward (next introspect's summary)

---

## The Introspect System Prompt

The 4B model receives a tightly structured system prompt:

```
You are a task executor with access to these tools:

{tool_name}: {tool_description}
  Parameters: {param_name} ({param_type}): {param_description}

[repeated for each allowed tool]

Given the user's goal and accumulated context, reason through what to do next,
then output exactly ONE action block.

To call a tool:
<ACTION>
{"type": "tool_call", "tool": "tool_name", "params": {...}}
</ACTION>

To provide the final answer (when the task is complete):
<ACTION>
{"type": "respond", "content": "your comprehensive answer"}
</ACTION>

Rules:
- Think step by step before choosing an action
- Call only ONE tool per step
- When you have enough information to fully answer the goal, respond immediately
- Do not repeat tool calls you have already made with the same parameters
```

The user message contains:

```
Goal: {original prompt}

Context so far:
{compacted summary, or "No prior context." on first iteration}
```

---

## Safety Rails

Three-layer termination prevents runaway loops:

### 1. Hard Step Cap

Maximum number of introspect iterations. Default: **20**. Configurable per-task.

When triggered: run one final compaction over all accumulated context, emit as the terminal response with a `forcedTermination: true` flag and reason `"step_cap_exceeded"`.

### 2. Token Budget

Total tokens consumed across all inference calls (introspect + compaction) within the task. Default: based on model context window (e.g., **32,000 tokens** for a 4K context model running multiple iterations).

When triggered: same forced termination behavior as step cap, with reason `"token_budget_exceeded"`.

### 3. Stuck Detection

Track `(tool_name, hash(params))` tuples across iterations. If the same tuple appears **3 times** (configurable), force termination with reason `"stuck_loop_detected"`.

### Forced Termination Behavior

On any forced termination:
1. Run a final compaction pass over all accumulated context
2. Emit the compacted result as the terminal response
3. Set `forcedTermination: true` on the task record
4. Set `terminationReason` to one of: `step_cap_exceeded`, `token_budget_exceeded`, `stuck_loop_detected`
5. Mark task status as `completed` (not `failed` — the partial result is still useful)

---

## Tool Availability

### Caller-Specified with Sensible Defaults

The caller can pass `allowedTools` in the `tzro_run` request. If omitted, the loop defaults to a curated subset:

**Default tool set:**
- `web_search` — multi-engine web search
- `read_file` — read file contents
- `list_dir` — list directory contents
- `search_files` — search for patterns in files
- `save_memory` — persist findings to memory

This default set covers the two most common reactive task patterns: web research and codebase exploration.

**Caller override:**
```json
{
  "allowedTools": ["web_search", "save_memory", "query_knowledge_graph"]
}
```

The introspect system prompt dynamically includes only the definitions for allowed tools, keeping the 4B model's context focused.

---

## New MCP Tool: `tzro_compact`

A conversation-aware compaction tool exposed via MCP for pre-processing context before submission.

### Purpose

Allows callers (e.g., cloud coding agents with long conversation histories) to compress conversation context using the free local model before submitting a `tzro_run` request. This is **cost arbitrage** — the compaction work runs on the local 4B model instead of burning expensive frontier tokens.

### API

```json
{
  "name": "tzro_compact",
  "description": "Compact a conversation history into a focused summary using the local model. Conversation-aware: preserves user corrections, explicit requirements, and final decisions while compressing assistant reasoning and dropping pleasantries.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "messages": {
        "type": "array",
        "description": "Conversation messages to compact",
        "items": {
          "type": "object",
          "properties": {
            "role": { "type": "string", "enum": ["user", "assistant", "system"] },
            "content": { "type": "string" }
          },
          "required": ["role", "content"]
        }
      },
      "focusHint": {
        "type": "string",
        "description": "Optional guidance on what to prioritize preserving during compaction"
      }
    },
    "required": ["messages"]
  }
}
```

### Response

```json
{
  "summary": "compacted text suitable as tzro_run prompt context",
  "stats": {
    "inputTokens": 4200,
    "outputTokens": 380,
    "compressionRatio": 11.05
  }
}
```

### Compaction Heuristics

The compaction system prompt instructs the local model to apply conversation-aware logic:

| Content Type | Action |
|---|---|
| User corrections ("actually, I meant...") | **Preserve** verbatim |
| Explicit requirements and constraints | **Preserve** verbatim |
| Final decisions and confirmed choices | **Preserve** verbatim |
| Technical details referenced in decisions | **Preserve** in compressed form |
| Assistant reasoning and explanations | **Compress** to key conclusions |
| Exploratory back-and-forth | **Compress** to outcome only |
| Pleasantries and acknowledgments | **Drop** |
| Repeated explanations | **Deduplicate** to single instance |

The optional `focusHint` parameter biases preservation toward content relevant to the hint. For example, `focusHint: "memory system architecture"` would prioritize preserving conversation segments about the memory system while aggressively compressing unrelated discussion.

---

## API Surface Changes

### Modified: `tzro_run`

New parameters added to the existing tool:

```json
{
  "prompt": "Research local-first AI trends and save findings",
  "mode": "loop",
  "allowedTools": ["web_search", "save_memory"],
  "loopConfig": {
    "maxSteps": 50,
    "maxTokens": 32000,
    "stuckThreshold": 3
  }
}
```

| Parameter | Type | Default | Description |
|---|---|---|---|
| `mode` | `"dag" \| "loop"` | `"dag"` | Execution mode. `"dag"` uses existing DAG engine. `"loop"` uses introspect loop. |
| `allowedTools` | `string[]` | (curated default set) | Tools available to the loop. Only used when `mode: "loop"`. |
| `loopConfig.maxSteps` | `int` | `50` | Maximum introspect iterations before forced termination. |
| `loopConfig.maxTokens` | `int` | `32000` | Total token budget across all inference calls. |
| `loopConfig.stuckThreshold` | `int` | `3` | Number of identical tool calls before stuck detection triggers. |

When `mode: "dag"` (default), behavior is identical to today. The `allowedTools` and `loopConfig` parameters are ignored.

### New: `tzro_compact`

See the `tzro_compact` section above for full API specification.

### Unchanged: `tzro_status`

The status tool works identically for both modes. Loop tasks return the same structure — `status`, `nodes[]`, `completedAt`. The node types will be `introspect` and `tool` instead of `probe`/`action`/`synthesis`, but the shape is the same.

A loop task's status response includes additional metadata:

```json
{
  "taskId": "abc-123",
  "status": "completed",
  "mode": "loop",
  "loopMetrics": {
    "stepsCompleted": 5,
    "totalTokensUsed": 12400,
    "forcedTermination": false
  },
  "nodes": [...]
}
```

---

## Dashboard Visualization

The existing DAG view in the dashboard renders nodes as a directed graph, which is natural for multi-branch DAGs but degenerates into a hard-to-read horizontal line for the introspect loop's linear chain.

Loop tasks should render as a **vertical timeline** instead:

```
┌─────────────────────────────────────┐
│  Task: Research AI trends           │
│  Mode: loop · Steps: 5/20          │
├─────────────────────────────────────┤
│                                     │
│  ● Introspect₀                     │
│  │ "Starting with broad search..." │
│  │ → web_search("AI trends 2026") │
│  │                                 │
│  ● Tool₀: web_search              │
│  │ Found 12 results               │
│  │                                 │
│  ● Introspect₁                    │
│  │ "Found 3 themes, need to dig   │
│  │  deeper on local-first..."     │
│  │ → web_search("local-first AI") │
│  │                                 │
│  ● Tool₁: web_search              │
│  │ Found 8 results                │
│  │                                 │
│  ● Introspect₂                    │
│  │ "Have comprehensive findings,  │
│  │  saving to memory..."          │
│  │ → save_memory(...)             │
│  │                                 │
│  ● Tool₂: save_memory             │
│  │ Saved                          │
│  │                                 │
│  ● Introspect₃                    │
│  │ "Task complete."               │
│  │ → respond                      │
│  │                                 │
│  ✓ Complete                       │
│                                    │
└─────────────────────────────────────┘
```

Each node in the timeline is expandable to show full scratchpad reasoning, raw tool output, and the compacted summary that was carried forward.

---

## Interaction with Existing Features

### Memory & Knowledge Graph

The introspect loop has full access to `save_memory` and KG tools if they are in the `allowedTools` set. Findings can be persisted during the loop, not just at the end.

### Micro-Skills (SOPs)

Successful introspect loop trajectories are candidates for SOP extraction, just like DAG trajectories. The linear chain structure is actually easier to convert to an SOP than a branching DAG.

### Client Tools & Human-in-the-Loop

If a tool in the `allowedTools` set is a client tool (registered via `tzro_register_client_tools`), the loop pauses at the tool node and waits for `tzro_client_tool_submit`, identical to the DAG engine's behavior.

### Observer Agent

The Observer sees introspect loop tasks the same way it sees DAG tasks — via node completion events. The `introspect` node type is a new event type the Observer should recognize.

---

## Future Considerations

These are explicitly **out of scope** for the initial implementation but worth noting:

1. **Auto-classification**: A future enhancement could use `tzro_classification` to automatically route prompts to `"dag"` or `"loop"` mode when the caller doesn't specify. Deferred because explicit mode selection is simpler and more predictable.

2. **Loop-as-DAG-node**: Embedding an introspect loop as a single node inside a larger DAG would enable hybrid workflows (e.g., `loop_research → dag_parallel_save`). Architecturally clean but adds complexity. Deferred.

3. **Multi-tool steps**: Allowing the introspect node to emit multiple tool calls in one step (parallel execution within a loop iteration). Would improve throughput for independent tool calls but complicates the linear chain model. Deferred.

4. **Adaptive compaction**: Varying compaction aggressiveness based on remaining token budget — compress more when running low on budget, preserve more when budget is ample. Optimization for later.
