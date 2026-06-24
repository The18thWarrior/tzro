---
name: wake-sentinel
description: Manually wake the Sentinel Agent to trigger an immediate retrieval-grounded analysis cycle. Use when the user says "wake sentinel", "check sentinel", "trigger sentinel", "sentinel scan", or wants an on-demand proactive insight before or after a significant code change.
---

# Wake Sentinel

Trigger the Sentinel Agent's retrieval-grounded synthesis pipeline on demand, outside its normal heartbeat cadence.

## Quick Start

Call the `tzro_sentinel_wake` MCP tool:

```
tzro_sentinel_wake({})
```

With a context hint to focus the analysis:

```
tzro_sentinel_wake({ "contextHint": "review auth module for migration risks" })
```

## When to Use

- **After a major code change** — get proactive feedback before committing
- **Before a release** — surface any cross-cutting concerns from accumulated knowledge
- **User requests it** — "wake the sentinel", "sentinel check", "run sentinel"
- **After bulk memory ingestion** — let the Sentinel correlate new knowledge with workspace state

## Workflow

1. Call `tzro_sentinel_wake` with an optional `contextHint`
2. Check the response `alertProduced` field:
   - `true` → An alert was generated. Retrieve it with `tzro_sentinel_alerts`
   - `false` → No actionable insight found (context too thin or candidates below confidence gate)
3. Surface any alerts to the user per the priority handling rules in `AGENTS.md`:
   - **critical** → Surface immediately
   - **suggestion** → Surface at natural conversation breaks
   - **ambient** → Batch and mention only if user asks

## Parameters

| Parameter     | Required | Description |
|---------------|----------|-------------|
| `contextHint` | No       | Biases retrieval toward a specific topic. Injected as a synthetic activity report before evaluation. |

## Example: Post-Refactor Check

```
// 1. Wake with focused hint
tzro_sentinel_wake({ "contextHint": "refactored database connection pooling in internal/db" })

// 2. If alert produced, retrieve it
tzro_sentinel_alerts({})
```
