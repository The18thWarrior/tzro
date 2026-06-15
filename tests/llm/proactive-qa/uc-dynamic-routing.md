# Use Case: Dynamic Local-Cloud Planning Routing

**Actor**: Developer submitting tasks to the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888/api/tasks
**Priority**: P0

---

## Intent

A developer wants the engine to automatically decide whether to plan and execute a task locally (using the local model) or in the cloud (using a cloud API) based on task complexity, privacy constraints, thermal pressure, and cloud key availability — without manual configuration per-task.

## Preconditions

- The tzro daemon is running with a local model loaded.
- Configuration includes privacy settings (restricted directories, sensitive keywords).
- Cloud API key may or may not be configured.
- The host machine is in a measurable thermal state.

## Success Criteria

- [ ] Submitting a simple task (e.g., "list my recent tasks") routes to the local model when complexity is below threshold.
- [ ] Submitting a complex task (e.g., "research and synthesize AI orchestration trends") routes to the cloud model when a cloud key is available.
- [ ] Submitting a task that references a restricted directory routes to local regardless of complexity.
- [ ] Submitting a task containing sensitive keywords (passwords, secrets) routes to local regardless of complexity.
- [ ] When no cloud API key is configured, all tasks route to local with a clear reason in the routing decision.
- [ ] When the host is under thermal pressure (serious/critical), the routing decision factors in thermal state.
- [ ] The routing decision reason is visible in the task execution output or observer events.

## Edge Cases to Probe

- Submitting a task with both a restricted directory path and a cloud-forced model mode override to verify gate precedence.
- Submitting a task when the cloud API key is present but the cloud backend returns an error to verify fallback behavior.
- Submitting a borderline-complexity task to verify the threshold boundary behavior.
- Running multiple tasks in rapid succession to verify routing decisions are independent per-task.

## Anti-Patterns to Watch For

- [ ] Task silently routes to cloud when the prompt contains sensitive keywords.
- [ ] Routing decision reason is missing or says "unknown" in the observer events.
- [ ] Privacy quarantine flag is not set when routing to local for privacy reasons.
- [ ] System crashes or hangs when both local and cloud backends are unavailable.
- [ ] Model mode override ("local" or "cloud") is silently ignored.
