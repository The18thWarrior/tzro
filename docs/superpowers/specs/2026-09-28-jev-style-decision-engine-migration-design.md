# Design Spec: Jev-Style-0.8B Decision Engine Migration & libllama Integration

**Date**: 2026-09-28  
**Status**: Draft  
**Target Release**: tzro v3.2.0  
**Supersedes**: ADR-0095 Section 1 (`pkg/laya` ModernBERT engine)  

---

## 1. Executive Summary

In tzro v3.0, System 1 Graph Calls utilized `mys/laya-GGUF` (ModernBERT-large, 421M params) supervised by a dedicated `laya` binary (built from `monatis/ggmlc`). While achieving sub-30ms categorical decisions, this setup incurred critical operational constraints:
1. **Proprietary binary dependency**: Required distributing and maintaining a specialized `laya` executable built against the niche `ggmlc` runtime.
2. **Severe context budget (512 tokens)**: ModernBERT's 512-token ceiling required aggressive state squashing ($\le 450$ tokens), strictly limiting triage evidence (capping inline diagnostics to 10 lines and candidate files to 5).
3. **Limited zero-shot generalization**: Laya struggled on unseen intent taxonomies and complex cross-file root-cause triage compared to modern instruction-tuned base models.

This specification details the migration to **[chaoliangUNSW/Jev-Style-0.8B-Decision-v3](https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3)**:
* **Runtime**: Standalone C++ binary `bin/jev-score` built against standard upstream `llama.cpp` (`libllama`), featuring native in-process tokenization via `llama_tokenize()`.
* **Accuracy & Context**: State budget expanded 4.5× (from 450 to 2,048 tokens) while staying within a 25,600-token capable architecture; higher zero-shot accuracy across Banking77 (68.2% vs 49.2%), JevBench, and multi-file code triage.
* **Pluggable Architecture**: Introduces `pkg/decision` with a dual-provider model: local execution via `bin/jev-score` (default) or remote HTTP dispatch to external Jev/SystemOne services.
* **Memory & Latency Guarantees**: `Q4_K_M` GGUF footprint of ~530 MB RAM. Combined system footprint (Go ~50 MB + GLiNER ~220 MB + Jev ~530 MB) is **~800 MB RAM**, strictly within the 870 MB budget with $<30\text{ms}$ step latency on Metal/CUDA.

---

## 2. Architecture & Component Boundaries

```
┌─────────────────────────────────────────────────────────────┐
│                       pkg/executor                          │
│                (Deterministic DAG Engine)                   │
└──────────────────────────────┬──────────────────────────────┘
                               │ Decider Interface
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                       pkg/decision                          │
│  - DeciderAdapter: implements executor.Decider              │
│  - StateSquasher: budgets & compacts state (<= 2,048 tokens)│
│  - Provider Factory: selects Local vs Remote provider       │
└──────────────┬──────────────────────────────┬───────────────┘
               │ Local (default)              │ Remote (optional)
               ▼                              ▼
┌──────────────────────────────┐ ┌────────────────────────────┐
│      bin/jev-score           │ │ External Jev / SystemOne   │
│  (C++ on libllama / stdin)   │ │ HTTP REST API (POST /v1)   │
└──────────────────────────────┘ └────────────────────────────┘
```

### 2.1 Component Responsibilities

1. **`bin/jev-score` (Local Daemon)**:
   * Self-contained C++17 process linking `libllama.a`/`.dylib`/`.so`.
   * Accepts JSON-RPC requests over stdin; writes responses to stdout.
   * Performs prompt layout tokenization using `llama_tokenize()`.
   * Evaluates verdict slot logits (`logit(" yes") - logit(" no")`) in a single forward pass over a shared prefix KV state.
   * Emits calibrated probabilities using built-in temperature tables.
