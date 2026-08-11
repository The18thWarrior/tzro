# Use Case: Dual-Sidecar Router+Worker Inference

**Actor**: Developer running tasks through the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`, `tzro_code`, `tzro_completion`)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

A developer runs tasks through the tzro engine and expects the system to automatically dispatch inference requests to the appropriate sidecar model — a fast, small router model for classification, tool selection, and navigation decisions, or a larger worker model for code generation, complex reasoning, and synthesis. The developer should never need to manually specify which model handles which inference call; the system makes this decision transparently based on the request type, with automatic fallback from router to worker when the router sidecar is unavailable.

## Preconditions

- The `tzro` daemon is running with both router and worker GGUF model sidecars configured.
- The router model sidecar is loaded (e.g., MiniCPM5-1B) for fast classification tasks.
- The worker model sidecar is loaded (e.g., a larger model) for quality-intensive tasks.
- At least one model sidecar is active and healthy.

## Success Criteria

- [ ] Starting the daemon launches two separate llama-server sidecar processes — one for the router model and one for the worker model.
- [ ] Classification requests (intent routing, complexity classification, parameter extraction) are dispatched to the router sidecar via `CallRouter`.
- [ ] Code generation, DAG planning, synthesis, and complex reasoning requests are dispatched to the worker sidecar via `CallWorker`.
- [ ] Probe Node thought chain steps route through the router sidecar for fast tool-selection decisions.
- [ ] Edge Thought generation routes through the router sidecar for confidence scoring.
- [ ] When the router sidecar is unavailable (stopped, not configured, or unhealthy), all `CallRouter` requests transparently fall back to the worker sidecar with a logged warning.
- [ ] When the router sidecar is unavailable, `CallRouterStream` falls back to `CallWorkerStream` transparently.
- [ ] `ExecuteRouterStructured` falls back to `ExecuteWorkerStructured` when the router is unavailable.
- [ ] The `isRouterAvailable()` health check correctly reports router status based on the sidecar's "active" or "adopted" state.
- [ ] Both sidecars run independently — a crash or restart of the router does not affect the worker, and vice versa.
- [ ] Model hot-swap via `tzro_model_set` correctly targets the appropriate sidecar (router vs worker).
- [ ] The install script downloads and configures both the router model and the worker model during onboarding.
- [ ] DRY (Don't Repeat Yourself) sampling detects repetitive n-gram patterns during generation and applies penalties to prevent phrase loops in synthesis output.
- [ ] The Generation Guard detects and aborts degenerate outputs (repetitive content, meta-commentary loops) by monitoring generation tokens in real-time.
- [ ] Generation Guard correctly identifies tabular content (markdown tables, CSV-like rows) and does NOT flag repetitive-looking table rows as degenerate.
- [ ] Pre-flight token truncation limits input context to the model's maximum window before sending, preventing inference failures from oversized prompts.
- [ ] Default temperature is configurable via `config.json` and applies consistently across all inference calls.
- [ ] Memory-gated router context injects relevant memory facts into the router's pre-flight context when available.
- [ ] The embedding sidecar (third sidecar for neural embeddings) runs independently of the router and worker without resource contention.

## Edge Cases to Probe

- Starting the daemon with only a worker model configured (no router model) — verify all inference routes through the worker without errors.
- Router sidecar crashes mid-task — verify inflight and subsequent requests gracefully fall back to the worker.
- Both sidecars sharing the same GPU — verify no resource contention errors under concurrent requests.
- Swapping the router model while a probe is in progress — verify the swap completes between steps without corrupting the thought chain.
- Worker sidecar unreachable while router is healthy — verify router-only tasks succeed but worker-dependent tasks fail with clear errors.
- Generation produces 50 consecutive identical tokens — verify DRY sampling penalizes the pattern and forces diverse output.
- Input context exceeds model's max tokens by 2x — verify pre-flight truncation clips to fit without crashing the sidecar.
- Generation Guard encounters a 200-row markdown table — verify it is NOT aborted as "repetitive."
- Temperature set to 0.0 in config — verify deterministic output across identical inputs.

## Anti-Patterns to Watch For

- [ ] Classification requests are dispatched to the worker sidecar when the router is healthy, wasting quality model capacity on trivial tasks.
- [ ] Router fallback to worker happens silently without any log message, making debugging impossible.
- [ ] A router sidecar crash causes the worker sidecar to restart or become unhealthy.
- [ ] Hot-swapping one model causes the other sidecar to reload or restart unnecessarily.
- [ ] The install script only downloads one model, leaving the user with a broken dual-sidecar setup.
- [ ] `isRouterAvailable()` returns stale status after the router crashes, routing requests to a dead process.
- [ ] DRY sampling is applied during GBNF-constrained extraction, corrupting the structured JSON output.
- [ ] Generation Guard aborts a valid long synthesis because it contains naturally repetitive structure (numbered lists, tables).
- [ ] Pre-flight truncation clips the most recent context instead of the oldest, losing the user's actual query.

