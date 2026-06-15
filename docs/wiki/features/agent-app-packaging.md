# Feature: Agent App Packaging Standard

## Problem & Solution

* **Context:** Today, extending `tzro` with new tools, prompts, or agents requires fragmented manual interventions (copying WASMs, setting config keys, running database SQL scripts).
* **Value:** The Agent App Packaging Standard (`.tzroapp`) standardizes application distribution. It bundles tools, pre-authored Procedural Micro-Skills, SQLite migrations, and capability declarations into a single distributable zip package, enabling plug-and-play local agent installations.

## Design Decisions (Resolved)

* **Composable model:** Multiple Agent Apps coexist additively on one tzro instance. Not a "flavor" — install many apps simultaneously.
* **App identity:** Short slug (e.g., `hubspot`), locally unique. Tools namespaced as `{appId}_{toolName}`.
* **Tool types:** WASM (Sandboxed Micro-Skills) and custom MCP server scripts. The app extends MCP config, not replaces it.
* **Incremental registration:** Package Manager calls `RegisterDaemon` on live MCPRegistry — no full daemon reload, no disruption to running tasks.
* **Permissions:** Manifest declares capabilities mapped to Proactivity Ladder tiers. Trust the developer; user approves at install time via Attention Queue.
* **Uninstall lifecycle:** Soft-disable (deregister tools, stop daemons, preserve data) + explicit `purge` for destructive cleanup. Migration tracking via `_tzro_migrations` table.
* **Prompts:** Pre-authored Procedural Micro-Skills in existing Markdown SOP format, indexed into skill store on install.
* **Database isolation:** Convention-enforced (table name prefixing), shared `tzro.db`. No per-app databases.
* **Distribution:** Local file paths only at GA. URL install deferred.

## Technical Design Summary

* **Core Modules:**
  * `internal/packagemanager/`: Parse manifests, run database migrations, extract assets safely.
  * `internal/tools/tools.go`: Scan `.tzro/apps/` on startup and dynamically register all validated WASM and MCP tools.
  * `internal/mcp/mcp.go`: Incremental `RegisterDaemon` for live app installation without full reload.
  * `internal/cli/packagemanager.go`: CLI subcommands (`install`, `uninstall`, `list`, `purge`).
* **Data Models / APIs:**
  * The `tzro.manifest.json` schema declaring `id`, `name`, `version`, `capabilities`, `tools`, `prompts`, and `migrations`.
* **Directory Structure:**
  * `.tzro/apps/{appId}/` — extracted app assets (WASM binaries, prompt SOPs, MCP configs, migration files).

## References

* **PRD:** [PRD.md](../../../.scratch/agent-app-packaging/PRD.md)
* **ADR:** [ADR-0031: Agent App Packaging](../../adr/0031-agent-app-packaging-and-package-manager.md)
* **Glossary:** [CONTEXT.md](../../../CONTEXT.md) — Agent App, Package Manager, Procedural Micro-Skill
* **Log Entry:** [Log Link](../log.md#2026-06-08-1915-document--prd-agent-app-packaging-standard)
