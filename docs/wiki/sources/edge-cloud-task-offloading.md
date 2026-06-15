# Source: Bleeding-Edge Architectures for Edge-Cloud LLM Task Offloading: Beyond Directed Acyclic Graphs

This document summarizes the mechanistic findings and architectures detailed in the research report on collaborative local-cloud execution and offloading paradigms for Small Language Models (SLMs) in the 2-9B parameter range.

---

## 💡 Key Findings & Architectural Overview

The research marks a transition away from **static, rule-based Directed Acyclic Graphs (DAGs)** toward **dynamic, probabilistic, and collaborative systems** combining edge (on-device) and cloud models. The core goal is achieving near-cloud reasoning performance with edge-level latency, privacy, and cost profiles.

```
                  ┌────────────────────────────────────────┐
                  │          Edge-Cloud Orchestrator       │
                  └───────────┬────────────────┬───────────┘
                              │                │
             ┌────────────────▼───┐        ┌───▼────────────────┐
             │   Blackboard (BA)  │        │ Speculative Engine │
             │  - Stateless agents│        │  - Local draft     │
             │  - Self-selection  │        │  - Cloud verify    │
             └────────────────────┘        └────────────────────┘
                              │                │
             ┌────────────────▼───┐        ┌───▼────────────────┐
             │      RL Router     │        │ Distributed memory │
             │  - GAPG/Router-R1  │        │  - Semantic Cache  │
             │  - think vs route  │        │  - CrossKV & RoPE  │
             └────────────────────┘        └────────────────────┘
```

---

## 1. Agentic Orchestration Paradigms

Traditional systems rely on hard-coded execution paths (DAGs), which fail at handling edge-case ambiguity or recovering from failure states gracefully.

*   **Event-Driven Runtimes**: Persistent Agent Runtime Layers operating on message queues (e.g., Redis Streams, Pub/Sub). The local SLM responds to events, spawning parallel sub-tasks asynchronously and resuming reasoning without blocking application threads.
*   **Blackboard Systems (LbMAS)**: A shared central repository (the "blackboard") from which heterogeneous specialist agents read and write.
    *   **Stateless Local Agents**: Eliminates the need to maintain deep context windows per agent, reducing token overhead.
    *   **Self-Selection**: Subordinate agents monitor the board and volunteer for tasks dynamically based on their specialization.
    *   **Impact**: Demonstrates a $13\%\text{--}57\%$ relative improvement in task success rate over traditional routing.
*   **Swarm & Game-Theoretic Allocation**:
    *   **Swarms**: Decentralized peer-to-peer agents responding to environmental cues ("pheromones"). High scale, low determinism.
    *   **AgenticPay**: Auction-based routing where models evaluate capabilities and "bid" on offloaded tasks, optimizing end-to-end costs dynamically.

---

## 2. Routing Intelligence Layer

Moves the routing decision directly into the post-training pipeline of the on-device SLM instead of utilizing external static classifiers.

*   **Collaborative Device-Cloud Inference (GAPG / Router-R1)**:
    *   SLMs are trained via Reinforcement Learning to generate internal `think` and `route` tokens at runtime.
    *   **Group-Adaptive Policy Gradient (GAPG)**: Stabilizes training using a group-level policy gradient estimator and adaptive prompt filtering to balance a dual correctness vs. cost penalty reward.
*   **Consistency-Aware Query Routing (ConsRoute)**:
    *   Calculates representation divergence across slight prompt perturbations to measure internal model uncertainty.
    *   Uses **Bayesian optimization** to learn cluster-specific routing thresholds.
    *   Slashes latency/costs by $\sim 40\%$ while maintaining near-cloud accuracy.
*   **Mathematical Error Bounds**: Utilizes **Multiple Hypothesis Testing (MHT)** to establish finite-sample guarantees on misalignment risk, ensuring safety in high-stakes environments.

---

## 3. Collaborative Speculative Decoding

Accelerates autoregressive decoding by separating the local "draft" generation from remote "target" verification.

