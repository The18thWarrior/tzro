# Probe Thought Chain Pass 1 routes through the Worker model

The Probe Thought Chain's Pass 1 (unconstrained free-text reasoning) was originally routed through the 1B router sidecar for speed. Benchmark results (results-research-10, 2026-08-03) showed catastrophic navigation failure: across 5 research tasks, the 1B model signaled "synthesize" at every step, produced 0 successful tool calls through 8-step probes, and all synthesis outputs were either repetitive (escalated to cloud) or hallucinatory. We reversed this: Pass 1 now routes through the 4B worker for navigation quality, while Pass 2 (GBNF-constrained action extraction) stays on the router. The routing is schema-based — `DefaultProbeInference.InferMessages` sends unconstrained calls (empty schema) to the worker and constrained calls (non-empty schema) to the router. The Pass 2 token cap was increased from 512 to 1024 to eliminate truncation-induced JSON parse failures observed in the same benchmark.

## Considered Options

- **Router for both passes** (original design): 2× faster per step, but the 1B model cannot reason about tool selection — every step defaulted to "synthesize" immediately.
- **Worker for both passes**: Simpler, but wastes worker capacity on GBNF extraction that the router handles well.
- **Configurable routing**: Rejected as premature — the benchmark evidence is conclusive and the routing is a one-line change if a better router model appears.

## Consequences

- Probe Thought Chain latency increases ~2× per step (worker generates at ~30-50 t/s vs router at ~100-150 t/s). A 20-step probe goes from ~40s to ~80-100s in the Thought Chain phase.
- Probe quality is expected to improve substantially — the 4B model can actually reason about which tools to call and produce meaningful `<ACTION>` tags for Pass 2 extraction.
- Recall nodes are unaffected — they already use `WorkerInference` for all passes.
