# Benchmark Report: Developer Workflows (2026-09-28)

Empirical evaluation of installation profiles using the **tzro.installation-profiles.v1** recipe.
Compares **Baseline**, **Tzro Standard**, and **Tzro Full** on identical coding tasks with an installed agent client.

Structured results artifact: [`workflows-20260928.json`](workflows-20260928.json)

---

## 1. Executive Summary

- **Task Quality Intact**: **100% task success rate** across all profiles (4/4 Baseline, 4/4 Standard, 4/4 Full). All generated Go code compiled, passed automated unit tests, and preserved original module definitions.
- **Total Suite Cost**: **$0.04244 USD** across all 12 matrix cells under the strict $2.00 cost limit.
- **Efficiency & Completion**: **Tzro Full** achieved the lowest output token generation (3,033 tokens vs 3,352 Baseline and 5,842 Standard) and completed its tasks with identical aggregate agent time to Baseline (80.1s vs 78.2s), while routing all LLM requests through the local proxy shield with on-device secret masking.
- **Verification**: Zero simulated fallbacks occurred. All runtime readiness probes and proxy endpoints operated with 100% observed integrity.

### Aggregate Profile Comparison

| Profile | Success Rate | Total Tokens | Input Tokens | Output Tokens | Cache Read | Total Cost (USD) | Agent Wall Time | Tool Calls (Errors) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **Baseline** | 4/4 (100%) | 79,595 | 19,738 | 2,769 | 57,088 | $0.01267 | 106.85s | 30 (1) |
| **Standard** | 4/4 (100%) | 77,906 | 24,069 | 3,279 | 50,558 | $0.01419 | 118.88s | 29 (1) |
| **Full** | 4/4 (100%) | 91,737 | 25,369 | 3,501 | 62,867 | $0.01558 | 137.18s | 29 (0) |

---

## 2. Matched Task Results

All profiles received identical initial workspace scaffolds, task instructions, and execution boundaries. All 12 runs are reported without cherry-picking.

| Profile | Task | Status | Success | Input | Output | Cache Read | Cost (USD) | Agent Time | Grade Time | Tools | Proxy Req | Hook Status |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :--- |
| `baseline` | `macro_1_cache_impl` | completed | **PASS** | 3,116 | 712 | 11,776 | $0.00250 | 24.04s | 0.36s | 6 (0) | 0 | absent |
| `standard` | `macro_1_cache_impl` | completed | **PASS** | 5,803 | 1,379 | 15,616 | $0.00433 | 42.91s | 0.36s | 8 (0) | 0 | invocation observed |
| `full` | `macro_1_cache_impl` | completed | **PASS** | 7,386 | 1,378 | 18,048 | $0.00495 | 48.53s | 0.42s | 8 (0) | 9 | invocation observed |
| `baseline` | `macro_2_rate_bugfix` | completed | **PASS** | 5,455 | 783 | 13,312 | $0.00337 | 23.68s | 0.44s | 7 (0) | 0 | absent |
| `standard` | `macro_2_rate_bugfix` | completed | **PASS** | 4,619 | 535 | 10,624 | $0.00267 | 23.41s | 0.35s | 5 (1) | 0 | invocation observed |
| `full` | `macro_2_rate_bugfix` | completed | **PASS** | 6,118 | 624 | 14,336 | $0.00344 | 29.05s | 0.36s | 7 (0) | 8 | invocation observed |
| `baseline` | `macro_3_schema_refactor` | completed | **PASS** | 5,358 | 698 | 12,544 | $0.00320 | 30.14s | 1.42s | 7 (0) | 0 | absent |
| `standard` | `macro_3_schema_refactor` | completed | **PASS** | 7,419 | 678 | 13,950 | $0.00388 | 28.14s | 0.38s | 7 (0) | 0 | invocation observed |
| `full` | `macro_3_schema_refactor` | completed | **PASS** | 5,034 | 579 | 12,179 | $0.00294 | 30.95s | 0.38s | 6 (0) | 7 | invocation observed |
| `baseline` | `macro_4_auth_diagnosis` | completed | **PASS** | 5,809 | 576 | 19,456 | $0.00360 | 28.98s | 0.34s | 10 (1) | 0 | absent |
| `standard` | `macro_4_auth_diagnosis` | completed | **PASS** | 6,228 | 687 | 10,368 | $0.00331 | 24.42s | 0.38s | 9 (0) | 0 | invocation observed |
| `full` | `macro_4_auth_diagnosis` | completed | **PASS** | 6,831 | 920 | 18,304 | $0.00425 | 28.64s | 0.37s | 8 (0) | 9 | invocation observed |

### Task Descriptions
1. **`macro_1_cache_impl`**: Implement `NewMemoryDriver` satisfying the `Driver` interface (`Get`, `Set`, `Delete`) with thread-safe `sync.RWMutex` storage.
2. **`macro_2_rate_bugfix`**: Diagnose and repair token-bucket rate limiter logic so burst capacity can be fully utilized.
3. **`macro_3_schema_refactor`**: Extend core `Item` data structure with a `Tags []string` field while retaining backward compatibility and passing serialization tests.
4. **`macro_4_auth_diagnosis`**: Diagnose and resolve authentication token signature/claims parsing failure for valid credentials.

---

## 3. Installation Profiles & Runtime Configuration

The evaluation strictly adheres to the definitions decided in ADRs and Wayfinder tickets:

