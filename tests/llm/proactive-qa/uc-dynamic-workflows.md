# Use Case: Dynamic Workflow Orchestration

**Actor**: Developer creating and executing dynamic workflows via CLI or MCP.
**Route**: CLI (`tzro workflow`) / MCP (`tzro_workflow`)
**Backend**: http://localhost:36888/api/workflows
**Priority**: P1

---

## Intent

A developer wants to create a workflow with a high-level goal and have the engine dynamically decide the next child task after each step completes — using LLM-driven orchestration rather than a pre-defined DAG — until the goal is achieved or a maximum iteration limit is reached.

## Preconditions

- The tzro daemon is running with a local or cloud model available for orchestration decisions.
- A workflow has been created with `orchestrationMode: "dynamic"`.
- The notification system is available for workflow event delivery.

## Success Criteria

- [ ] Creating a dynamic workflow stores it with the correct orchestration mode.
- [ ] Executing a dynamic workflow spawns child tasks iteratively until the LLM declares "goal_achieved".
- [ ] Each child task's result is fed back to the LLM as context for the next iteration decision.
- [ ] The workflow execution record captures all child task IDs and the final summary.
- [ ] The workflow stops after reaching the maximum iteration limit (10) with a clear indication.
- [ ] Workflow completion triggers a notification to the user.
- [ ] The workflow execution status transitions correctly: pending → running → completed/failed.

## Edge Cases to Probe

- Submitting a workflow with a goal that the LLM considers already achieved on the first iteration.
- A child task failing mid-workflow to verify error propagation and workflow failure state.
- The LLM returning an invalid JSON response during orchestration to verify error handling.
- Cancelling a workflow while a child task is in progress.
- Creating a workflow with an empty goal string.

## Anti-Patterns to Watch For

- [ ] Workflow enters an infinite loop when the LLM never returns "goal_achieved".
- [ ] Child task failures silently swallowed without updating the workflow status.
- [ ] Workflow execution ID collisions under rapid concurrent executions.
- [ ] LLM context grows unboundedly as child task results accumulate (should be bounded).
- [ ] Notification not sent when the workflow completes or fails.
- [ ] Workflow with orchestrationMode != "dynamic" accepted by the dynamic orchestrator.
