# Handoff: Dynamic LoRA Adapter Switching for tzro Local Inference

**Created**: 2026-06-05T08:53:00-07:00
**Purpose**: Explore and design a Dynamic LoRA Adapter Switching architecture for the `tzro` local inference sidecar, enabling the Local Model (Tactician) to hot-swap specialized execution personas per DAG node without reloading base weights.

---

## Context & Motivation

A research ingestion session analyzed bleeding-edge architectures for edge-cloud LLM task offloading. One of three high-value evolution paths identified for `tzro` is **Dynamic Adapter Switching** — using LoRA (Low-Rank Adaptation) to create a parameter-efficient local Mixture-of-Experts (MoE) where a single frozen base model routes its forward pass through specialized low-rank matrices.

The core problem: the `tzro` Local Model (Tactician) currently loads a single monolithic GGUF model (e.g., Qwen-3.5-Instruct 4B) and uses it for all tasks — JSON parameter extraction, code generation, conversational synthesis, RAG retrieval formatting. Training a monolithic model to excel at all of these leads to catastrophic forgetting or degraded niche performance. Loading multiple full models into VRAM is physically impossible on consumer edge hardware.

### Key Research Concepts to Explore

1. **LoRA-Switch** — Token-wise adapter switching with fused CUDA kernels. Swaps LoRA weights per token during generation. Custom kernel fuses merging operations into a single block, reducing decoding latency by >2.4x vs. standard dynamic adapters.

2. **LoRAX / S-LoRA** — Heterogeneous Continuous Batching. Packs requests for different LoRA adapters into a single inference batch on a shared base model. Prefetches adapter weights between GPU/CPU memory just-in-time.

3. **Temporal LoRA** — Lightweight gating networks (routers) in the forward pass that classify input context and route to the correct adapter (e.g., "Literature" adapter vs. "Coding" adapter) with near-100% accuracy.

---

## Current Architecture (What Exists)

### Local Model Manager
- **File**: `internal/inference/local_model.go`
- Manages a single `llama-server` child process loading one GGUF model
- Exposes `CallLocalModel` and `CallLocalModelStream` via OpenAI-compatible HTTP
- KV cache managed via `--cache-reuse 256`, slot save/restore for preemption
- Mode-dependent KV cache quantization (`q4_0` cooperative, `q8_0` local)

### Inference Routing
- **File**: `internal/inference/routing.go`
- `ExecuteStructured` routes between local/cloud based on `ModelMode` config
- Speed Floor Monitor: if generation drops below 5 t/s for 3 consecutive nodes, flips to cloud fallback for the rest of the task
- No per-node adapter selection — same model weights for every node type

### Executor System Prompt
- **File**: `internal/executor/executor_context.go`
- `buildContextAwareSystemPrompt` constructs per-node system prompts
- Every node gets the same base instruction prefix ("You are the Local Tactician Node Executor...")
- Schema and accumulated context are injected per-node, but the model weights are static

### Pluggable Inference Backend
- **ADR**: `docs/adr/0016-pluggable-inference-backend.md`
- Already decouples inference from the hosting process via `InferenceBackend` interface
- Supports `llama-server` sidecar, OpenAI-compatible remotes (Ollama, LMStudio, vLLM)
- This is the natural extension point for multi-adapter support

### Domain Glossary
- **File**: `CONTEXT.md`
- **Inference Backend**: Pluggable provider abstraction decoupling LLM inference from hosting process
- **Local Model**: Default-path workhorse handling all structured work
- No existing terms for adapter, LoRA, or execution persona concepts

---

## Key Design Questions for the Next Session

1. **Backend constraint**: Does `llama-server` (llama.cpp) support multi-LoRA hot-swapping natively? If not, should the adapter switching target Ollama (which has LoRA support) or vLLM (which has LoRAX-style batching) via the existing Pluggable Inference Backend?

2. **Adapter granularity**: Should adapters swap per-node (coarse — one adapter per DAG execution step), per-token (fine — LoRA-Switch style), or per-task-type (medium — one adapter for all GBNF extraction nodes, another for synthesis nodes)?

3. **Router placement**: Where does the adapter selection decision live?
   - In the Go executor (deterministic routing based on node type/action)?
   - In a lightweight gating network inside the model (Temporal LoRA)?
   - In the Cloud Planner (Strategist specifies adapter ID per node in the Abstract Graph)?

4. **VRAM budget**: How many adapters can coexist in memory simultaneously on target hardware (Apple Silicon 16-32GB unified memory, RTX 4090 24GB VRAM)?

5. **Domain terminology**: What should we call this in `CONTEXT.md`? Candidates: "Execution Persona", "Tactical Adapter", "Skill Adapter". Must not conflict with existing "Procedural Micro-Skill" (which is a Markdown SOP, not a weight modification).

6. **Interaction with KV cache**: If adapters switch between nodes, does the `--cache-reuse 256` prefix matching still work? Or does an adapter switch invalidate the cached KV state?

---

## Existing Documentation to Reference

- **Research Source**: `docs/wiki/sources/edge-cloud-task-offloading.md` — Section 5 covers LoRA-Switch, LoRAX, and Temporal LoRA mechanisms
- **Gap Analysis**: `docs/wiki/architecture/edge-cloud-co-orchestration.md` — Maps adapter switching to tzro's current architecture
- **Pluggable Backend ADR**: `docs/adr/0016-pluggable-inference-backend.md`
- **Domain Glossary**: `CONTEXT.md`
- **Cooperative Execution Spec**: `docs/cooperative-local-cloud-dag-execution.md`
- **Wiki Index**: `docs/wiki/index.md`

---

## Suggested Skills

- **`/grill-with-docs`** — Stress-test the adapter switching design against the existing domain model, sharpen terminology, and produce an ADR if the design involves hard-to-reverse trade-offs.
- **`/tdd`** — If implementation proceeds, use test-driven development to build the adapter selection router and backend integration.
- **`/prototype`** — Consider a throwaway prototype to validate whether `llama-server` or Ollama can hot-swap LoRA adapters without unacceptable latency.

---

## Out of Scope for This Handoff

The following topics were addressed in a parallel `/grill-with-docs` session and should NOT be re-explored:
- **Self-Assessing Local Routing** (GAPG-style confidence signals in the Tactician)
- **KV Cache Segment Sharing** (CrossKV / RelayCaching between DAG nodes)

These are being resolved in the active conversation (conversation ID: `6af2e57e-472b-45cd-8c21-f8f48e5072bf`).
