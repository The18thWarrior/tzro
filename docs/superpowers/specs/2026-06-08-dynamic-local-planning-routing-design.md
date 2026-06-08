# Design Spec: Dynamic Local Planning and Routing

**Date:** 2026-06-08
**Status:** Approved (brainstorming complete)
**PRD:** [PRD.md](../../../.scratch/local-planning-routing/PRD.md)
**Wiki:** [local-planning-routing.md](../../wiki/features/local-planning-routing.md)

---

## Problem

`tzro`'s planning architecture is binary: cloud planner if an API key is present, local backend otherwise. There is no runtime routing that considers task complexity, data privacy, or cost. This prevents hybrid deployments where simple or sensitive tasks stay on-device while complex strategic planning uses cloud models.

## Solution

Introduce a **Dynamic Router** into the planning pipeline that evaluates each task at runtime and routes planning to local or cloud backends based on three factors:

1. **Privacy policy** — workspace paths and prompt keywords that quarantine planning to local
2. **Complexity grading** — T0/T1/T2 tiers compared against a configurable threshold
3. **Cooperative fallback** — local plan validation with cloud escalation on failure (when permitted)

---

## Design Decisions (from brainstorming)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Privacy level semantics | `strict-local`: never cloud. `hybrid`: evaluate context. `cloud-preferred`: cloud default, respects privacy constraints. |
| 2 | Sensitivity detection | Restricted directory path matching + configurable keyword substring check on prompt |
| 3 | Complexity threshold | String enum `"T0"` / `"T1"` / `"T2"` (default `"T1"`). Tasks at/below threshold plan locally. |
| 4 | Validation & fallback | 3-step validation (JSON parse, Kahn cycle detection, tool conformance). 1-retry escalation: local → cloud. No retries within same tier. |
| 5 | Architectural seam | New `internal/routing/` package. `Route()` returns a `RoutingDecision`. `Plan()` in `task.go` calls the router. |
| 6 | CLI/config override | Reuse existing `ModelMode` field. `"local"` / `"cloud"` bypass the router entirely. `"cooperative"` (default) enables dynamic routing. |

---

## Section 1: Config Schema Changes

### New fields on `EngineConfig` in `internal/config/config.go`

```go
// Privacy & Routing (Dynamic Local Planning)
PrivacyLevel          string   `json:"privacyLevel,omitempty"`          // "strict-local" | "hybrid" | "cloud-preferred" (default: "hybrid")
RestrictedDirectories []string `json:"restrictedDirectories,omitempty"` // Absolute or relative paths locked to local-only planning
ComplexityThreshold   string   `json:"complexityThreshold,omitempty"`   // "T0" | "T1" | "T2" (default: "T1")
SensitiveKeywords     []string `json:"sensitiveKeywords,omitempty"`     // Custom keywords; empty = use built-in defaults
```

### New helper functions

- `GetPrivacyLevel()` → returns `"hybrid"` if empty/invalid
- `GetComplexityThreshold()` → returns `"T1"` if empty/invalid (validated against `T0`/`T1`/`T2`)
- `GetSensitiveKeywords()` → returns config list if non-empty, otherwise built-in defaults: `["password", "secret", "private_key", "api_key", "token", "credential", "db_url", "ssh_key"]`
- `GetRestrictedDirectories()` → returns config list as-is (empty = no restrictions)

The `Save()` and `Override()` functions are extended to copy these fields. No migration needed — omitted fields use defaults.

---

## Section 2: Routing Context & Decision Types (`internal/routing/types.go`)

```go
package routing

// RoutingContext carries everything the router needs to make a decision.
type RoutingContext struct {
    Prompt              string
    ActivePaths         []string
    ComplexityTier      string   // "T0" | "T1" | "T2"
    PrivacyLevel        string   // "strict-local" | "hybrid" | "cloud-preferred"
    ComplexityThreshold string   // "T0" | "T1" | "T2"
    RestrictedDirs      []string
    SensitiveKeywords   []string
    ModelMode           string   // "cooperative" | "local" | "cloud"
    CloudKeyAvailable   bool
    LocalBackendActive  bool
}

// RoutingDecision is the output of Route().
type RoutingDecision struct {
    Backend            string // "local" | "cloud"
    Reason             string // Human-readable for telemetry/logging
    PrivacyQuarantined bool   // True if privacy constraints forced the decision
    AllowCloudFallback bool   // Can the validation pipeline escalate to cloud?
}
```

Key properties:
- `RoutingContext` is a value struct with no package dependencies.
- `AllowCloudFallback` is `false` when `PrivacyQuarantined` is `true` or `ModelMode` is `"local"`.

---

## Section 3: Router Logic (`internal/routing/router.go`)

The `Route()` function implements a short-circuit decision tree:

```
ModelMode override? ──yes──► return forced backend
        │ no
Privacy quarantine? ──yes──► return local (no fallback)
        │ no
Cloud key available? ──no──► return local (no fallback)
        │ yes
Tier ≤ threshold? ──yes──► return local (fallback OK)
        │ no
strict-local? ──yes──► return local (no fallback)
        │ no
        └──► return cloud (fallback OK)
```

### Gate 0: ModelMode override
- `"local"` → return local, no fallback
- `"cloud"` → return cloud, no fallback

