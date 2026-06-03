# ADR-0003: Proactive Observer Agent Design

## Context & Problem Statement

Large-scale agentic execution systems require background coordination and system cleaning (such as deactivating recurring cron jobs that return zero changes, purging failed threads, and optimizing memory). If these auditing loops are embedded directly within active task-tool workflows, they block critical execution threads, increasing task latency.

Furthermore, running background audit cycles immediately in response to every single tool event creates massive system thrashing and excess local model CPU usage.

## Proposed Decision

We choose to implement a non-blocking background **Observer Agent** running on a debounced, event-driven pipeline.

1. **Buffered Event Channels:** Active Go execution runners push telemetry events (e.g. step success, database write, API failure) to a non-blocking, globally shared Go channel (`observerChan`) with a capacity of `500`.
2. **Dual-Gate Debounce Loop:** A background Go routine polls `observerChan` and aggregates events, executing evaluations _only_ when:
   - The system remains inactive for **5 minutes** (ensures no active tasks are slowed).
   - Or the event queue reaches **10 events** (forces eager evaluations during heavy active periods).
3. **Automated Lifecycle Garbage Collection:** The Observer is equipped with a specific set of tools allowing it to list active heartbeats and task states, evaluate performance metrics (e.g., a cron task failing 5 times consecutively), and execute deactivations automatically.
4. **Dashboard Synchronization:** Decisions made by the Observer are published to the user's local dashboard using clean, structured Markdown notifications.

---

## Technical Specifications

### 1. The Debounced Observer Engine

```go
package observer

import (
	"context"
	"database/sql"
	"time"
)

type ObserverEvent struct {
	ID        string
	Type      string    // "task_success" | "task_failed" | "heartbeat_tick"
	Payload   string
	Timestamp time.Time
}

// Global buffered event channel
var ObserverChan = make(chan ObserverEvent, 500)

// StartObserverLoop runs the non-blocking background debouncer.
func StartObserverLoop(ctx context.Context, db *sql.DB) {
	const debounceDuration = 5 * time.Minute
	const maxBatchSize = 10

	var batch []ObserverEvent
	timer := time.NewTimer(debounceDuration)
	timer.Stop() // Initialize in idle, stopped state

	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ObserverChan:
			batch = append(batch, evt)

			// Eager Trigger Gate: Queue accumulated 10 events
			if len(batch) >= maxBatchSize {
				timer.Stop()
				b := append([]ObserverEvent(nil), batch...)
				batch = nil
				go evaluateBatch(db, b)
			} else {
				// Reset timer for idle check
				timer.Stop()
				timer.Reset(debounceDuration)
			}
		case <-timer.C:
			// Idle Trigger Gate: 5 minutes of system inactivity
			if len(batch) > 0 {
				b := append([]ObserverEvent(nil), batch...)
				batch = nil
				go evaluateBatch(db, b)
			}
		}
	}
}

// evaluateBatch processes aggregated telemetry events via background worker LLM
func evaluateBatch(db *sql.DB, events []ObserverEvent) {
	// 1. Analyzes performance trends
	// 2. Triggers system cleanups (e.g. deactivates dormant tasks)
}
```

---

### 2. Lifecycle Garbage Collection & Auditing Flow

When an audit is executed, the Observer evaluates heartbeat metadata:

```
                  Observer Auditing Execution
                              │
            ┌─────────────────┴─────────────────┐
            ▼                                   ▼
[Call list_heartbeat_tasks]            [Scan Task Trajectories]
            │                                   │
            └─────────────────┬─────────────────┘
                              ▼
        [Verify Failure & Redundancy Thresholds]
        - Has task failed 5 consecutive runs?
        - Has recurring sync returned 0 changes for 14 days?
                              │
            ┌─────────────────┴─────────────────┐
            ▼ (Threshold Met)                   ▼ (Normal Operation)
   ┌──────────────────┐                     [No Action Needed]
   │ Invoke tool:     │
   │ deactivate_task  │
   └──────────────────┘
            │
            ▼
┌───────────────────────────────────────────────┐
│ Publish structured Markdown user dashboard    │
│ notification detailing deactivation reason.  │
└───────────────────────────────────────────────┘
```

---

## Consequences

- **Pros:**
  - **Zero Execution Overhead:** The non-blocking channel ensures the critical path of the active Go executor is never blocked by audit overhead.
  - **Optimized CPU/GPU Usage:** Debouncing prevents the worker LLM from firing on every single tiny event, protecting computer battery life and CPU usage.
  - **Budget Protection:** Orphaned or malfunctioning API tasks are automatically cleaned up within hours, preventing unexpected API costs.
- **Cons:**
  - **Delayed Auditing:** Issues that do not hit the eager threshold are only reviewed 5 minutes after tasks complete, leading to a small lag in metric updates.
  - **Concurrency Complexity:** Handled via concurrent goroutines; requires robust thread-safety locks (`sync.Mutex`) when modifying shared resources in SQLite databases.