2. **`pkg/decision` (Go Engine Subsystem)**:
   * **`DecisionProvider`**: Common interface for decision evaluation.
     ```go
     type DecisionProvider interface {
         Evaluate(ctx context.Context, req *DecisionRequest) (*DecisionResponse, error)
         Close() error
     }
     ```
   * **`LocalDaemonProvider`**: Manages the child lifecycle of `bin/jev-score`, handling pipe drainage, health-checks, process restarts on broken pipes, and graceful teardown.
   * **`RemoteHTTPProvider`**: Dispatches requests to an external HTTP endpoint (`TZRO_DECISION_URL`) with Bearer token authentication.
   * **`DeciderAdapter`**: Adapts `executor.DecisionInput` into `DecisionRequest`, delegating to the configured provider and converting output to `executor.DecisionOutput`.
   * **`StateSquasher`**: Enforces a strict 2,048-token context ceiling across file skeletons, diagnostics, and probe matches before dispatch.
3. **Retired Components**:
   * Complete removal of `pkg/laya/`.
   * Removal of `bin/laya` and `bin/laya_worker.py`.
   * Deletion of `models/laya/` directory and GGUFs.

---

## 3. Protocol, Rendering & Verdict Readout

### 3.1 JSON-RPC Protocol (stdin/stdout)

* **Handshake Line (Daemon $\rightarrow$ Go)**:
  Upon loading weights into memory, `jev-score` writes:
  ```json
  {"status":"ready","model":"Jev-Style-0.8B-Decision-v3-Q4_K_M","device":"metal","vocab_size":152064}
  ```
* **Request Line (Go $\rightarrow$ Daemon)**:
  ```json
  {
    "question_type": "choice",
    "prompt": "Which file is the primary root cause of this failure?",
    "options": ["pkg/proxy/proxy.go", "pkg/store/store.go", "cmd/tzro/main.go"],
    "state": {
      "error": "panic: nil pointer dereference in proxy.go:142",
      "target_file": "pkg/proxy/proxy.go"
    },
    "category": "theme_routing"
  }
  ```
* **Response Line (Daemon $\rightarrow$ Go)**:
  ```json
  {
    "answer": "pkg/proxy/proxy.go",
    "confidence": 0.9412,
    "scores": {
      "pkg/proxy/proxy.go": 0.9412,
      "pkg/store/store.go": 0.0451,
      "cmd/tzro/main.go": 0.0137
    },
    "latency_ms": 18
  }
  ```

### 3.2 Native Prompt Formatting (`macjev-render-v1`)

The daemon formats the prompt according to the Jev-Style specification:

```text
State:
<JSON-serialized state>

Question [<type>]: <prompt text>
Options:
- <option 1>
- <option 2>
Judge each option:
<option 1> ->
<option 2> ->
```

* **Tokenization**: Segmented through `llama_tokenize()` with `special=false` to prevent user strings from injecting tokenizer control tokens.
* **Verdict Slots**: The token position of each ` ->` suffix is tagged as that option's verdict slot.
* **Logits Evaluation**:
  $$\text{score}_k = \text{logit}_k(\text{" yes"}) - \text{logit}_k(\text{" no"})$$
* **Softmax with Calibration**:
  $$P(\text{option}_k) = \frac{\exp(\text{score}_k / T)}{\sum_j \exp(\text{score}_j / T)}$$
  Where $T$ is looked up by category family from the calibration map (default global $T = 0.880$).

### 3.3 Question Types

1. **`choice`**: Categorical selection across 2–20 discrete options.
2. **`noul`**: Binary boolean verification (`yes` vs `no`). Threshold $\ge 0.5 \rightarrow \text{"yes"}$.
3. **`score`**: Ordered levels (e.g. 1 to 5). Expected value computed from normalized level probabilities.

---

## 4. Context Squashing & Memory Management

### 4.1 Budget Allocation (2,048 Tokens)

