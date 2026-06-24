# Use Case: Web Dashboard Agent Interface

**Actor**: Developer or End User interacting with the tzro server through their web browser.
**Route**: / (React Frontend Web App — OpenUI-powered layout)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

A developer wants a visual observability surface to monitor active tasks, inspect execution events, view system configuration, manage workflows, and observe the thermal and inference state of the local engine — all rendered from a server-generated OpenUI layout spec that the agent can compose dynamically.

## Preconditions

- The tzro backend daemon is running and reachable on `http://localhost:36888`.
- The dashboard is served at `http://localhost:36888/dashboard/` (static build) or via Vite dev server on `http://localhost:5173`.
- A modern web browser is used to access the dashboard.
- At least one task has been executed so the dashboard has data to display.

## Success Criteria

- [ ] User sees a dashboard rendered from the OpenUI layout spec served by the backend at `/api/dashboard/spec`.
- [ ] User can view a list of active and completed tasks with their statuses.
- [ ] User can select a task to see its detailed execution timeline (node-level events).
- [ ] User can view recent observer events in a scrollable event log.
- [ ] User can view system configuration including model settings and active services.
- [ ] User can view downloaded models and their status.
- [ ] User can view workflow definitions and their execution history.
- [ ] User can trigger a dashboard spec regeneration and see the layout update.
- [ ] User can toggle between light and dark themes with the theme persisted across sessions.
- [ ] Dashboard shows a loading skeleton while fetching the spec, not a blank screen.
- [ ] User can view notifications from the notification system.
- [ ] Dashboard gracefully handles a missing spec (404) with a clear empty state and regenerate prompt.
- [ ] User can view a DAG visualization of a task's execution graph with nodes and edges rendered correctly.
- [ ] User can click a node in the DAG view to see its details (instructions, output, status) in a spotlight panel.
- [ ] DAG node status badges update to reflect completed, running, pending, and failed states.
- [ ] User can view a workflow monitor showing real-time task progress with spotlight details.
- [ ] Sidecar status indicator displays the current state of the local model inference engine.

## Edge Cases to Probe

- Clicking a DAG node while another node's detail panel is already open to verify clean transition.
- Starting the dashboard when the backend is offline and verifying clean error messaging.
- Triggering spec regeneration while the dashboard is already displaying data.
- Refreshing the browser while a task is in progress to verify the spec re-fetch is seamless.
- Viewing the dashboard with no tasks, no events, and no workflows to verify empty states.
- Resizing the window between desktop and mobile widths to verify responsive layout.
- Rapidly switching between light and dark themes to verify no flash-of-unstyled-content.

## Anti-Patterns to Watch For

- [ ] Blank screen or React runtime crash when the layout spec contains an unknown component type.
- [ ] Raw JSON errors or network stack traces displayed directly in the UI.
- [ ] Stale data persisting after a spec regeneration completes.
- [ ] Theme toggle not persisting across page reloads.
- [ ] Loading spinner that never resolves when the backend is unreachable (should timeout with message).
- [ ] Task spotlight/detail view showing empty content for a task that has completed nodes.
- [ ] Broken layout when the OpenUI spec includes nested grid or flex containers.
- [ ] Regenerate button remaining in "loading" state after the regeneration request completes or fails.
