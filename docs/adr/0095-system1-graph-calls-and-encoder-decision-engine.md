# ADR-0095: System 1 Graph Calls and Non-Autoregressive Decision Engine

**Status**: Accepted  
**Date**: 2026-09-21  

## Context

In `tzro v1` (`819c5d6c`), the execution engine attempted to run autonomous agentic loops ("Thought Chains") inside macro-nodes using a dual small-model setup: a 4B Worker for reasoning and a 1B Router for GBNF action extraction. Across 13+ benchmark runs, this architecture exhibited high failure rates (90% tool tag emission failures, premature synthesis termination, format tax, and repetitive token degeneration loops) and excessive step latency (3.5s per turn), requiring fragile compensatory guardrails (DRY sampling, presence penalties, GBNF rescue).

## Decision

We replace autonomous generative loops inside nodes with **System 1 Graph Calls** executed over a deterministic declarative DAG runtime (`pkg/executor`), supported by an external System 2 Planner (cloud LLM) and an on-device non-autoregressive ML stack (Tier 2):

1. **System 1 Decision Daemon (`pkg/laya`)**: Supervise `mys/laya-GGUF` (ModernBERT-large, 421M params) via `ggmlc` daemon over stdin/stdout IPC. Evaluates typed questions (`choice`, `score`, `noul`) over bounded state (<= 450 tokens) in a single forward pass (~25 ms) without token generation.
2. **Zero-Shot Span Extractor (`pkg/extractor`)**: Supervise `GLiNER` (150M params) via ONNX Runtime over stdin/stdout IPC. Extracts exact parameter substrings from unstructured text using natural language semantic labels without generative hallucination.
3. **Dual Frontend**: Expose execution via `tzro mcp` (with structured modal authorization and `notifications/progress` streaming) for interactive IDE harnesses, alongside `tzro execute` for CLI and CI/CD pipelines.
4. **Substrate Integration**: Feed Laya decisions strictly through `v2.1.0` compaction and discovery primitives (`pkg/ast` skeletons, `pkg/compactor` 10-line diagnostic cap, and `pkg/probe` top-5 candidate rankings), keeping state strictly under 450 tokens.
5. **No Local Generative Codegen**: Reject local FIM/generative models (Tier 3). Strategy, creative synthesis, and code patch generation remain strictly owned by the cloud System 2 planner on yield turns.

## Considered Options

- **Autoregressive Small LLM Loop (v1 architecture)**: Rejected — small 1B–4B generative models lack calibration, suffer attention fatigue at ~400 tokens, and create format tax under grammar constraints.
- **In-Process Cgo Monolith**: Rejected — fragile cross-compilation across macOS Metal and Linux CUDA, and any C segfault crashes the host IDE session.
- **Tier 3 (Adding 0.5B FIM Coder)**: Rejected — elided code bodies are already losslessly retrievable from SQLite (`pkg/store`), and cloud coding harnesses reliably outperform small models for code patches.

## Consequences

- Decision latency drops from ~3,500 ms to ~25–35 ms per step (~45x speedup).
- Resident memory is strictly bounded and static at ~870 MB (zero dynamic KV-cache inflation).
- Eliminates the entire class of generative failure modes on decision seams (format tax, repetition loops, hallucinated candidate paths).
- Graph execution requires packaging `laya` and `gliner` daemon binaries and downloading model weights (~570 MB on disk).
