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
- [ ] When a local plan contains nodes referencing hallucinated (non-existent) tools, the plan repair pipeline surgically replaces those nodes with a probe node.
- [ ] Plan repair runs up to 2 attempts before escalating to cloud planning.
- [ ] At execution time, if a node references a hallucinated tool, the executor's tool name classifier maps it to the closest real tool using local inference.
- [ ] When cloud escalation is blocked (local_only privacy mode), confidence checks are skipped since cloud is unavailable as a fallback.
- [ ] Intent classification and complexity scoring are dispatched to the router sidecar (fast, small model), not the worker sidecar.
- [ ] When the router sidecar is unavailable, intent classification falls back to the worker sidecar transparently.

## Edge Cases to Probe

- Submitting a task with both a restricted directory path and a cloud-forced model mode override to verify gate precedence.
- Submitting a task when the cloud API key is present but the cloud backend returns an error to verify fallback behavior.
- Submitting a borderline-complexity task to verify the threshold boundary behavior.
- Running multiple tasks in rapid succession to verify routing decisions are independent per-task.
- Local plan hallucinating 3 different invalid tools across 3 nodes, verifying all are repaired in a single repair pass.
- Repair exhaustion: plan with invalid tools that persist after 2 repair attempts, verifying escalation to cloud.
- Tool classifier receives a near-miss tool name (e.g., "search_file" instead of "search_files"), verifying it resolves to the correct tool.

## Anti-Patterns to Watch For

- [ ] Task silently routes to cloud when the prompt contains sensitive keywords.
- [ ] Routing decision reason is missing or says "unknown" in the observer events.
- [ ] Privacy quarantine flag is not set when routing to local for privacy reasons.
- [ ] System crashes or hangs when both local and cloud backends are unavailable.
- [ ] Plan repair silently drops nodes, causing broken dependency edges in the DAG.
- [ ] Tool classifier maps a hallucinated tool to the wrong real tool (e.g., "delete_file" → "read_file").
- [ ] Model mode override ("local" or "cloud") is silently ignored.
