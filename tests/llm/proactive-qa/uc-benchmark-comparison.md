# Use Case: Benchmark Comparison Framework

**Actor**: Developer evaluating local-vs-cloud execution quality.
**Route**: CLI (`tzro compare`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer wants to run standardized benchmark tasks across multiple execution modes (local-only, cloud-only, cooperative) and compare the quality, latency, and cost of each mode. The comparison framework executes tasks, judges output quality using an LLM, and generates a structured report with per-task and aggregate metrics.

## Preconditions

- The `tzro` daemon is running with both local and cloud models available.
- Benchmark task definitions exist as JSON (e.g., `internal/comparison/testdata/docgen_tasks.json`).
- At least one cloud API key is configured for cloud execution modes.

## Success Criteria

- [ ] Developer can run `tzro compare` to execute a benchmark suite against multiple execution modes.
- [ ] Each task is executed in each configured mode (local, cloud, cooperative) and produces captured output.
- [ ] An LLM judge evaluates each output on well-defined criteria (completeness, accuracy, depth) and assigns a numeric score.
- [ ] The judge's evaluation includes per-criterion scores and a rationale for each.
- [ ] A structured JSON report is generated containing per-task results, latencies, token counts, and cost estimates.
- [ ] A human-readable markdown report is generated with summary tables, per-task breakdowns, and aggregate statistics.
- [ ] Pricing estimates use per-mode cost models (local = $0, cloud = token-based pricing).
- [ ] Tasks that fail or timeout in a given mode are reported as failures without crashing the entire suite.
- [ ] Condition-based success thresholds can be defined (e.g., "local must score >= 0.7 of cloud") and violations are flagged.

## Edge Cases to Probe

- Running comparison with only one mode configured (e.g., no cloud key) — verify graceful degradation.
- A task that consistently fails under one mode but succeeds under others — verify independent reporting.
- Judge model returning malformed JSON — verify the framework handles parse failures gracefully.
- Very long task output exceeding judge context limits — verify truncation before judging.
- Running the same suite twice — verify results are independent and not cached.

## Anti-Patterns to Watch For

- [ ] A failing task in one mode causes subsequent modes or tasks to be skipped.
- [ ] Judge scores are not normalized or comparable across modes.
- [ ] Cost estimates are missing or show $0 for cloud execution modes.
- [ ] Report omits latency data or shows 0ms for all tasks.
- [ ] Stack traces or raw errors appear in the generated report instead of structured failure records.
- [ ] The comparison framework modifies the daemon state or persists benchmark data into production memory.