| Priority | Content Component | Source Package | Max Token Budget | Truncation Strategy |
| :--- | :--- | :--- | :--- | :--- |
| **P1** | Question & Options | `pkg/decision` | ~150 tokens | Never truncated; raises error if $>500$ |
| **P2** | Diagnostic Failure Evidence | `pkg/compactor` | ~500 tokens | 50-line head/tail diagnostic cap |
| **P3** | Target Skeletons / AST | `pkg/ast` | ~1,000 tokens | Method bodies elided to `#hash` tags |
| **P4** | Probe Matches & Callers | `pkg/context` | Remainder ($\approx 398$ tokens) | Top-20 candidates ranked by BM25 |

### 4.2 Resident Memory Guard

* **Model File**: `models/decision/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf` (530 MB on disk).
* **KV-Cache Sizing**: Context size parameter fixed to `n_ctx = 2048` with `n_batch = 1024`.
* **RAM Profile**:
  * tzro Go core: ~50 MB
  * GLiNER ONNX worker: ~220 MB
  * Jev-Style daemon: ~530 MB
  * **Total Static RAM**: **~800 MB** ($\le 870\text{ MB}$ architectural ceiling).

---

## 5. Dual-Provider Model & Configuration

Users configure decision execution via `.tzro/config.json` or environment variables:

| Environment Variable | Config Key | Default | Description |
| :--- | :--- | :--- | :--- |
| `TZRO_DECISION_PROVIDER` | `decision.provider` | `"local"` | `"local"` (daemon) or `"remote"` (HTTP) |
| `TZRO_DECISION_BIN` | `decision.bin_path` | `~/.tzro/bin/jev-score` | Path to local `jev-score` binary |
| `TZRO_DECISION_MODEL` | `decision.model_path` | `~/.tzro/models/decision/...` | Path to GGUF weights |
| `TZRO_DECISION_URL` | `decision.remote_url` | `""` | Base URL for remote Jev/SystemOne provider |
| `TZRO_DECISION_API_KEY` | `decision.api_key` | `""` | Bearer token for remote HTTP provider |

---

## 6. Installation & Verification Pipeline

### 6.1 `install.sh` Adjustments
1. **Target Binaries**:
   * macOS: Download `jev-score-darwin-arm64` $\rightarrow$ `${INSTALL_DIR}/bin/jev-score`.
   * Linux: Download `jev-score-linux-x86_64` (or `arm64`) $\rightarrow$ `${INSTALL_DIR}/bin/jev-score`.
2. **Target Weights**:
   * Fetch `Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf` from Hugging Face / release mirror to `${INSTALL_DIR}/models/decision/`.
3. **Cleanup**:
   * Automatically remove legacy `${INSTALL_DIR}/bin/laya` and `${INSTALL_DIR}/models/laya/`.

### 6.2 `tzro doctor` Health Checks
* **Local Provider**:
  1. Verify `bin/jev-score` exists, has executable permissions, and dynamic libraries load.
  2. Verify `Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf` file exists and passes size/hash verification.
  3. Dispatch synthetic handshake and `noul` probe test. Assert latency $< 50\text{ms}$ and confidence in $[0, 1]$.
* **Remote Provider**:
  1. Ping `${TZRO_DECISION_URL}/health` or test endpoint.
  2. Validate Bearer authentication.

---

## 7. Migration Plan & Documentation

1. **ADR-0096**: Author `docs/adr/0096-migration-to-jev-style-and-libllama-decision-engine.md`, documenting the deprecation of Laya and adoption of Jev-Style.
2. **Proactive QA Specs**:
   * Rename `tests/llm/proactive-qa/uc-laya-decisions.md` to `tests/llm/proactive-qa/uc-system1-decisions.md`.
   * Update route and backend references from `pkg/laya` to `pkg/decision`.
3. **Wiki & Context Updates**:
   * Update `docs/wiki/index.md` and `docs/wiki/log.md`.
   * Update `AGENTS.md` and `CONTEXT.md` references to decision inference.
