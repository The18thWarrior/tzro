# Bug Post-Mortem: Cloud Planner Timeout & Heuristic Fallback Tool Pollution

## Diagnosis Summary

During the cooperative benchmark suite execution on `2026-05-25`, the suite experienced a **16.0% overall success rate** (8/50 cases passed). A deep analysis of the execution logs (`benchmark_results_5_25_2026_10_58.json`) reveals two tightly coupled architectural issues that caused a massive cascade of avoidable failures.

---

## 1. Root Cause Analysis

### Issue A: Hardcoded 15-Second Cloud HTTP Client Timeout

- **Symptom**: Repeated log warnings of type:
  `[Task Planner Warning] Cloud planning failed: Post "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions": context deadline exceeded (Client.Timeout exceeded while awaiting headers).`
- **Location**: `internal/inference/cloud_model.go#L92`
- **Mechanism**:
  - `CallCloudModel` initializes a local `http.Client` with a hard timeout of `15 * time.Second` (`client := &http.Client{Timeout: 15 * time.Second}`).
  - Because Strategic Cloud planning requires compiling large abstract JSON graphs with complex GBNF schemas and instructions, the remote models (e.g. Gemini via OpenAI-compatible endpoints) under network load often take 15–25 seconds to formulate the full response.
  - When the response exceeds 15 seconds, the HTTP client terminates the connection, raising a context timeout exception and forcing the task planner to trigger a heuristic fallback.

### Issue B: Heuristic Fallback Tool Pollution (Hardcoded Tool Injection)

- **Symptom**: 15 test cases (30% of the entire benchmark suite) crashed with errors like:
  `tool 'salesforce_query' is not registered or discovered in the dynamic Tool Registry` or `tool 'slack_message' is not registered...`
- **Location**: `internal/task/task.go#L197` (`buildHeuristicGraph`)
- **Mechanism**:
  - When cloud planning fails, `internal/task/task.go` falls back to `buildHeuristicGraph` to create an offline plan.
  - However, `buildHeuristicGraph` is entirely static. If a prompt mentions `"query"`, `"sheet"`, or `"lead"`, it hardcodes `salesforce_query` and `postgres_insert`. If a prompt mentions `"message"` or `"slack"`, it hardcodes `slack_message`. For all other generic tasks, it defaults to a two-node `salesforce_query` -> `postgres_insert` chain.
  - Because benchmark test cases dynamically mock and register their own specific schemas (e.g., `GorillaFileSystem`, `TwitterAPI`, `MathAPI`), these hardcoded generic tools do not exist in the active `tools.Registry`.
  - The local `Executor` attempts to execute the heuristic plan and immediately crashes because `salesforce_query` or `slack_message` are not registered.

---

## 2. Statistical Impact Breakdown

Out of 50 evaluated cases:

- **Passed Cases**: 8 (16.0%)
- **Failed Cases**: 42 (84.0%)
  - **Tool Registration Crashes**: 15 cases (30.0% of total) caused directly by Heuristic Fallback pollution injecting unregistered tools.
    - `salesforce_query` crashes: 11 cases
    - `slack_message` crashes: 4 cases
  - **Plan/Parameter Mismatches**: 27 cases (54.0% of total) where the planned DAG or argument values differed from ground truth.

---

## 3. Resolution (Implemented on 2026-05-25)

Both corrective actions have been fully implemented under TDD unit testing:

1. **Extended Cloud Model Client Timeout**: The HTTP client timeout in `internal/inference/cloud_model.go` was extended to `60 * time.Second` to handle complex schema compilation under high network/API latencies safely.
2. **Disabled Heuristic Fallback Planning**: We completely decoupled the static heuristic fallback from the plan generation pipeline in `internal/task/task.go`. If remote cloud planning fails or is unconfigured, the system immediately returns the original planner error, preventing any incorrect static tool injection crashes.
3. **Unit Tests Adapted**: Created `TestPlan_NoHeuristicFallback` to verify direct failures, and adapted existing structural checks to verify `buildHeuristicGraph` directly.
