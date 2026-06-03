# Pluggable Inference Backend

We split the `LocalModelManager` into two concerns: a pluggable **Inference Backend** interface for structured LLM calls, and a **Sidecar Manager** for llama-server process lifecycle. This decoupling allows the Local Model to be backed by any OpenAI-compatible endpoint — the embedded llama-server sidecar, LMStudio, Ollama, vLLM, or even a harness callback routing inference through an external agent framework (e.g., Hermes Agent).

The Inference Backend is selected via config-level settings (`localBackendType` + `localBackendURL`), not runtime injection. The routing logic in `ExecuteStructured` remains unchanged — it still follows the `cooperative | local | cloud` ModelMode split from ADR-0010. Only the "local" leg now dispatches through the configured backend instead of always hitting the embedded llama-server.

The Sidecar Manager (port allocation, health probes, PID files, RSS monitoring, Tier 2 GC, slot save/restore, GPU layer detection, P-core pinning) is only relevant when the backend type is `llama-server`. Other backends skip sidecar lifecycle entirely.

## Considered Options

- **Runtime injection via MCP**: The harness would inject an inference callback at connection time. Rejected — config-level is simpler, avoids MCP protocol complexity for what is fundamentally a deployment topology choice, and allows the user to change backends without restarting the harness.
- **Keep `LocalModelManager` monolithic, add URL override**: Just add a `localBackendURL` config field and skip sidecar startup if set. Rejected — couples unrelated lifecycle management code to every backend path, making the Remote/Ollama path carry dead llama-server-specific code.
