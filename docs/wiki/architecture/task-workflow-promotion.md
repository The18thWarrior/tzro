# Task-to-Workflow Promotion Engine

The **Task-to-Workflow Promotion Engine** dynamically elevates standard, thread-bound Task DAGs to persistent Multi-Task Workflows when they trigger specific cognitive, temporal, or operational thresholds.

## Architectural Boundaries

In the `tzro` domain model:

- **Task**: A compiled sequence of execution steps and dependency edges representing a single multi-step operational objective. It executes in a single sweep and is bound to a single thread/engine runtime session.
- **Workflow**: A persistent orchestrator that schedules, triggers, and coordinates multiple dependent **Tasks** over hours, days, or weeks to achieve high-level business goals.

To ensure safety on resource-constrained hardware and protect local execution logic from LLM attention degradation, the Unified Classifier enforces a three-dimensional boundary checking pipeline:

```
   ┌────────────────────────────────────────────────────────┐
   │                UNIFIED CLASSIFIER INTAKE               │
   └───────────────────────────┬────────────────────────────┘
                               │
             Does it meet any Promotion Triggers?
             [1] BFS Tool Cap > 12?
             [2] Delayed / Cron / Event-driven step?
             [3] Human-in-the-Loop checkpoint requested?
                               │
               ┌────────────────┴────────────────┐
               ▼ YES                             ▼ NO
      ┌─────────────────┐               ┌─────────────────┐
      │ Promoted to a   │               │ Compiled as a   │
      │    WORKFLOW     │               │   Single TASK   │
      └─────────────────┘               └─────────────────┘
```

## Promotion Triggers

### A. The Tool Cap Breakpoint (Cognitive Scale)

- **Trigger**: The 2-hop BFS tool neighborhood (original matched tools + intermediate WASM helper skills) exceeds **12 tools**.
- **Rationale**: Capping the tool catalog at 10–15 items preserves optimal LLM planning recall and keeps GBNF grammar sizes compact. Promoted workflows split the catalog into isolated sub-tasks, keeping each individual task below the optimal limit.

### B. Durable Delay & Temporal Triggers (Time Scale)

- **Trigger**: The prompt implies temporal gaps, deferrals, cron triggers, or asynchronous wait states (e.g. _"Wait 3 days"_, _"Run every Tuesday at 9 AM"_, _"Wait until the lead accepts the invite"_).
- **Rationale**: A single Task engine execution thread must never "sleep" or block system resources across daemon restarts. Gaps are promoted to Workflows, allowing independent Tasks to be dispatched, completed, and checkpointed durably in SQLite.

### C. State-Resiliency & Failure Isolation (Operational Scale)

- **Trigger**: The prompt requests explicit Human-in-the-Loop (HITL) validation checkpoints between heavy operations (e.g. _"Perform a dry run and wait for my sign-off before executing the Salesforce writes"_).
- **Rationale**: Tasks are binary (they succeed fully or fail completely). Workflows support checkpoint persistence. Promoted workflows register independent tasks where Task A (Dry Run) can be marked as `completed` while Task B (Salesforce Write) is held in `pending` awaiting manual TUI/CLI approval, eliminating redundant retries of previous steps.

## Implementation Details

The Promotion Engine is implemented under a deep, isolated subsystem in the `internal/classifier` package:

1. `ShouldPromoteToWorkflow(prompt string) bool`: Identifies temporal waits, schedule deferrals, and HITL approvals using compiled regular expressions.
2. `CalculateBFSNeighborhoodToolCount(matchedTools []string) int`: Graph-RAG neighborhood traversal utilizing `memory.DB.GetEntityNeighborhood` up to 2 hops, filtering and counting unique tools and skills.
3. `DecomposeWorkflow(prompt string) (memory.WorkflowDefinition, []memory.WorkflowTask)`: Splits a promoted user request into a set of sequential, dependent `WorkflowTask` templates.
4. `PromoteAndDecompose(prompt string) (bool, memory.WorkflowDefinition, []memory.WorkflowTask)`: Unified helper returning a promotion flag and the decomposed workflow.
