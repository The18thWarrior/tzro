# ADR-0096: Migration to Jev-Style-0.8B and libllama Decision Engine

**Status**: Accepted  
**Date**: 2026-09-28  
**Supersedes**: ADR-0095 Section 1 (Laya/ModernBERT decision daemon)  

## Context

In ADR-0095, tzro adopted `mys/laya-GGUF` (ModernBERT-large, 421M params) supervised via a custom `laya` binary (`ggmlc`) over stdin/stdout IPC. While achieving sub-30ms categorical decisions, this setup incurred critical operational limits:
1. **Proprietary binary dependency**: Required distributing and maintaining a specialized `laya` executable built against the niche `ggmlc` runtime.
2. **512-token context ceiling**: ModernBERT's hard token limit forced aggressive state squashing ($\le 450$ tokens), strictly limiting triage evidence (capping inline diagnostics to 10 lines and candidate files to 5).
3. **Limited zero-shot generalization**: Laya struggled on unseen intent taxonomies and complex cross-file root-cause triage compared to modern instruction-tuned base models.

## Decision

We replace the `pkg/laya` subsystem with `pkg/decision` powered by **`chaoliangUNSW/Jev-Style-0.8B-Decision-v3`**:

1. **`libllama` Native Daemon (`bin/jev-score`)**: A standalone C++ binary built against standard upstream `llama.cpp` (`libllama`). Performs in-process prompt layout tokenization via `llama_tokenize()` with `special=false`, evaluates logits at verdict slots (`logit(" yes") - logit(" no")`), and outputs calibrated probabilities over a persistent stdin/stdout JSON-RPC protocol.
2. **Context Budget Expansion (2,048 Tokens)**: Jev-Style natively supports up to 25,600 tokens. We expand tzro's decision state budget from 450 to 2,048 tokens via `StateSquasher`, allowing rich multi-file skeletons (up to 1,000 tokens), expanded diagnostic failure logs (up to 50 lines), and up to 20 candidate probe matches.
3. **Pluggable Dual-Provider Architecture**: `pkg/decision` defines `DecisionProvider` supporting both `LocalDaemonProvider` (`bin/jev-score`) and `RemoteHTTPProvider` (dispatches to external Jev/SystemOne HTTP endpoints with Bearer auth).
4. **Clean Deprecation of Laya**: Completely retire `pkg/laya/`, `bin/laya`, `bin/laya_worker.py`, and `models/laya/`.

## Considered Options

- **Retaining Laya (`mys/laya-GGUF`)**: Rejected — context ceiling cannot be expanded, binary is tied to `ggmlc`, and zero-shot decision accuracy lags behind Jev-Style.
- **Python-Only Sidecar (`jev-style` / `tokenizers`)**: Rejected for local execution — adds Python virtual environment dependencies and slow startup for decisions. Kept local inference 100% native C++.
- **`llama-server` HTTP for Local Execution**: Evaluated — while viable, running `jev-score` directly over stdin/stdout provides lower step latency (<20ms vs 45ms) and deterministic memory management without HTTP server overhead.

## Consequences

- Decision state budget expands 4.5× (from 450 to 2,048 tokens).
- Zero-shot classification and bug triage accuracy increases significantly across benchmarks (Banking77, JevBench, multi-file code triage).
- Binary toolchain aligns with upstream `llama.cpp` (`libllama`), easily buildable from source or Homebrew on macOS and Linux.
- Resident memory footprint remains strictly bounded at ~800 MB RAM (Go ~50 MB + GLiNER ~220 MB + Jev Q4_K_M ~530 MB), fitting within the 870 MB budget.
- Legacy `pkg/laya` code is removed, eliminating naming and architectural debt.