### Gate 1: Privacy quarantine
`isPrivacyQuarantined()` checks two conditions:
1. **Restricted directory match**: Any `ActivePaths` entry is a child of any `RestrictedDirs` entry (using `filepath.Rel` or `strings.HasPrefix` after path cleaning).
2. **Sensitive keyword match**: `strings.Contains(strings.ToLower(prompt), keyword)` for any keyword in the list.

If either triggers → `PrivacyQuarantined: true`, `AllowCloudFallback: false`.

### Gate 2: Cloud availability
No cloud API key → must go local, no fallback possible.

### Gate 3: Complexity threshold
`tierBelow(actual, threshold)` uses ordinal map `{"T0": 0, "T1": 1, "T2": 2}`.
- At or below threshold → local (with cloud fallback allowed)
- Above threshold → fall through to cloud

### Gate 4: strict-local safety net
Final check for `strict-local` privacy level (catches edge cases where privacy wasn't triggered by path/keyword but level is restrictive).

### Default
Cloud planning with fallback allowed.

---

## Section 4: Validation Pipeline (`internal/routing/validate.go`)

### `ValidateGraph(graph, toolExists)` — 3-step pipeline

1. **Structural check**: graph is non-nil with at least one node
2. **Cycle detection**: `compiler.CompileAndSort(graph)` succeeds
3. **Tool schema conformance**: every action node's `action` field references a registered tool (probe/synthesis/deterministic nodes are exempt)

### `PlanWithEscalation(ctx, localPlanFn, cloudPlanFn, decision, toolExists)`

- Calls `localPlanFn`, validates the result
- On validation failure: publishes `plan_validation_failed` telemetry event
- If `AllowCloudFallback` is `true` → calls `cloudPlanFn` (1-retry cap)
- If `AllowCloudFallback` is `false` → returns error with privacy explanation
- Cloud failures are not retried — error surfaces to user

---

## Section 5: `Plan()` Refactor in `task.go`

### New `ExecuteOptions` field

```go
ActivePaths []string // File/directory paths from active workspace context
```

### Refactored flow

1. Collect tool names, classify complexity tier (existing classifier)
2. Assemble `RoutingContext` from config getters + classifier output + `ExecuteOptions`
3. Call `routing.Route()` → get `RoutingDecision`
4. Publish `plan_routing` telemetry event with decision details
5. Dispatch: if `Backend == "local"` → `PlanWithEscalation()`; if `Backend == "cloud"` → direct `planWithCloud()`
6. If privacy-quarantined planning fails → publish `plan_privacy_blocked` telemetry event
7. Run SCT expansion (unchanged)

### Caller impact

- `handleChat` in `server.go`: no breaking API change. It still calls `classifier.ClassifyComplexity` for the HTTP response. `Plan()` runs its own internal classification for routing.
- MCP `tzro_run`: can populate `ActivePaths` if harness provides workspace context. Empty = restricted dir check is a no-op.

---

## Section 6: Testing Strategy

### Unit tests (`internal/routing/router_test.go`, `validate_test.go`)

| Test case | Input | Expected |
|---|---|---|
| ModelMode=local short-circuit | `ModelMode: "local"` | `Backend: "local"`, `AllowCloudFallback: false` |
| ModelMode=cloud short-circuit | `ModelMode: "cloud"` | `Backend: "cloud"`, `AllowCloudFallback: false` |
| Restricted directory match | `ActivePaths: ["/secrets/db.go"]`, `RestrictedDirs: ["/secrets"]` | `PrivacyQuarantined: true` |
| Sensitive keyword match | Prompt: `"rotate the api_key"` | `PrivacyQuarantined: true` |
| T0 below T1 threshold | `Tier: "T0"`, `Threshold: "T1"` | `Backend: "local"`, fallback OK |
| T2 above T1 threshold | `Tier: "T2"`, `Threshold: "T1"`, cloud available | `Backend: "cloud"` |
| No cloud key | `CloudKeyAvailable: false`, `Tier: "T2"` | `Backend: "local"`, no fallback |
| Validation: cycle | Cyclic edge graph | `ValidateGraph` → cycle error |
| Validation: unknown tool | Node with `"nonexistent_tool"` | `ValidateGraph` → unknown tool error |
| Escalation: cloud allowed | `AllowCloudFallback: true` | Cloud plan returned |
| Escalation: privacy blocked | `AllowCloudFallback: false` | Error with privacy message |

### Integration test (`internal/task/task_test.go`)

- Mock local backend to return a graph with an invalid tool reference
- Verify escalation to cloud planner occurs (when privacy allows)
- Verify escalation is blocked under `strict-local`

---

## Out of Scope

- Token-level data redaction before sending prompts to cloud (deferred to future security sprint)
- Automatic local model fine-tuning or model downloading pipelines
- Latency timeout gate on local planning (may revisit if local planning latency becomes a problem)

## File Manifest

| File | Action | Description |
|---|---|---|
| `internal/config/config.go` | MODIFY | Add 4 new config fields + 4 getter functions |
| `internal/routing/types.go` | NEW | `RoutingContext` and `RoutingDecision` types |
| `internal/routing/router.go` | NEW | `Route()` decision tree + privacy/threshold helpers |
| `internal/routing/validate.go` | NEW | `ValidateGraph()` pipeline + `PlanWithEscalation()` |
| `internal/routing/router_test.go` | NEW | Unit tests for all routing paths |
| `internal/routing/validate_test.go` | NEW | Unit tests for validation pipeline |
| `internal/task/task.go` | MODIFY | Refactor `Plan()` to call router, add `ActivePaths` to `ExecuteOptions` |