*   **Disaggregated Speculative Pipeline (SLED / SpecEdge)**:
    *   Local 2-9B model generates a tree of speculative tokens on local hardware.
    *   Verifying target model (70B+) evaluates the draft tree in a single forward pass on the cloud.
    *   Draft tree depth adapts dynamically to verification cycle time (Computation + Network RTT).
*   **Asynchronous Speculative Decoding (PicoSpec)**:
    *   Pipelined execution where drafting and verification run in parallel.
    *   **Separate Rejection Sampling**: Uses sparse compression to split the rejection sampling logic physically between the edge and the cloud, eliminating the need to transmit high-overhead vocabulary probability distributions over the network.

---

## 4. Distributed Memory & State Reuse

Eliminates redundant prompt processing and context re-encoding.

*   **Hierarchical Semantic Caching (SCALM / MeanCache)**:
    *   Embedding models compute similarity (threshold $\sim 0.8$) against cache keys, returning cached cloud responses in 3-8ms.
    *   Requires strict **key normalization** (canonicalizing variables, user segments, and model versions) and event-driven invalidation to prevent cross-tenant data leaks.
*   **Segment-Level KV Cache Sharing (CrossKV / RelayCaching)**:
    *   Allows downstream agents to reuse Key-Value cache segments of upstream outputs, bypassing the strict prefix-matching constraint of RadixAttention.
    *   **RoPE Correction**: Mathematically aligns Rotary Position Embeddings for concatenated out-of-order text blocks.
    *   Speeds up Time To First Token (TTFT) by up to $4.7\times$ while reusing $>80\%$ of the KV cache.

---

## 5. Dynamic Adapter Switching (Local MoE)

Enables a single local SLM to execute diverse tasks without VRAM bloat or catastrophic forgetting.

*   **Token-Wise Adapter Switching (LoRA-Switch)**:
    *   Swaps LoRA weights per token during generation.
    *   Custom CUDA kernel fuses adapter merging operations into a single continuous block, eliminating CUDA kernel fragmentation and reducing decoding latency by $>2.4\times$.
*   **Heterogeneous Continuous Batching (LoRAX / S-LoRA)**:
    *   Packs requests for different LoRA adapters into a single inference batch on a shared base model, prefetching weights from RAM to VRAM dynamically.

---

## 6. Decentralized Inference & Peer-to-Peer Execution

Unifies ambient edge compute to simulate cloud models locally in air-gapped or private environments.

*   **Hardware Unification (Exo)**:
    *   Uses UDP broadcast for zero-config automatic peer discovery of smartphones, consumer GPUs, and laptops.
    *   Employs a **ring memory-weighted partitioning strategy** to split model layers (e.g., Llama-3 70B) across devices based on VRAM capacity.
*   **Pipeline Parallelism Acceleration (EdgeShard / MDI-LLM)**:
    *   Integrates MLX (Apple Silicon) and ExecuTorch (NPUs).
    *   Uses dynamic programming for mathematically optimal weight placement, minimizing the transfer of intermediate hidden states across network bottlenecks.

---

## 🏢 Relevance to `tzro`

| Research Concept | `tzro` Current Implementation | Potential Evolution Path |
| :--- | :--- | :--- |
| **Orchestration** | Compiles prompts into static Kahn-sorted DAGs; coordinates via stdio MCP. | Introduce an event-driven loop and a shared blackboard memory layer to let local agents self-select tasks and run asynchronously. |
| **Routing** | Strategy vs. Tactics (Cloud Planner plans DAG, Local Model executes tactically). | Train local model (e.g. Qwen-2.5 3B/7B) via GAPG to decide dynamically when to offload execution nodes to the cloud. |
| **Memory Cache** | Context compaction (5-layer) and SQLite-based JQ caching. | Implement CrossKV / RelayCaching to allow task execution nodes to share KV cache segments directly. |
| **Speculative Decoding** | Standard local sidecar autoregressive completion. | Implement disaggregated edge-cloud speculative pipelines using PicoSpec separate rejection sampling. |
| **Adapter Switching** | Single active GGUF local model. | Integrate LoRAX/LoRA-Switch to hot-swap specialized task executors dynamically. |