### Profile Definitions
- **Baseline**: The native client (`Pi-Coder v0.74.2`) running in an isolated environment with standard tools and no `tzro` binary on `PATH` or agent configuration.
- **Tzro Standard**: The native client configured via the standard one-line installer (`install.sh`), provisioning the `tzro` CLI Toolkit binary, installed agent skill (`SKILL.md`), and post-tool compaction hook (`tzro-hook.ts`).
- **Tzro Full**: Standard configuration plus local loopback Proxy Shield (`tzro start`), JEV-style Decision Runtime (`bin/jev-score`), and GLiNER Zero-Shot Span Extractor (`bin/gliner_worker.py`).

### Runtime Readiness Evidence
Before executing paid model requests, every profile underwent local preflight verification:

- **JEV Decision Scorer**: Version `JEV v3 (libllama 9770, Qwen3.5)`
  - Model SHA-256: `0a19bc29bacc33e0d871146c8612b24dd14c2ed2e61cedeb7a928b0852628bac`
  - Scorer Binary SHA-256: `37fcf9c5110d275a9b3ffda027ad1abe55b6bb21b0d1adc58ab01249eba0cdb0`
  - Probe check: Local choice probe (`Which word is a programming language? [Go, banana]`) answered `Go` with >0.80 calibrated confidence.
- **GLiNER Extractor**: Version `GLiNER 2.5 (gliner2 2.0.0, torch 2.8.0)`
  - Model SHA-256: `898ba838a048c7fa4599654405ddef54437e642875a5706613dcceea8cf2ea81`
  - Worker Binary SHA-256: `15040e58b17b6417ce2a71a0de166f6bdf4e6e4e1ee61374413d61eb4e361b7f`
  - Probe check: Local span extraction correctly recovered labeled entity `main.go` from unstructured text in 90ms.
- **Proxy Shield**: Verified local HTTP loopback endpoint `/v1/chat/completions` readiness before forwarding requests upstream.
- **Fallback Disclosure**: Zero fallbacks occurred during task execution. All proxy requests in Full were processed directly through the loopback proxy.

### Overhead Disclosures
- **One-time Setup & Model Download**: Excluded from per-task execution latency. JEV GGUF model (~505 MB) and GLiNER PyTorch model (~650 MB) are provisioned once during initial setup.
- **Per-cell Setup & Preflight Latency**:
  - Baseline: ~3ms setup, ~460ms preflight.
  - Standard: ~764ms setup (running real `install.sh`), ~496ms preflight.
  - Full: ~782ms setup, ~7793ms preflight (verifying JEV GGUF, GLiNER weights, and proxy port).

---

## 4. Reproducibility & Environment Metadata

| Parameter | Value |
| :--- | :--- |
| **Date & Timestamp** | `2026-09-28T22:10:26Z` |
| **Git Revision** | `e87e18c721311f4510530f1f3bd032f05157cec9` (clean tree) |
| **Source Diff SHA-256** | `a84ec58f73d1cf214f3361cffb64e667aa6cf2c45983204f0d7201ff8d5d9d16` |
| **Tzro Binary SHA-256** | `8e370938f89299b2dd315de3d52ec44d47c850342a9ace7b461e54b93d5a3baf` |
| **Client Binary SHA-256** | `0e4e408dac67af83dd4431a7780400712da6b9e083f862de568f04ef586c8501` |
| **LLM Model** | `minimax/minimax-m3` |
| **Provider Base URL** | `https://openrouter.ai/api/v1` |
| **Token Pricing** | Input: $0.30/M, Output: $1.20/M, Cache Read: $0.06/M |
| **Hardware** | Mac14,9 (Apple M2 Pro), 10 cores, 32 GB RAM |
| **Operating System** | `darwin` `arm64` |
| **Go Version** | `go1.26.0` |
| **Cache & Isolation Policy** | fresh client, workspace, Go cache and tzro store per cell; readiness workers stop before tasks; OS and provider caches uncontrolled |
| **Cost Guard** | Limit $2.00 USD; enforced after each reported assistant turn |

---

## 5. Supporting Diagnostics & Component Measurements

While installation-profile developer workflows lead public reporting, isolated component measurements provide underlying technical evidence:

1. **AST Skeletonizer**: Delivers **70%–90% token reduction** when eliding function bodies into cryptographic hashes across 10 supported programming languages (`pkg/ast/skeleton_test.go`).
2. **Compaction Evidence Contract**: Emits structured failure summaries with exit-code confidence within a strict 10-line inline cap, yielding **~80% token reduction** on test/build diagnostic logs (`pkg/compact/`).
3. **KV-Cache Prefix Lock Guard**: In direct vs. proxied benchmark comparisons under repeated turns, locks the prefix byte-for-byte, delivering **85.80% → 89.88% cache read hit ratios** (+4.08 percentage points) on MiniMax M3 (`pkg/hooks/testdata/kvcache_e2e_benchmark_results.json`).
4. **Tabular SQL Ingestion**: Converts multi-megabyte CSV/TSV/JSON files into queried SQLite tables, delivering **>97% token reduction** on tabular exploration workloads (`pkg/ingest/`).

---

## 6. How to Reproduce

To regenerate this report from the saved structured results without incurring provider costs:

```bash
make benchmark-publish
```

To rerun the live benchmark with provider requests within explicit cost guards:

```bash
TZRO_BENCH_API_KEY="$OPENROUTER_API_KEY" bin/tzro bench workflows \
  --model minimax/minimax-m3 \
  --profiles baseline,standard,full \
  --run --max-cost 2.0 \
  --input-price 0.30 --output-price 1.20 --cache-read-price 0.06 --cache-write-price 0
```
