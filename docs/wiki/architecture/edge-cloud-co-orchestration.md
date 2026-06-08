# Architecture Concept: Edge-Cloud Co-Orchestration Beyond DAGs

Cooperative Edge-Cloud Co-Orchestration refers to architectures and techniques that coordinate small, local models (SLMs, 2-9B parameters) with large, remote cloud models (70B+ parameters) to optimize cost, latency, privacy, and accuracy.

---

## 🗺️ Architectural Alternatives Map

Compared to traditional Directed Acyclic Graph (DAG) executors, the modern landscape contains several advanced coordination patterns:

```
                                  ┌────────────────────────┐
                                  │ Co-Orchestration Paths │
                                  └───────────┬────────────┘
                                              │
         ┌─────────────────────┬──────────────┴───────┬─────────────────────┐
         ▼                     ▼                      ▼                     ▼
┌─────────────────┐   ┌─────────────────┐    ┌─────────────────┐   ┌─────────────────┐
│   Blackboard    │   │    RL Routing   │    │   Speculative   │   │  Decentralized  │
│  - Stateless    │   │  - GAPG/Router  │    │  - PicoSpec     │   │  - P2P Exo      │
│  - Self-select  │   │  - Uncertainty  │    │  - Draft/Verify │   │  - Apple/NPU    │
└─────────────────┘   └─────────────────┘    └─────────────────┘   └─────────────────┘
```

### 1. Orchestration Paradigms
*   **Directed Acyclic Graphs (DAGs)**: Pre-compiled sequential/parallel steps. Highly predictable and deterministic, but rigid and unable to dynamically restructure on failure or semantic ambiguity. (Used in `tzro`'s Kahn Compiler).
*   **Event-Driven Runtimes**: Non-blocking asynchronous message brokers (e.g. Redis, Pub/Sub) allowing local agents to launch multiple remote/local sub-tasks in parallel and resume execution dynamically.
*   **Blackboard Systems**: Central shared memory store. Specialists read requests, compute, and write outcomes. No central coordinator requires hardcoded agent routing logic. Local agents remain stateless, saving prompt context size.

### 2. Autonomous Routing Layers
*   **In-Model RL Routers (GAPG)**: The SLM is trained to emit `think` (deliberate) and `route` (delegate) tokens based on runtime confidence. Optimized via the Group-Adaptive Policy Gradient (GAPG) algorithm using collaboration-aware rewards.
*   **Consistency-Aware Routers (ConsRoute)**: Evaluates representations across slight prompt perturbations to detect semantic divergence (high divergence = high uncertainty) and applies Bayesian optimization for thresholds.
*   **MHT-Bounded Risk**: Uses Multiple Hypothesis Testing (MHT) to set cost/correctness bounds, ensuring a target maximum error rate.

### 3. Distributed Speculative Decoding
*   **Disaggregated Speculative Pipeline**: Uses the local SLM as a token drafter and the remote cloud model as a verification target. Reduces token-by-token cloud generation overhead.
*   **Asynchronous Rejection Sampling (PicoSpec)**: Drafting and verification overlap in an asynchronous pipeline. Separate Rejection Sampling and sparse compression are applied to avoid transmitting full vocabulary probability distributions over the network.

### 4. Distributed State Reuse
*   **Hierarchical Semantic Caching**: Caches natural language queries based on cosine embedding similarity ($\ge 0.8$), bypassing LLM generation entirely for repeat queries (3-8ms response).
*   **Segment-Level KV Cache Sharing (CrossKV / RelayCaching)**: Programmatic extraction and position alignment (RoPE correction) of KV tensors between upstream and downstream agents. Reuses context even when prompt prefixes vary, speeding up TTFT by up to $4.7\times$.

---

## ⚖️ Architectural Gap Analysis: `tzro` vs. Bleeding-Edge

The table below contrasts `tzro`'s current architecture (as defined in [CONTEXT.md](../../CONTEXT.md) and ADRs) with the bleeding-edge concepts introduced in this research.

| Feature Area | Current `tzro` Implementation | Research Alternative | Integration Viability & Impact |
| :--- | :--- | :--- | :--- |
| **Execution Topology** | Compiles plans into a static, durable DAG using the **Kahn Compiler**; handles conditional edges via deterministic JSONPath evaluations. | **Blackboard System** or **Event-Driven Runtime** with self-selecting specialists. | **Medium**. Transitioning core `tzro` to a Blackboard architecture would require replacing the Kahn compiler with an event loop. However, an event-driven *Tactical* executor could be built to run within individual execution nodes. |
| **Routing Layer** | **Strategy-vs-Tactics (SCT)** split. Cloud Planner plans high-level steps; Local Model compiles GBNF and runs simple evaluations. | **In-Model RL Router (GAPG)** or **ConsRoute** uncertainty scoring. | **High**. We can train or configure the local LLM sidecar (e.g. Qwen-2.5-Coder) to evaluate its own execution capabilities, or implement representation divergence checking in the local sidecar client. |
| **Speculative Acceleration** | Autoregressive generation inside the local `llama.cpp` sidecar. | **Disaggregated Speculative Pipeline (PicoSpec)**. | **Low**. Requires tight synchronization, custom low-level model access, and high network bandwidth between client and cloud. |
| **Memory & Cache** | **5-Layer Context Compaction** (hoisting JSON arrays, pruning) and disk-backed SQLite JQ caching. | **CrossKV / RelayCaching** (segment-level KV sharing with RoPE alignment). | **Medium**. Can be integrated if the local sidecar engine (e.g., `llama.cpp` or Ollama) exposes API endpoints for segment-level KV tensor injection and manipulation. |
| **Local Specialization** | Static GGUF base models loaded into local sidecar processes. | **LoRA-Switch** token-wise adapter switching with fused CUDA kernels. | **Medium**. Can be simulated by loading multiple fine-tuned LoRA adapters into the local sidecar and dynamically toggling them via prompt prefixes or sidecar adapter APIs. |

---

## 🛠️ Potential Design Recommendations for `tzro`

To leverage these developments, future `tzro` enhancements should focus on the following:

1.  **Local Self-Assessment Routing**:
    *   Introduce an uncertainty-based fallback layer in the local runner. Before executing a tactical sub-task, the local model evaluates the prompt's semantic trajectory consistency. If the representation divergence exceeds a set threshold (bounded by MHT risk constraints), it bypasses local run and triggers a cloud planning fallback.
2.  **Segment-Level Cache Injection**:
    *   Extend the current `Disk-Backed JQ Cache` to cache intermediate KV tensors of frequently executed tools, injecting them directly into the local model's prompt context during downstream execution steps.
3.  **Dynamic LoRA Toggling**:
    *   Leverage multi-LoRA support in the local inference backend (e.g. Ollama/llama.cpp) to dynamically swap execution personas (e.g. `JSON-formatting`, `Bash-execution`, `RAG-extraction`) on a per-step basis without reloading base weights.
