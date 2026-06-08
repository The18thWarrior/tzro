# Latency Analysis and Remediation Notes

This document summarizes the findings from the execution latency prototype and outlines concrete ideas to remediate the bottlenecks.

## Findings

1. **Sleep Bottleneck in Node Execution**:
   - `internal/executor/executor.go` contains hardcoded `time.Sleep(800 * time.Millisecond)` statements in every node execution type:
     - `gbnf_bridge` (line 500)
     - `deterministic` (line 620)
     - `synthesis` (line 762)
     - Generic action path (line 966)
   - These sleeps occur *on every single node* executed. Even if the local LLM inference and the tool execution take `0ms`, a single node takes at least `800ms`.

2. **Sleep Bottleneck in Level Transition**:
   - `internal/executor/executor.go` also contains a hardcoded `time.Sleep(500 * time.Millisecond)` (line 279) inside the loop that iterates over Kahn levels.
   - This delay is introduced for "visual representation in the GUI/TUI".

3. **Cumulative Impact**:
   - For a simple 4-level linear graph (1 node per level), the executor sleeps for:
     `4 * 800ms (nodes) + 4 * 500ms (levels) = 5.2 seconds` of pure idle CPU time.
   - This makes simple local tasks feel extremely sluggish and adds significant overhead to developers running test suites.
   - For example, the Go unit test suite for the executor (`internal/executor`) takes **~17 seconds** to run just a few tests. The benchmark suite (`internal/benchmark`) takes **~77 seconds** to run.

---

## Remediation Proposal

We should eliminate hardcoded sleeps during programmatic executions (such as CLI runs, benchmark suites, and unit tests) while retaining the pacing/observability delays *only* when the user is explicitly viewing them via the GUI or TUI.

### 1. Introduce Configuration Options
Add configuration options in `internal/config/config.go`:
```go
type Config struct {
    // ...
    ExecutorNodeDelayMs  int `json:"executor_node_delay_ms"`  // Default: 800
    ExecutorLevelDelayMs int `json:"executor_level_delay_ms"` // Default: 500
}
```

### 2. Dynamically Adjust Delays
Update `internal/executor/executor.go` to load these delays from config:
```go
cfg := config.Get()
nodeDelay := time.Duration(cfg.ExecutorNodeDelayMs) * time.Millisecond
levelDelay := time.Duration(cfg.ExecutorLevelDelayMs) * time.Millisecond
```

And substitute the hardcoded sleeps:
- `time.Sleep(800 * time.Millisecond)` -> `time.Sleep(nodeDelay)`
- `time.Sleep(500 * time.Millisecond)` -> `time.Sleep(levelDelay)`

### 3. Automatically Skip Delays in Non-Interactive Modes
We can automatically set these delays to `0` or near-zero under the following conditions:
- **Test Mode**: Detect if running inside a unit test (e.g. via `flag.Lookup("test.v") != nil` or `isBenchmark` context value) and bypass sleeps entirely.
- **CLI Mode**: If executing via `tzro chat` or headless CLI where no GUI is active, bypass sleeps.
- **Custom Header/Option**: Allow client requests (e.g., TUI/GUI) to pass a pacing option if they want visual delays, defaulting to `0` otherwise.

### 4. Impact of Remediation
- **Unit Test Speed**: Executor unit tests will drop from **~17s to <0.5s**.
- **Benchmark Suite Speed**: Benchmark suites will drop from **~77s to <10s** (saving over a minute of developer time per run).
- **Execution Speed**: Typical T1/T2 task executions will complete almost instantly (limited only by local/cloud model inference latency and tool execution time).
