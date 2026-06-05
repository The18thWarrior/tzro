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
- [ ] User can toggle the mobile menu and see the Mobile Navigation Drawer with navigation links.
- [ ] User can switch between Raw Input and Compacted panels using the mobile tab switcher on screens smaller than 768px.
- [ ] User can click Claude Desktop, Cursor Settings, and Antigravity Config tabs in the MCP Setup section and see the corresponding configuration format displayed in the code block.
- [ ] User can view the "Autonomous Agent Offload & Wait Protocol" detailing the Offload Decision Rule.
- [ ] User can see suggested prompt templates for Research, Multi-System Automation, and Codebase Exploration (Probe Node) on the onboarding page.
- [ ] User sees the newly documented Handshake Verification test using a JSON-RPC initialize command and the critical warnings about standard input/output redirection.

## Edge Cases to Probe

- Prompting the agent with empty, excessively long, or malicious prompt injections.
- Refreshing the web page while an active background task is executing to verify state persistence.
- Starting the dashboard when the backend is offline, and verifying clean loading/retry behavior.
- Operating the dashboard on mobile, tablet, and desktop viewports to ensure responsive container queries.
- Resizing the window to mobile width (<768px) and toggling the hamburger menu, then checking if the mobile navigation drawer closes when a link is clicked or when clicking outside.
- Running the compaction pipeline on mobile and verifying that the view automatically switches to the Compacted panel upon completion.
- Accessing the Handshake Verification section and copying the initialization JSON payload.
- Reviewing the offload decision rule and verifying layout responsiveness of the policy grid on narrow viewports.

## Anti-Patterns to Watch For

- [ ] Entire screen flashes white or displays a blank screen due to React runtime crashes.
- [ ] Broken layouts, text wrapping overlaps, or unstyled default HTML components.
- [ ] Stale data indicators persisting when tasks have completed or failed.
- [ ] Raw backend JSON errors or network stack traces displayed directly in the user interface.
- [ ] Buttons or input forms becoming dead/unresponsive without visual loading/disabled cues.
- [ ] Mobile navigation drawer or hamburger menu overlapping other UI elements or blocking interaction.
- [ ] Mobile tab switcher displaying incorrect payload size metrics or failing to display the active panel.
- [ ] The "Autonomous Agent Offload & Wait Protocol" section breaks layout or overlaps on mobile screen widths.
