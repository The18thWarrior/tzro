# Use Case: App Installation and Management

**Actor**: Developer installing or managing third-party tool apps in the tzro engine.
**Route**: HTTP API (`/api/apps`, `/api/apps/install`, `/api/apps/uninstall`)
**Backend**: http://localhost:36888/api/apps
**Priority**: P1

---

## Intent

A user wants to extend tzro's capabilities by installing community or custom tool apps via the package manager. Apps bundle WASM tools and optional MCP server daemons that register with the engine at install time and auto-load on startup.

## Preconditions

- App/daemon is running and accessible
- A valid app manifest (`tzro.manifest.json`) exists at the install source
- Database is initialized with the `_tzro_apps` schema

## Success Criteria

- [ ] User can list installed apps via `GET /api/apps`
- [ ] User can install an app via `POST /api/apps/install` with a valid source path
- [ ] Installed app's tools are registered and available in the tool registry
- [ ] Installed app's MCP daemon (if any) is started and registered
- [ ] User can uninstall an app via `POST /api/apps/uninstall`
- [ ] Uninstalled app's tools are removed from the registry
- [ ] Previously installed apps auto-load on daemon restart via `LoadInstalledApps()`
- [ ] App data persists in the database with status tracking (active/removed)

## Edge Cases to Probe

- Install an app with a missing or malformed manifest
- Install the same app twice — should update, not duplicate
- Uninstall an app that isn't installed
- Install an app with conflicting tool names
- Daemon restart — verify all active apps reload

## Anti-Patterns to Watch For

- [ ] Orphaned MCP daemon processes after uninstall
- [ ] Database rows left in `active` status after uninstall
- [ ] Tools from uninstalled apps still appearing in the registry
- [ ] Silent failure when manifest parsing fails
- [ ] App installation modifying files outside `.tzro/apps/`
