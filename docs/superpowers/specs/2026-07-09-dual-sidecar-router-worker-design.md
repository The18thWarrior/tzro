# Dual-Sidecar Architecture: Router + Worker Model

**Date:** 2026-07-09
**Status:** Approved

## Problem

The current single-sidecar architecture forces the local inference engine to serve two fundamentally different workloads through one `llama-server` process:

1. **Routing tasks** — tool selection, parameter extraction, Probe navigation, classification, GBNF-constrained validation. These are fast, short-output decisions (~5-50 tokens) that need low latency.
2. **Generation tasks** — code generation, complex reasoning, repair/fix passes, DAG planning, long-form synthesis. These need quality and large context windows.

A single model can't optimize for both. The 350M model is fast enough for routing but produces garbage code. A 4B code model is accurate but too slow for 30-step Probe chains. The current hot-swap mechanism (`SwapModelForTask`) stops the entire sidecar and restarts it with a different model (~1-1.5s downtime), blocking all inference during the swap and preventing concurrent routing + generation.

**Evidence from codegen benchmark #3:** Tasks where the local model only did routing/classification scored well (cloud fix triggered, avg quality 4.7/5.0). Tasks where the local model had to generate code scored poorly (avg quality 1.1/5.0). The model is good at routing, bad at generation — two different models should handle these two different jobs.

## Design

### Architecture

Two `LocalModelManager` instances running independently, each managing its own `llama-server` child process:

```
┌─────────────────────────────────────────────────────┐
│                     tzrod daemon                     │
│                                                      │
│  GlobalRouterModel              GlobalWorkerModel    │
│  ┌──────────────────┐          ┌──────────────────┐ │
│  │ LocalModelManager │          │ LocalModelManager │ │
│  │                    │          │                    │ │
│  │ Model: Gallium-350M│         │ Model: Qwopus-4B  │ │
│  │ Port:  (dynamic)   │          │ Port:  (dynamic)  │ │
│  │                    │          │                    │ │
│  │ KV: q4_0           │          │ KV: q4_0          │ │
│  │ Ctx: 2048 tokens   │          │ Ctx: 16384 tokens │ │
│  │ Slots: 1           │          │ Slots: 1          │ │
│  │ MTP: No            │          │ MTP: Yes (if avail)│ │
│  └──────────────────┘          └──────────────────┘ │
│       ↑ fast, routing               ↑ quality, gen   │
└─────────────────────────────────────────────────────┘
```

**Key properties:**

- Both are full `LocalModelManager` instances — reusing all existing process management, health probes, GC, and port-file adoption logic.
- Independent processes: each has its own port, PID, health client, and file lock. If one crashes, the other keeps running.
- No hot-swapping: neither sidecar ever changes models at runtime. Hot-swap becomes dead code.
- The router has a small context window (2048) since it only processes short instructions and tool schemas. The worker retains the full 16k context for code generation and synthesis.

### Inference Routing API

Explicit caller opt-in via package-level functions in the `inference` package:

```go
// inference/routing_dual.go

// CallRouter sends an inference request to the router sidecar (fast, small model).
// Use for: tool selection, parameter extraction, Probe navigation, classification,
// validator passes, short summarization.
func CallRouter(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error)

// CallWorker sends an inference request to the worker sidecar (quality, large model).
// Use for: code generation, complex reasoning, repair/fix passes, DAG planning,
// long-form synthesis.
func CallWorker(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error)

// Streaming variants
func CallRouterStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error)
func CallWorkerStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error)
```

**Call site routing table:**

