# Durable proactive notification system

We implement a durable, hybrid proactive notification system allowing background Tasks, Workflows, the Sidecar, and the Observer Agent to communicate asynchronous lifecycle states, warnings, or action requests securely to the user.

We introduce a deep `internal/notification` Go package and a durable SQLite persistence table `durable_notifications` within the Relational Knowledge Graph.

## Key Design Rules

1. **Durable Persistence**: All notifications are saved in a SQLite database table. On page load, the frontend queries `GET /api/notifications` to fetch past alerts and hydrate the panel, ensuring no critical events are lost on page refreshes or restarts.
2. **Real-time SSE Fan-out**: When a notification is created, it is saved in SQLite and immediately published as a `StreamChunk` with source `"notification"` over the `stream.GlobalBus` to establish immediate SSE push-delivery.
3. **Deep Linking via Targets**: Notifications contain nullable relations: `task_id`, `workflow_id`, and `target_id` (a general system entity identifier like a KGNode or chat thread). Clicking a notification automatically shifts dashboard focus or centers node views on that target.
4. **Observer Agent Rollups & Debouncing**: Under heavy execution or cron schedules, the Observer Agent aggregates telemetry events to produce consolidated batch notifications, while the notification publisher filters/updates identical unread warnings to prevent user notification fatigue.

## Considered Options

- **Transient SSE-only Alerting**: Stream alerts strictly in memory over SSE. Rejected — alerts would be permanently lost if the user refreshed their browser or kept the dashboard closed during long-running background tasks.
- **Unified Event Table**: Store all raw telemetry events (node started, completed, etc.) in a massive SQLite table and have the frontend query them. Rejected — creates severe database bloat and shifts complex presentation and state-filtering logic (unread vs read) to the frontend.
