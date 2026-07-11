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

## Edge Cases to Probe

- Starting the daemon with only a worker model configured (no router model) — verify all inference routes through the worker without errors.
- Router sidecar crashes mid-task — verify inflight and subsequent requests gracefully fall back to the worker.
- Both sidecars sharing the same GPU — verify no resource contention errors under concurrent requests.
- Swapping the router model while a probe is in progress — verify the swap completes between steps without corrupting the thought chain.
- Worker sidecar unreachable while router is healthy — verify router-only tasks succeed but worker-dependent tasks fail with clear errors.

## Anti-Patterns to Watch For

- [ ] Classification requests are dispatched to the worker sidecar when the router is healthy, wasting quality model capacity on trivial tasks.
- [ ] Router fallback to worker happens silently without any log message, making debugging impossible.
- [ ] A router sidecar crash causes the worker sidecar to restart or become unhealthy.
- [ ] Hot-swapping one model causes the other sidecar to reload or restart unnecessarily.
- [ ] The install script only downloads one model, leaving the user with a broken dual-sidecar setup.
- [ ] `isRouterAvailable()` returns stale status after the router crashes, routing requests to a dead process.
