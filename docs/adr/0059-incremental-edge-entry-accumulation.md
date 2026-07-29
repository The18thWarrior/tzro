# Incremental Edge Entry Accumulation for Probe Thought Chains

Status: accepted. Supersedes ADR-0056 (append-only probe context).

Probe and Analyze Node Thought Chains switch from an append-only conversation with rolling compaction (ADR-0056) to an incremental accumulation model. Each step sees only the system prompt and the most recent tool output — no growing conversation history, no in-loop LLM compaction calls. Accumulated findings are captured as **Edge Entries** (tool name, arguments, deterministically truncated result snippet) and fed to the synthesis pass at loop termination.

## Problem

ADR-0056 introduced append-only conversations for KV cache reuse, but two overlapping context management systems compounded into a regression:

1. The **append-only conversation** grows monotonically, requiring `slidingWindowCompact` to drop oldest turns when exceeding ~11.4K tokens — each drop causes a full KV cache miss (~5-10s prefill penalty).
2. **`compactThoughtChain`** fires every 3 steps with an LLM call (~5-15s) to summarize recent steps into `ThoughtSummary` records — but these summaries don't replace the raw turns in the conversation, so the model processes both.

Together these cost **80-210s per probe** in non-inference overhead. The sliding window also silently violated the original Thought Chain contract of "every step sees the full accumulated context" — turns were dropped without the model's awareness.

## Decision

**Fixed-context-window loop**: Each step receives exactly two messages — the static system prompt (goal + upstream context + tool schemas + instructions) and a user message (breadcrumbs + last tool output + step query). The system message is byte-identical across all steps, enabling perfect KV cache prefix reuse with zero prefill overhead after step 1.

**Edge Entry accumulation**: Each successful tool call appends an `EdgeEntry` struct (tool name, args, truncated result snippet) to an in-memory slice. This replaces the append-only conversation as the exploration log. Edge Entries are never injected into the per-step prompt — they're compiled for synthesis only.

**Tool-type-aware truncation** (tiered budget): `read_file` results are reduced to Code Skeletons (signatures preserved, bodies stripped — uses existing Structured Compactor infrastructure). `search_files`, `list_dir`, `introspect_cache`, and `sql_cached_data` outputs are kept at full fidelity (they're already compact). This keeps the edge log well within the worker model's 64K context for synthesis.

**Breadcrumbs**: A deterministic, tool-type-aware exploration progress summary (~200-500 chars) injected into each step prompt. Provides the routing memory lost by removing the conversation history — lists files read, directories listed, searches run, and queries executed. Built from the Edge Entry accumulator at zero LLM cost. Complements the Exploration Queue (ADR-0058) which handles `read_file` dedup reactively; breadcrumbs provide proactive routing context for all tools.

**Probe-over-edges overflow**: When the concatenated Edge Entry log exceeds the synthesis model's context window (~200K chars), a second thought chain pass runs using the edge log as "tool outputs" instead of raw files. This is the overflow handler only — the tiered truncation budget keeps typical probes (≤30 steps) well within limits.

**Upstream context in system prompt**: `config.UpstreamContext` (Accumulated Context from upstream DAG nodes) is baked into the system prompt rather than injected as a separate message pair. This makes the system prompt the single static prefix — maximum KV cache benefit.

**SQLite persistence unchanged**: `AddThoughtStep` continues to write every step to SQLite for crash recovery, debugging, and Recall Node map-reduce. Only the in-loop `compactThoughtChain` calls are removed — no more rolling LLM compaction during the loop.

**Two-tier synthesis**: The Probe Node's own synthesis pass reads from the Edge Entry log (low-fidelity, fast). The downstream Recall Node's map-reduce reads from full SQLite ThoughtSteps (high-fidelity, thorough). Both produce synthesis outputs; the Recall Node refines the Probe's first pass.

## Considered Options

- **Keep append-only + compaction as a toggleable mode**: Rejected — the two systems overlap destructively (compaction doesn't reduce the conversation, sliding window doesn't benefit from compaction). There's no configuration where both systems help simultaneously. Hard switch is cleaner.
- **Sliding window only (remove compaction, keep append-only)**: Still pays cache miss at every window boundary. The fundamental problem is the growing conversation, not the compaction.
- **Full tool output in Edge Entries with LLM compression at synthesis time**: Single LLM compaction call at synthesis instead of rolling compaction. Better than rolling, but still adds inference latency. Code Skeleton truncation is deterministic and faster.

## Consequences

- **Performance**: Eliminates ~80-210s of non-inference overhead per probe (no sliding window cache misses, no in-loop compaction LLM calls). Per-step prefill drops to near-zero after step 1.
- **Routing quality**: Without conversation history, the model loses implicit memory of prior steps. Mitigated by breadcrumbs (proactive) and Exploration Queue (reactive). Net impact is a hypothesis until benchmarked.
- **Synthesis fidelity**: Edge Entry snippets are thinner than full tool outputs. Mitigated by Code Skeleton preservation of structural information and full-fidelity Recall Node pass.
- **Dead code**: `slidingWindowCompact`, `compactThoughtChain`, `buildProbeSegmentedMessages`, and `ThoughtSummary` persistence are no longer called from the probe loop. Retained in codebase — Recall Node and future consumers may use them.
- **Domain language**: CONTEXT.md Thought Chain definition updated to reflect incremental accumulation. New term "Edge Entry" added to glossary.
