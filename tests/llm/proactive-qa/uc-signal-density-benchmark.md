# Use Case: Signal Density Benchmark Suite

**Actor**: Developer, researcher, or performance engineer evaluating context optimization efficiency
**Route**: CLI — `tzro bench signal-density [--battery <name>] [--max-cost <usd>]`
**Backend**: Benchmark battery runner (`pkg/benchmark/signaldensity/`) with cost circuit breaker
**Priority**: P2

---

## Intent

A developer or researcher wants to empirically evaluate how effectively Tzro compacts context, locks KV-cache prefixes, and preserves critical task signal compared to unoptimized baselines across heterogeneous LLM workloads. Running `tzro bench signal-density` executes standardized task batteries, measures signal density metrics (task success, tokens consumed, signal per token), enforces strict spend limit circuit breakers, and generates structured comparison reports.

## Preconditions

- Tzro binary is installed and on PATH
- Valid provider API key configured (e.g. `OPENROUTER_API_KEY` or direct provider keys)
- Network connectivity to evaluation endpoints

## Success Criteria

- [ ] `tzro bench signal-density` runs default battery suite with automated cost tracking
- [ ] Hard spending circuit breaker (`--max-cost`) immediately halts execution if cumulative expenditure exceeds threshold
- [ ] Task completion and information recall are quantitatively scored against ground truth matchers
- [ ] Token reduction and signal density metric ($S = \text{Recall} / \text{Tokens}$) are calculated for each condition
- [ ] Output prints a formatted comparison table comparing Baseline vs Token Shield
- [ ] JSON report is exported containing granular turn-by-turn latencies, tokens, and hit rates
- [ ] Benchmark halts gracefully on SIGINT or unexpected provider 429/500 errors

## Edge Cases to Probe

- Setting `--max-cost 0.01` to verify immediate breaker shutdown on cost breach
- Running with an invalid API key (should report clear authentication error without burning retry loops)
- Running a specific isolated battery via `--battery <name>`
- Provider rate limits encountered mid-benchmark (exponential backoff handling)

## Anti-Patterns to Watch For

- [ ] Benchmark exceeds the user's `--max-cost` ceiling
- [ ] Infinite retry loops on hard upstream errors (401, 403)
- [ ] Reporting inflated signal metrics when evaluation matchers fail to validate assertions
- [ ] Corrupting stored local benchmark databases on premature termination
