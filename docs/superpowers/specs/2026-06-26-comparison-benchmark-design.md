# Comparison Benchmark: Cloud ReAct vs. tzro Hybrid Execution

**Date:** 2026-06-26  
**Status:** Draft  
**Purpose:** Produce defensible, real-world metrics comparing cloud-only agent execution against tzro's hybrid local/cloud execution model, using documentation generation tasks against the tzro codebase itself.

---

## 1. Overview

This benchmark runs a tiered suite of 5 documentation generation tasks against the tzro codebase under 4 execution conditions, measuring cloud token consumption, local token consumption, dollar cost, wall-clock time, and output quality. The results produce:

- A JSON data file with raw metrics for every condition × task combination
- A markdown report with comparison tables, scaling analysis, and compaction attribution

The primary marketing claim this supports: **tzro's cooperative execution mode reduces cloud API token consumption by 65–85% compared to a standard cloud-only ReAct agent loop, while maintaining comparable output quality.**

---

## 2. Execution Conditions

Four conditions form a 2×2 matrix isolating compaction benefits from local-model offloading benefits:

| Condition | ID | Pipeline | Compaction | Planning Model | Execution Model |
|---|---|---|---|---|---|
| Cloud ReAct (Baseline) | `cloud_react` | ReAct loop | ❌ | Cloud | Cloud |
| Cloud DAG + Compaction | `cloud_dag` | tzro DAG | ✅ | Cloud | Cloud |
| tzro Local-Only | `local_only` | tzro DAG | ✅ | Local | Local |
| tzro Cooperative | `cooperative` | tzro DAG | ✅ | Cloud | Local |

### Attribution Model

- **Compaction savings** = delta between `cloud_react` and `cloud_dag` (same cloud backend, compaction is the only variable)
- **Local offloading savings** = delta between `cloud_dag` and `cooperative` (compaction held constant, execution model changes)
- **Total savings** = delta between `cloud_react` and `cooperative` (the headline number)

---

## 3. Task Suite

Five documentation generation tasks of increasing complexity, all targeting the tzro codebase:

| Tier | Task ID | Prompt | Target Scope | Expected File Reads |
|---|---|---|---|---|
| T1 | `cache_function_index` | Generate a function index for `internal/cache/` listing every exported function, its signature, and a one-line description | `internal/cache/` | ~3-5 files |
| T2 | `inference_module_docs` | Generate module-level documentation for `internal/inference/` covering all public types, their relationships, and usage patterns | `internal/inference/` | ~10-13 files |
| T3 | `adr_summary` | Read all ADR files in `docs/adr/` and produce a consolidated decision log with status, date, and key implications for each | `docs/adr/` | ~15-25 files |
| T4 | `internal_architecture` | Generate architecture documentation for the full `internal/` tree showing package dependencies, data flow, and key abstractions | `internal/` | ~40-60 files |
| T5 | `comprehensive_readme` | Generate a comprehensive README with architecture overview, quickstart guide, package index, and API reference | Full codebase | ~60-80 files |

### Task Definition Schema

```json
{
  "id": "cache_function_index",
  "tier": 1,
  "prompt": "Generate a function index for internal/cache/ ...",
  "targetPaths": ["internal/cache/"],
  "qualityRubric": {
    "criteria": [
      {"name": "Completeness", "description": "Covers all exported functions"},
      {"name": "Accuracy", "description": "Signatures match source code"},
      {"name": "Structure", "description": "Well-organized, easy to scan"},
      {"name": "Usefulness", "description": "Developer would find this helpful"}
    ],
    "maxScore": 5.0
  }
}
```

---

## 4. Condition A: Cloud ReAct Loop (New Implementation)

The only net-new execution mode. Implements a standard multi-turn ReAct agent loop:

### Loop Mechanics

```
1. Initialize messages = [system_prompt, user_prompt]
2. Loop:
   a. Send messages → cloud API (record tokens)
   b. If response contains tool_call:
      - Execute tool (read_file / list_dir / search_files)
      - Append raw, uncompacted tool result to messages
      - Go to 2a
   c. If response is final text:
      - Record as output
      - Done
3. Safety limits:
   - Max 50 tool call iterations
   - Max ~180k accumulated tokens (terminate early if exceeded)
```

### Available Tools

| Tool | Source | Notes |
|---|---|---|
| `read_file` | `internal/tools/` | Returns raw file contents, no compaction |
| `list_dir` | `internal/tools/` | Returns directory listing |
| `search_files` | `internal/tools/` | Returns grep/ripgrep matches |

### System Prompt

```
You are a documentation generator. You have access to filesystem tools to explore 
a Go codebase. Read the relevant source files, understand the code, and produce 
the requested documentation. Call tools as needed. When you have gathered enough 
information, output the final documentation as markdown.
```

### Key Property: No Compaction

Tool outputs are appended to the conversation history **as-is**. This means:
- A 500-line Go file adds ~500 lines of raw text to the context
- Directory listings include all metadata
- Conversation grows monotonically — no windowing, no summarization

This is deliberately wasteful — it simulates the baseline cost of a naive cloud agent.

---

## 5. Conditions B/C/D: DAG Execution Modes

These use the existing `task.Execute()` pipeline with different `modelMode` settings:

| Condition | `modelMode` | What Changes |
|---|---|---|
| B: Cloud DAG | `"cloud"` | Cloud planning + cloud execution, compaction active |
| C: Local Only | `"local"` | Local planning + local execution, compaction active |
| D: Cooperative | `"cooperative"` | Cloud planning + local execution, compaction active |

