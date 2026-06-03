---
name: analyze-benchmark-run
description: Parse and evaluate cooperative local-cloud benchmark runs, classify failure modes, and architect robust system improvements. Use when analyzing test case output logs, evaluating json files prefixing with 'benchmark_results_', diagnosing planning or parameter extraction failures, or formulating refactor/ideation plans using improve-codebase-architecture and trend-architect.
---

# Analyze Benchmark Run

Conduct quantitative and architectural analysis of benchmark runs to systematically root out failure modes and design robust cooperative engine improvements.

## Quick Start

1. **Locate the latest results**: Find any `benchmark_results_*.json` in the workspace root.
2. **Execute quantitative audit**: Run the script from the repository root:
   ```bash
   python3 .agents/skills/analyze-benchmark-run/scripts/analyze.py <path_to_json>
   ```
3. **Draft the HTML Report**: Combine statistics, failure clustering, and architectural candidate cards.

## Workflows

### 1. Quantitative Evaluation

- Run `scripts/analyze.py` to extract stratified pass rates, latency percentiles (p50/p90/p99), and token ratios (local sidecar vs cloud).
- Record overall results and note significant regressions from baseline runs.

### 2. Failure Mode Clustering & Triage

Classify all failures into one of three canonical buckets:

- **Planning Mismatch**: Wrong tools selected, tool missing from call stack, or incorrect call order.
- **Parameter Mismatch**: Correct tools chosen but called with empty, malformed, or wrong argument types.
- **Operational Failures**: Planning and parameters match, but the tool failed to execute or timed out.

### 3. Deepening Improvements

- Apply the **deletion test** and identify **seams** on the failed modules using the [improve-codebase-architecture](file:///Users/jp/Desktop/Repos/tzro/.agents/skills/improve-codebase-architecture/SKILL.md) skill.
- Design deeper, high-leverage modules (e.g. semantic validators, schema anchors) behind small interfaces to isolate parameters and planning logic.

### 4. Trend-Driven Orchestration

- Use the [trend-architect](file:///Users/jp/Desktop/Repos/tzro/.agents/skills/trend-architect/SKILL.md) skill to research modern competitive local-first patterns.
- Investigate sidecar optimizations, such as KV-cache pinning, predictive parameter pre-fetching, or speculative execution threads.

### 5. Interactive HTML Report & Wiki Alignment

- Generate a standalone dashboard in `$TMPDIR` titled `benchmark-analysis-<timestamp>.html`.
- Style elegantly using Tailwind and Mermaid. Include:
  - Quantitative charts (latency, tokens, success rate).
  - Cards for each key failure mode with Before/After Mermaid diagrams illustrating the architectural fix.
- Present the report to the user and log decisions in `docs/wiki/` under the appropriate category.

## Advanced Features

For mathematical metric definitions, advanced diagnostic heuristics, and failure mode taxonomies, see [REFERENCE.md](REFERENCE.md).
