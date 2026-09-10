# Signal Density Per Token Benchmark — Design Spec

> **Status**: Approved — pending implementation plan  
> **Date**: 2026-09-09  
> **Scope**: Benchmark suite and CLI command measuring task-completion signal density per token across Tzro optimizations  

---

## 1. Problem & Motivation

Tzro provides several native optimizations that aggressively reduce token volume:
- **AST Skeletonization** (`tzro skeleton`): 70–90% reduction via syntax-aware function body elision.
- **Log Compaction** (`tzro compact`): 80% reduction via runtime stack trace elision.
- **Smart JSON Crusher**: 60–80% reduction by converting uniform JSON arrays to Markdown tables.
- **Tabular Ingestion** (`tzro ingest` / `tzro query`): 97%+ reduction via SQL envelope pointers.

Historically, Tzro's benchmarks have focused on **transport plumbing and token counting**:
- Warm KV-cache prefix hit ratios (`pkg/proxy/kvcache_bench_test.go`).
- Raw token count drops and byte compression percentages.
- Hook latency and memory allocations.

However, token reduction alone is insufficient if the transformations strip essential semantics needed by the underlying LLM. Conversely, when transformations eliminate distracting boilerplate (goroutine dumps, JSON punctuation, repetitive imports), models often reason *better* and hallucinate *less*.

**Goal**: Establish an automated, repeatable benchmark suite that proves Tzro optimizations increase **Signal Density**—empirically verifying that the model achieves equal or superior task accuracy while ingesting substantially fewer tokens.

---

## 2. Metric Formalism: Signal Density Multiplier ($SDM$)

For any test task $i$ evaluated across two conditions:
- **Condition $R$ (Raw Baseline)**: Uncompressed source code, raw stack traces, verbose JSON, or raw CSV dumps.
- **Condition $T$ (Tzro Optimized)**: Context processed by Tzro's native transforms.

### 2.1 Core Metrics

1. **Accuracy / Pass Rate ($A \in [0, 1]$)**:
   - For micro-tasks: Binary match (1 = correct, 0 = incorrect) against ground-truth facts, error origins, or extraction targets.
   - For macro-tasks: Unit test execution (`go test ./...` passing = 1, failing = 0).

2. **Input Tokens ($K$)**:
   - Total billed prompt/context tokens reported by the provider API for the task query.

3. **Signal Retention ($SR$)**:
   $$\text{SR} = \frac{\sum_{i=1}^N A_{T, i}}{\sum_{i=1}^N A_{R, i}}$$
   *(If baseline fails a task where $A_{R, i}=0$ and $A_{T, i}=1$, $SR$ is credited proportionally without division-by-zero).*

4. **Token Compression Ratio ($CR$)**:
   $$\text{CR} = \frac{\sum_{i=1}^N K_{R, i}}{\sum_{i=1}^N K_{T, i}}$$

5. **Signal Density Multiplier ($SDM$)**:
   $$\text{SDM} = \text{SR} \times \text{CR}$$

### 2.2 Aggregation & Interpretation

- **Per-Component $SDM$**: Computed separately for each optimization primitive (`skeleton`, `compactor`, `json`, `tabular`, `macro`).
- **Composite $SDM$**: Geometric mean across all component $SDM$ scores:
  $$\text{Composite SDM} = \left( \prod_{c=1}^M \text{SDM}_c \right)^{\frac{1}{M}}$$
- **Interpretation Guide**:
  - $\text{SDM} > 1.0$: Net positive information density. The optimization saved tokens faster than any loss in accuracy.
  - $\text{SDM} \ge \text{CR}$ (with $\text{SR} \ge 1.0$): Perfect signal preservation with pure token savings, or improved accuracy from noise reduction.
  - $\text{SDM} < 1.0$: Critical failure / signal destruction. Compaction dropped essential facts, degrading model performance below the token savings.

---

## 3. Architecture & Test Battery

The benchmark suite operates in two tiers:

```
                  ┌──────────────────────────────────────────────┐
                  │    tzro bench signal-density --model ...    │
                  └──────────────────────┬───────────────────────┘
                                         │
                 ┌───────────────────────┴───────────────────────┐
                 ▼                                               ▼
   ┌───────────────────────────┐                   ┌───────────────────────────┐
   │    Tier 1: Micro-Evals    │                   │    Tier 2: Mini-Macro     │
   │  (Isolated Primitives)    │                   │   (End-to-End Workflows)  │
   ├───────────────────────────┤                   ├───────────────────────────┤
   │ 1. AST Skeletonization    │                   │ • Task A: Interface Impl  │
   │ 2. Log / Stack Compactor  │                   │ • Task B: Bug Fix / TDD   │
   │ 3. Smart JSON Crusher     │                   │ • Task C: Schema Refactor │
   │ 4. Tabular SQL Ingestion  │                   │                           │
   └───────────────────────────┘                   └───────────────────────────┘
```

