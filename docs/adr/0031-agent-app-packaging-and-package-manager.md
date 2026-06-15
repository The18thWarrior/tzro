# Agent App Packaging Standard and Package Manager Design

The `.tzroapp` packaging format and Package Manager service were designed to enable composable, third-party capability extensions for tzro. Several architectural trade-offs were evaluated during the design grilling session.

## Key Decisions

**Composable, not exclusive.** Multiple Agent Apps coexist additively on a single tzro instance. Each app adds tools, prompts, and migrations alongside existing apps rather than replacing them. This was chosen over a "flavor" model (one active personality at a time) because tzro's core value is multi-tool orchestration — a user needs HubSpot AND Slack AND PostgreSQL tools active simultaneously within the same DAG workflow.

**App-scoped tool namespacing via short slug.** Each app declares a locally-unique short slug ID (e.g., `hubspot`). All tools are registered as `{appId}_{toolName}` (e.g., `hubspot_create_contact`). This prevents tool name collisions between apps. Global uniqueness (reverse-domain naming) was rejected as unnecessarily verbose for locally-installed packages — especially since a centralized registry is out of scope.

**Incremental MCP registration, no full reload.** The Package Manager registers new MCP daemons incrementally on the live `MCPRegistry` rather than rewriting `mcp_config.json` and calling `LoadConfig` (which tears down all running daemons). Each app's MCP config is stored in `.tzro/apps/{appId}/mcp.json` and merged at daemon startup. This avoids disrupting in-flight task execution during install.

**Trust the developer for capability declarations.** The manifest declares capabilities (`compute`, `filesystem_read`, `network_outbound`, etc.) that map to Proactivity Ladder tiers. The Package Manager presents these to the user at install time. No runtime enforcement beyond what sandboxes naturally provide (WASM is hermetic; MCP servers are trusted by declaration). This mirrors the desktop OS model — the developer declares, the user decides. Runtime capability enforcement was rejected because it limits what future app types can do.

**Soft-disable uninstall with explicit purge.** `tzro uninstall` deregisters tools and stops daemons but preserves database tables and data. `tzro purge` (or `--purge` flag) performs destructive cleanup. Migration state is tracked via a `_tzro_migrations` table recording `(app_id, migration_file, applied_at)` to support incremental upgrades.

**Convention-enforced database isolation.** All apps share `tzro.db`. Table names are prefixed with the app ID by convention (`hubspot_leads`, `salesforce_contacts`). Per-app SQLite databases were rejected because they prevent cross-app SQL queries that composable workflows need. Query-rewriting isolation layers were rejected as complex and fragile for marginal security gain given the trust-the-developer posture.

**App prompts are pre-authored Procedural Micro-Skills.** The `prompts/` directory in a `.tzroapp` archive contains developer-authored Markdown SOPs in the existing Procedural Micro-Skill format. They are indexed into the skill store on install, giving the Local Model expert guidance from day one without waiting for trajectory extraction.

**Local file distribution only at GA.** Apps are installed from local file paths (`tzro install ./hubspot.tzroapp`). URL-based installation is deferred to post-GA to avoid introducing a network fetch and trust surface without supporting infrastructure (checksums, verified publishers).
