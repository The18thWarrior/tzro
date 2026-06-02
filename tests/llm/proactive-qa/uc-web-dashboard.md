# Use Case: Web Dashboard Agent Interface

**Actor**: Developer or End User interacting with the tzro server through their web browser.
**Route**: / (React Frontend Web App)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

A developer wants a beautiful, responsive visual interface to orchestrate, configure, and inspect the state of agents, memories, tasks, and system configurations.

## Preconditions

- The tzro backend daemon is running and reachable on `http://localhost:36888`.
- The Vite web development server is running on `http://localhost:8000` (or frontend files served).
- A modern web browser is used to access the dashboard.

## Success Criteria

- [ ] User sees a visually premium dashboard showing system telemetry, active tasks, memory nodes, and running services.
- [ ] User can view a real-time stream of agent operations, tool executions, and compilation steps.
- [ ] User can view the relational memory graph in an interactive canvas or neighborhood view.
- [ ] User can toggle between different execution strategies (T0 Direct, T1 Planned, T2 Supervised) for natural language queries.
- [ ] User can trigger new agent tasks through the chat interface and receive streaming replies.
- [ ] User can inspect the active tool catalog, loaded skills, and registered MCP Hosts.
- [ ] User sees beautiful animations, clean dark-theme layout, and harmonious HSL tailored colors.

## Edge Cases to Probe

- Prompting the agent with empty, excessively long, or malicious prompt injections.
- Refreshing the web page while an active background task is executing to verify state persistence.
- Starting the dashboard when the backend is offline, and verifying clean loading/retry behavior.
- Operating the dashboard on mobile, tablet, and desktop viewports to ensure responsive container queries.

## Anti-Patterns to Watch For

- [ ] Entire screen flashes white or displays a blank screen due to React runtime crashes.
- [ ] Broken layouts, text wrapping overlaps, or unstyled default HTML components.
- [ ] Stale data indicators persisting when tasks have completed or failed.
- [ ] Raw backend JSON errors or network stack traces displayed directly in the user interface.
- [ ] Buttons or input forms becoming dead/unresponsive without visual loading/disabled cues.