### 3.1 Tier 1: Micro-Primitive Batteries

Each battery contains 5 curated test cases embedded in `pkg/benchmark/signaldensity/testdata/`:

1. **AST Skeletonization Battery (`ast_skeleton`)**:
   - *Target*: Multi-file Go/TypeScript packages with cross-module dependencies.
   - *Query*: Architectural questions requiring understanding type hierarchies, interfaces, and function signatures without needing function body implementations.
   - *Condition R*: Full raw files (5K–25K tokens).
   - *Condition T*: Skeletonized files with `// [body elided: #hash]` (800–3K tokens).
   - *Expected Result*: $SR = 1.0$, $CR \approx 5\times$–$8\times$, $SDM \approx 5.0$–$8.0\times$.

2. **Log & Stack Trace Compactor Battery (`compactor_logs`)**:
   - *Target*: Real verbose test outputs and panic dumps (Go `testing`, Node.js unhandled rejection, Python pytest) containing 200–500 lines of runtime boilerplate.
   - *Query*: Identify the root-cause failure message, the offending file path, and line number.
   - *Condition R*: Raw terminal output.
   - *Condition T*: Piped through `compactor.CompactLogStream()`.
   - *Expected Result*: $SR \ge 1.0$ (often $>1.0$ due to elimination of distraction), $CR \approx 4\times$–$7\times$, $SDM \approx 5.0$–$7.5\times$.

3. **Smart JSON Crusher Battery (`smart_json`)**:
   - *Target*: Uniform JSON arrays (50–100 items) representing cloud API listings, database query dumps, or commit logs.
   - *Query*: Extract matching records, compute exact row counts, or filter across multiple keys.
   - *Condition R*: Pretty-printed JSON array.
   - *Condition T*: Markdown table via `FormatTable()`.
   - *Expected Result*: $SR = 1.0$, $CR \approx 3\times$–$4\times$, $SDM \approx 3.0$–$4.0\times$.

4. **Tabular SQL Ingestion Battery (`tabular_sql`)**:
   - *Target*: 1,000–5,000 row CSV/TSV datasets (financial ledgers, access logs).
   - *Query*: Aggregate calculations (e.g. *"What is the sum of column X grouped by Y where Z > 100?"*).
   - *Condition R*: Raw CSV dump (or head/tail truncated).
   - *Condition T*: Tzro SQLite data envelope with schema, sample rows, and `tzro query` SQL invocation.
   - *Expected Result*: $SR > 1.2$ (raw LLM math regularly hallucinates sums; SQL execution is 100% exact), $CR \approx 20\times$–$40\times$, $SDM \ge 25\times$.

### 3.2 Tier 2: Mini-Macro Coding Tasks

Multi-file coding challenges executed against ephemeral workspaces scaffolded in `t.TempDir()`:
- **Task A (Interface Implementation)**: Implement a new caching driver satisfying an interface declared across existing packages.
- **Task B (Regression Bug Fix)**: Given a failing regression test and a 10-file repository, identify the bug and produce a patch that makes `go test ./...` pass.
- **Task C (Schema Refactoring)**: Add a new typed field to a domain model and update all dependent call sites and serialization logic.

**Evaluation Protocol**:
- **Baseline ($R$)**: Agent receives standard raw file reads and raw test command outputs.
- **Tzro ($T$)**: Agent receives skeletonized views, `tzro probe` discovery outputs, and compacted test run outputs.
- **Scoring**: Patches are applied to a clean git tree; automated test execution confirms pass/fail.

---

## 4. Execution Engine & Safety Controls

### 4.1 CLI Surface
```bash
# Run full benchmark against Claude 3.5 Sonnet
tzro bench signal-density --model anthropic/claude-3.5-sonnet

# Run targeted subset with spending limits
tzro bench signal-density \
  --model openai/gpt-4o \
  --tier micro \
  --primitive skeleton \
  --max-cost 1.50 \
  --samples 1 \
  --output .tzro/benchmarks/sdm_results.json
```