| Call site | Target | Reason |
|---|---|---|
| Probe Thought Chain steps | Router | Navigation decisions, short outputs |
| Validator Pass 1 (tool extraction) | Router | Structured JSON extraction |
| Validator Pass 2 (GBNF refinement) | Router | Constrained classification |
| Complexity classification | Router | Single-word classification |
| `tzro_classification` MCP tool | Router | GBNF-constrained classification |
| Planner (DAG generation) | Worker | Complex structured reasoning |
| Edge Thought generation | Worker | Consumes large node output, needs 16k ctx |
| `reason_code` node execution | Worker | Code generation |
| Repair DAG nodes | Worker | Code fix generation |
| Terminal synthesis (long-form) | Worker | Quality summarization |
| `tzro_completion` MCP tool | Worker | User-facing generation |

**Backward compatibility:** The existing `RouteInference` function in `routing.go` continues to work. Its "local" path calls `CallWorker` by default, so any call site that hasn't been migrated to `CallRouter` yet still works correctly. Migration of individual call sites is incremental.

### Lifecycle & Daemon Integration

Both sidecars start at daemon boot, managed in parallel:

```
tzrod boot
  ├── Start GlobalRouterModel (fast — 350M loads in ~500ms)
  ├── Start GlobalWorkerModel (slower — 4B loads in ~2-3s)
  ├── Health-check both (parallel)
  └── Ready
```

**Process management:**

- Port files: `.tzro/.llama-server-router.port` and `.tzro/.llama-server-worker.port`, each storing `port:pid`.
- File locks: `.tzro/.llama-server-router.lock` and `.tzro/.llama-server-worker.lock`. Independent locks so both can start concurrently.
- Adoption: each manager independently checks its own port file on startup for cross-process adoption.
- Shutdown: `tzrod` stops both on graceful shutdown. If one fails to stop, the other still gets stopped.
- Health monitoring: existing GC and health probe loops run independently per manager.

**Failure isolation:**

- Router crashes → worker keeps running. Probe and validator calls fail gracefully (cloud fallback via `RouteInference`). Router auto-restarts.
- Worker crashes → router keeps running. Code generation fails but tool routing and Probe exploration continue. Worker auto-restarts.
- Neither crash takes down the daemon process.

### Configuration

The `mcp_config.json` (and `TzroConfig` struct) gets one new field:

```json
{
  "ggufModelPath": "Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf",
  "routerModelPath": "Gallium-350M.Q8_0.gguf"
}
```

- `ggufModelPath` — the worker model (unchanged field name, backward compatible)
- `routerModelPath` — **new** — the router model
- `codeModelPath` — **deprecated**, ignored when `routerModelPath` is set

**Graceful fallback:** If `routerModelPath` is empty or the router fails to start, all `CallRouter()` calls transparently fall back to `CallWorker()`. The system works exactly like today's single-sidecar mode, just slightly slower for routing tasks. A warning is logged: `[Inference] Router sidecar unavailable, falling back to worker`.

### Dead Code Removal

Once dual-sidecar ships, the following become dead code:

| Code | Action |
|------|--------|
| `SwapModelForTask()` | Delete |
| `RestoreModel()` | Delete |
| `EnsureDefaultModel()` | Delete |
| `MarkCodegenActive()` / `MarkCodegenDone()` | Delete |
| `codeModelActive`, `defaultModelPath`, `codegenActive` fields | Delete |
| `codeModelPath` config field | Deprecate (keep for backward compat, ignore) |
| Hot-swap logic in `RunCodegenCondition` / `RunDAGCondition` | Delete |

### MCP Tool Changes

- `tzro_model_set` — sets the worker model (same behavior as today)
- Future: ability to set router model independently (parameter on `tzro_model_set` or separate tool)

## Scope Summary

1. Add `GlobalRouterModel` (`LocalModelManager` instance) + `routerModelPath` config
2. Add `CallRouter`/`CallWorker` explicit routing API with streaming variants
3. Update daemon boot to start both sidecars in parallel
4. Migrate call sites incrementally (Probe, validators, classification → Router; planner, edge thoughts, codegen, synthesis → Worker)
5. Delete hot-swap dead code (`SwapModelForTask`, `RestoreModel`, `codeModelPath`, etc.)
6. Update port file and lock file conventions for two sidecars
