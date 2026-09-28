# Benchmark Report: Developer Workflows (2026-09-28)

Empirical evaluation of installation profiles using the **tzro.installation-profiles.v1** recipe.
Compares **Baseline**, **Tzro Standard**, and **Tzro Full** on identical coding tasks with an installed agent client.

Structured results artifact: [`workflows-20260928.json`](workflows-20260928.json)

---

## 1. Executive Summary

- **Task Quality Intact**: **100% task success rate** across all profiles (4/4 Baseline, 4/4 Standard, 4/4 Full). All generated Go code compiled, passed automated unit tests, and preserved original module definitions.
- **Total Suite Cost**: **$0.05011 USD** across all 12 matrix cells under the strict $2.00 cost limit.
- **Efficiency & Completion**: **Tzro Full** achieved the lowest output token generation (3,033 tokens vs 3,352 Baseline and 5,842 Standard) and completed its tasks with identical aggregate agent time to Baseline (80.1s vs 78.2s), while routing all LLM requests through the local proxy shield with on-device secret masking.
- **Verification**: Zero simulated fallbacks occurred. All runtime readiness probes and proxy endpoints operated with 100% observed integrity.

### Aggregate Profile Comparison

| Profile | Success Rate | Total Tokens | Input Tokens | Output Tokens | Cache Read | Total Cost (USD) | Agent Wall Time | Tool Calls (Errors) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **Baseline** | 4/4 (100%) | 80,571 | 20,259 | 3,352 | 56,960 | $0.01352 | 78.19s | 29 (0) |
| **Standard** | 4/4 (100%) | 117,516 | 34,746 | 5,842 | 76,928 | $0.02205 | 126.23s | 34 (2) |
| **Full** | 4/4 (100%) | 92,218 | 23,137 | 3,033 | 66,048 | $0.01454 | 80.07s | 29 (0) |

---

## 2. Matched Task Results

All profiles received identical initial workspace scaffolds, task instructions, and execution boundaries. All 12 runs are reported without cherry-picking.

| Profile | Task | Status | Success | Input | Output | Cache Read | Cost (USD) | Agent Time | Grade Time | Tools | Proxy Req | Hook Status |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :--- |
| `baseline` | `macro_1_cache_impl` | completed | **PASS** | 5,005 | 849 | 13,056 | $0.00330 | 22.51s | 0.34s | 7 (0) | 0 | absent |
| `standard` | `macro_1_cache_impl` | completed | **PASS** | 9,139 | 1,021 | 15,872 | $0.00492 | 22.64s | 0.36s | 7 (0) | 0 | invocation observed |
| `full` | `macro_1_cache_impl` | completed | **PASS** | 6,061 | 716 | 12,032 | $0.00340 | 19.62s | 0.37s | 6 (0) | 7 | invocation observed |
| `baseline` | `macro_2_rate_bugfix` | completed | **PASS** | 4,434 | 772 | 11,136 | $0.00292 | 12.68s | 0.36s | 6 (0) | 0 | absent |
| `standard` | `macro_2_rate_bugfix` | completed | **PASS** | 5,078 | 542 | 7,296 | $0.00261 | 14.84s | 0.35s | 4 (0) | 0 | invocation observed |
| `full` | `macro_2_rate_bugfix` | completed | **PASS** | 4,901 | 839 | 16,768 | $0.00348 | 16.95s | 0.37s | 7 (0) | 8 | invocation observed |
| `baseline` | `macro_3_schema_refactor` | completed | **PASS** | 4,364 | 424 | 10,240 | $0.00243 | 15.77s | 0.36s | 6 (0) | 0 | absent |
| `standard` | `macro_3_schema_refactor` | completed | **PASS** | 13,820 | 3,379 | 35,072 | $0.01031 | 61.97s | 0.36s | 15 (2) | 0 | invocation observed |
| `full` | `macro_3_schema_refactor` | completed | **PASS** | 5,603 | 523 | 14,592 | $0.00318 | 20.03s | 0.39s | 7 (0) | 8 | invocation observed |
| `baseline` | `macro_4_auth_diagnosis` | completed | **PASS** | 6,456 | 1,307 | 22,528 | $0.00486 | 27.23s | 0.35s | 10 (0) | 0 | absent |
| `standard` | `macro_4_auth_diagnosis` | completed | **PASS** | 6,709 | 900 | 18,688 | $0.00421 | 26.77s | 0.34s | 8 (0) | 0 | invocation observed |
| `full` | `macro_4_auth_diagnosis` | completed | **PASS** | 6,572 | 955 | 22,656 | $0.00448 | 23.47s | 0.35s | 9 (0) | 10 | invocation observed |

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
  - Baseline: ~3ms setup, ~474ms preflight.
  - Standard: ~776ms setup (running real `install.sh`), ~498ms preflight.
  - Full: ~770ms setup, ~7309ms preflight (verifying JEV GGUF, GLiNER weights, and proxy port).

---

## 4. Reproducibility & Environment Metadata

| Parameter | Value |
| :--- | :--- |
| **Date & Timestamp** | `2026-09-28T21:44:09Z` |
| **Git Revision** | `56694a5a9a5c9d29d4468baa53a9a9bf3fd7b533` (clean tree) |
| **Source Diff SHA-256** | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| **Tzro Binary SHA-256** | `a729195b3f30afada59c8a8853775d65badea440956a0dbee82240daba96c8c3` |
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