### 4.2 Flags & Options
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--model` | string | `anthropic/claude-3.5-sonnet` | Target model (OpenRouter or direct provider ID) |
| `--tier` | string | `all` | Filter by tier: `all`, `micro`, `macro` |
| `--primitive`| string | `all` | Filter micro primitive: `skeleton`, `compactor`, `json`, `tabular` |
| `--max-cost` | float | `2.00` | Hard spending limit in USD; halts immediately if exceeded |
| `--timeout` | duration | `60s` | Per-request timeout |
| `--output` | string | `""` | Optional filepath to persist complete JSON execution trace |
| `--no-cache` | bool | `true` | Busts KV cache headers to measure raw input tokens cleanly |

### 4.3 Cost Guard & Circuit Breaker
The execution loop calculates costs after every call using provider token rates:
$$\text{Cost}_{\text{turn}} = (K_{\text{prompt}} \times P_{\text{prompt}}) + (K_{\text{completion}} \times P_{\text{completion}})$$
If $\sum \text{Cost} \ge \text{MaxCost}$, execution halts cleanly, outputs partial metrics gathered so far, and logs an alert:
`[!] Max cost limit ($2.00) reached. Halting benchmark safely.`

---

## 5. Reporting & Artifacts

### 5.1 Terminal Output
A formatted Markdown table rendered directly to `stdout`:

```
========================================================================================
                      TZRO SIGNAL DENSITY BENCHMARK REPORT
========================================================================================
Model: anthropic/claude-3.5-sonnet | Max Cost: $2.00 | Total Spent: $0.48
────────────────────────────────────────────────────────────────────────────────────────
Component / Battery       Raw Tok   Tzro Tok   Acc (R)   Acc (T)   Ret (SR)   Comp (CR)   SDM
────────────────────────────────────────────────────────────────────────────────────────
AST Skeletonization       24,150     3,480     100.0%    100.0%     1.00x       6.94x    6.94x
Log Compactor             16,800     2,450      80.0%    100.0%     1.25x       6.86x    8.58x
Smart JSON Crusher         9,200     2,600     100.0%    100.0%     1.00x       3.54x    3.54x
Tabular SQL Ingestion     42,500     1,120      60.0%    100.0%     1.67x      37.95x   63.38x
Mini-Macro Coding         58,000    16,200     100.0%    100.0%     1.00x       3.58x    3.58x
────────────────────────────────────────────────────────────────────────────────────────
COMPOSITE SCORE          150,650    25,850      88.0%    100.0%     1.14x       5.83x    6.65x
========================================================================================
```

### 5.2 JSON Artifact Schema
Saved to `.tzro/benchmarks/sdm_<model>_<timestamp>.json`:
```json
{
  "$schema": "https://tzro.dev/schemas/signal-density-benchmark-v1.json",
  "metadata": {
    "model": "anthropic/claude-3.5-sonnet",
    "timestamp": "2026-09-09T22:25:00Z",
    "total_cost_usd": 0.482,
    "composite_sdm": 6.65
  },
  "summary": {
    "raw_tokens": 150650,
    "tzro_tokens": 25850,
    "overall_accuracy_raw": 0.88,
    "overall_accuracy_tzro": 1.0,
    "compression_ratio": 5.83,
    "signal_retention": 1.14
  },
  "batteries": [
    {
      "name": "ast_skeleton",
      "raw_tokens": 24150,
      "tzro_tokens": 3480,
      "accuracy_raw": 1.0,
      "accuracy_tzro": 1.0,
      "sdm": 6.94,
      "cases": []
    }
  ]
}
```

---

## 6. Verification & Test Plan

1. **Unit Tests (`pkg/benchmark/signaldensity/`)**:
   - Test metric calculation functions ($SR$, $CR$, $SDM$, geometric mean) with synthetic pass/fail token vectors.
   - Test cost guard circuit breaker: verify runner halts within $\le 1$ turn after exceeding max cost.
   - Test ground-truth matchers (exact regex, JSON schema, test runner exit-code detector).
2. **Offline Integration Test**:
   - Run the benchmark suite against a mock local HTTP test server (`httptest.NewServer`) returning recorded completions.
   - Asserts that all batteries run, table formatting renders cleanly, and the JSON artifact schema validates.
3. **Live Smoke Run**:
   - Run `tzro bench signal-density --primitive json --samples 1 --max-cost 0.10` against OpenRouter to verify end-to-end API integration and real token parsing.
