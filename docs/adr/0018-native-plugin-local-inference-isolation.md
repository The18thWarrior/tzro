# Native Plugin Local Inference Isolation

We decide that `tzro` running in **Native Plugin Mode** must mandate local inference execution for DAG step parameter extraction (The Tactician role) to preserve execution privacy, eliminate cloud API token costs, and maintain low-latency local execution. 

By default, the plugin will launch and manage its own isolated local worker (`llama-server` sidecar process loading the Qwen-4B GGUF model). 

However, if the user explicitly provides or configures an existing local inference interface (such as LM Studio, Ollama, or an external llama.cpp server) via config parameters (`localBackendType` and `localBackendURL`), the plugin will connect to that interface, preventing duplicate model RAM usage and resource competition on consumer hardware.

Under no circumstances should intermediate step parameter extractions default to the parent harness's cloud model.

## Considered Options

*   **Defaulting to Parent Harness Inference**: If the parent harness uses a cloud model (such as Gemini 3.5 or Claude 3.5 Sonnet), route step executions to it. Rejected — executing intermediate DAG steps via cloud models defeats the local-first security, privacy, and cost-reduction value of the Kahn compiled task graph, incurring heavy token and latency costs.
*   **Always Booting Sidecar (No Local API Reuse)**: Mandate that `tzro` always boots its own dedicated `llama-server` process regardless of the developer's environment. Rejected — if the user already has a local Ollama or LM Studio instance running, booting a second model process causes unnecessary RAM overhead and CPU contention on consumer laptops.