All three benefit from:
- **5-layer compaction pipeline** reducing tool output sizes before context injection
- **Accumulated context windowing** (max 6 upstream nodes)
- **Topological parallel execution** (faster wall-clock time)
- **Disk-backed JQ cache** for payloads >12KB

No new code needed for these — they use existing infrastructure.

---

## 6. Metrics Captured

### Per Condition × Task

```go
type ComparisonResult struct {
    TaskID        string               `json:"taskId"`
    TaskTier      int                  `json:"taskTier"`
    Condition     string               `json:"condition"`
    CloudTokens   inference.TokenUsage `json:"cloudTokens"`
    LocalTokens   inference.TokenUsage `json:"localTokens"`
    WallClockMs   int64                `json:"wallClockMs"`
    EstCostUSD    float64              `json:"estCostUSD"`
    ToolCallCount int                  `json:"toolCallCount"`
    OutputText    string               `json:"outputText"`
    QualityScore  float64              `json:"qualityScore"`
    QualityNotes  string               `json:"qualityNotes"`
    Error         string               `json:"error,omitempty"`
}
```

### Cost Estimation

Dollar costs computed from a configurable pricing table:

```go
type PricingTable struct {
    PromptPer1KTokens     float64 // e.g., $0.003 for Claude 3.5 Sonnet input
    CompletionPer1KTokens float64 // e.g., $0.015 for Claude 3.5 Sonnet output
}
```

Formula: `cost = (promptTokens / 1000 * promptPrice) + (completionTokens / 1000 * completionPrice)`

Local tokens have $0.00 cost (the entire point).

---

## 7. Quality Scoring: LLM-as-Judge

After all conditions complete a task, a separate judge pass evaluates each generated document:

### Judge Protocol

1. Send the generated doc + quality rubric to the cloud API
2. Use a **fixed judge model** (always cloud, same model across all conditions)
3. Judge returns per-criterion scores (1–5) and brief reasoning
4. Judge tokens tracked separately (not part of the comparison)

### Judge System Prompt

```
You are a documentation quality evaluator. You will receive a generated documentation
file and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Missing/wrong
  2 = Minimal/mostly incorrect  
  3 = Adequate but incomplete
  4 = Good, covers most requirements
  5 = Excellent, comprehensive and accurate

Respond with valid JSON matching the provided schema.
```

### Judge Output Schema

```json
{
  "criteria": [
    {"name": "Completeness", "score": 4, "reasoning": "..."},
    {"name": "Accuracy", "score": 5, "reasoning": "..."}
  ],
  "overallScore": 4.25,
  "summary": "..."
}
```

---

## 8. Report Generation

### JSON Output

All `ComparisonResult` structs serialized to `comparison_results_YYYY-MM-DD.json`.

### Markdown Report

Sections:

1. **Executive Summary** — headline: "Cooperative mode reduced cloud tokens by X% and cost by $Y vs. baseline"
2. **Per-Task Comparison Table** — all 4 conditions × 5 tasks in a single table
3. **Scaling Analysis** — how savings percentages change from T1 (small) to T5 (large)
4. **Compaction Attribution** — A→B delta (compaction alone) vs. B→D delta (local offloading)
5. **Quality Comparison** — quality scores across conditions, showing quality is maintained
6. **Methodology** — models used, pricing, date, system specs
7. **Raw Data** — link to JSON file

---

## 9. Execution Isolation

Each condition runs in complete isolation:

- Fresh `TokenTracker` per condition
- Isolated benchmark SQLite database (no shared memory/KG state)
- KV cache GC between conditions (for local model runs)
- Same task prompt and target scope across all conditions
- Conditions run sequentially (not in parallel) to avoid resource contention

---

## 10. Package Structure

```
internal/comparison/
├── suite.go           # ComparisonSuite, orchestration, task loading
├── react.go           # ReAct loop implementation (Condition A)
├── conditions.go      # Condition B/C/D wrappers around task.Execute
├── judge.go           # LLM-as-judge quality scoring
├── report.go          # Markdown + JSON report generation
├── pricing.go         # Cost estimation from token counts
├── types.go           # ComparisonResult, ComparisonTask, PricingTable
└── testdata/
    └── docgen_tasks.json  # The 5-task suite definitions

cmd/tzro/
└── compare.go         # CLI: `tzro compare --tasks docgen --output results/`
```

### CLI Interface

```bash
# Run all tasks, all conditions
tzro compare --output results/

# Run a single tier for quick testing
tzro compare --tier 1 --output results/

# Run a single condition for debugging
tzro compare --condition cooperative --output results/

# Specify custom pricing
tzro compare --prompt-price 0.003 --completion-price 0.015 --output results/
```

---

## 11. Expected Outcomes

Based on the architecture of the compaction pipeline and the nature of doc-gen tasks:

| Metric | Expected Range | Justification |
|---|---|---|
| Compaction savings (A→B) | 40–65% cloud token reduction | File contents compress well via Layer 2 (JSON→TSV) and Layer 3 (KV formatting) |
| Local offloading savings (B→D) | 60–90% additional cloud token reduction | Cooperative mode only uses cloud for planning (~2-3 calls), not execution (~10-50 calls) |
| Total savings (A→D) | 80–95% cloud token reduction | Combined effect |
| Quality delta (A vs D) | -0.5 to -1.0 on 5-point scale | Local execution produces slightly lower quality; cooperative planning mitigates |
| Wall-clock delta | Variable | DAG parallelism may offset local model's slower inference speed |

These are hypotheses to be validated, not guaranteed results.
