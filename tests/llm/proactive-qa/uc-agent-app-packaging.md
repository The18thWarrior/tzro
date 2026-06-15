# Use Case: Agent App Package Management

**Actor**: Developer installing, managing, or removing Agent Apps via CLI or MCP tools.
**Route**: CLI (`tzro app install/list/uninstall`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer wants to install a `.tzroapp` archive containing an Agent App — including its tools, migrations, and prompts — and have the engine register the app's capabilities, run any database migrations, and make its tools available for task execution. They also want to list installed apps, deactivate them, or fully uninstall them with cleanup.

## Preconditions

- The tzro daemon is running with a valid database connection.
- A `.tzroapp` archive file is available on the local filesystem.
- The archive contains a valid `manifest.json` with name, version, and capabilities.

## Success Criteria

- [ ] Installing a valid `.tzroapp` archive registers the app and makes its tools available for task execution.
- [ ] After installation, the app appears in the installed apps list with correct name, version, and capabilities.
- [ ] App database migrations are applied during install and tracked in the migrations table.
- [ ] Uninstalling an app removes its files, deregisters its tools, and marks it as removed.
- [ ] Listing apps shows all installed apps with their current status (active/inactive).
- [ ] Installing an app with a duplicate name returns a clear error without corrupting existing state.
- [ ] The package manager schema tables are auto-created on first use.

## Edge Cases to Probe

- Installing an archive with a malformed or missing `manifest.json`.
- Installing an archive with a migration that fails (SQL error) to verify rollback behavior.
- Uninstalling an app that has active tasks depending on its tools.
- Installing an app when the `apps/` directory doesn't exist yet to verify auto-creation.
- Installing an oversized archive to verify no memory exhaustion.

## Anti-Patterns to Watch For

- [ ] Partial install leaves orphaned files or database entries when a migration fails.
- [ ] Tools from an uninstalled app remain callable after removal.
- [ ] App list shows stale status after an install/uninstall operation.
- [ ] Archive extraction writes files outside the designated apps directory (path traversal).
- [ ] Missing or corrupt manifest crashes the daemon instead of returning an error.
- [ ] Migration table not created before first migration attempt, causing SQL errors.
