# Append-Only Probe Context for Full KV Cache Prefill Reuse

Probe Thought Chain execution now uses an **append-only conversation** instead of rebuilding the message array from scratch each step. This makes the entire conversation prefix byte-identical across steps, enabling llama-server's KV cache to fully reuse all prior prefill computation.

## Problem

Each probe step called `buildProbeSegmentedMessages()` which reconstructed the full message array by re-reading compaction summaries and recent thought steps from SQLite. Because the accumulated context segment changed every step (new thoughts and compaction updates), only segment 1 (system prompt, ~500-1000 tokens) matched the KV cache prefix. Everything else was re-prefilled from scratch.

With `--cache-reuse 2048`, only the first 2048 tokens were checked for prefix matches — enough for the system prompt but insufficient for the growing conversation. Over 20 steps with ~3000 prompt tokens per step, approximately ~2000 tokens per step were redundantly re-processed, wasting ~6-7 seconds per step (~120-140s total per probe).

## Decision

### Append-Only Conversation Construction

Instead of rebuilding each step:
```
Step 1: [system, upstream, ack, accumulated_v1, ack, query_1]
Step 2: [system, upstream, ack, accumulated_v2, ack, query_2]  ← v2 differs!
```

Maintain a growing conversation where each step appends:
```
Step 1: [system, upstream, ack, query_1]
Step 2: [system, upstream, ack, query_1, response_1, query_2]
Step 3: [system, upstream, ack, query_1, response_1, query_2, response_2, query_3]
```

The entire prefix is byte-identical across steps, enabling full KV cache reuse.

### Unlimited Prefix Matching

`--cache-reuse` raised from 2048/256 to 0 (unlimited) for both worker and router sidecars. Made configurable via `CacheReuseTokens` in the engine config (default 0).

### Sliding Window Compaction

When the conversation exceeds 70% of the router's 16K context window (~11.4K tokens), the oldest user/assistant turn pairs are dropped while preserving:
1. The static prefix (system prompt + upstream context)
2. A compaction marker noting how many turns were dropped
3. The most recent turns that fit within the budget

This is a one-time cache miss per compaction boundary vs. the old approach of paying full prefill every single step.

## Considered Options

### Compaction Strategy
- **Full history compaction**: Compact all accumulated turns into a single summary. Rejected: requires an LLM call at each compaction point, adding latency; summary loses the granular tool output detail the model needs for exploration decisions.
- **Sliding window (chosen)**: Drop oldest turns, keep recent N. Zero-cost (no LLM call), preserves the most relevant context, and the compaction marker gives the model awareness that earlier context was dropped.

### Prefix Matching Budget
- **`--cache-reuse 8192`**: Would cover most practical prefixes but still miss very long conversations. Added complexity of "tuning the right number".
- **`--cache-reuse 0` (chosen)**: Unlimited. Simplest, maximum benefit. Memory cost is bounded by `--cache-ram 2048` (2GB cap) which prevents unbounded growth.

## Consequences

- **Performance**: ~6-7s saved per probe step (prefill elimination). Over a 20-step probe, this saves ~120-140s of wall-clock time.
- **Memory**: Larger KV cache prefix store. Bounded by `--cache-ram 2048` (2GB).
- **Context pressure**: Router's 16K context fills faster with append-only conversations. Sliding window compaction triggers around step 8-10 for typical probes, paying one cache miss to unlock another N steps of full reuse.
- **SQLite persistence**: Unchanged. Thought steps and compaction summaries are still persisted identically — the in-memory conversation is orthogonal to durability.
- **Backward compatibility**: `buildProbeSegmentedMessages()` is retained for any external callers but no longer used in the main probe loop.
