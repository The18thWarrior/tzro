# Feature: Dual-Audience Hardening

## Problem & Solution

* **Context:** `tzro` is ready for GA as a developer-centric framework, but lacks the necessary UX, lifecycle, and privacy controls to deliver on the promises made to non-technical end-users (e.g., Cursor/Claude Desktop users). Gaps include cloud-fallback PII leakage risks, IDE child process lifecycle termination, disconnected human-in-the-loop approvals, and incomplete CLI/MCP wiring of the Package Manager.
* **Value:** Hardening these four boundaries creates a secure, durable, and seamless local-first runtime for non-developers. It prevents data leaks via a strict "Fail-Local" policy, ensures background tasks execute persistently overnight via detached daemon execution, enables editor-native task management (resuming and approving), and wires up the `.tzroapp` Package Manager CLI and MCP server tools to allow dynamic capability extension.

## Technical Design Summary

* **Core Modules:**
  * `internal/config/config.go`: Extend `PrivacyLevel` to govern execution-time cloud fallback.
  * `internal/executor/`: Check `PrivacyLevel == "strict-local"` in `confidence.go`, `executor.go`, and `retry.go` to block cloud retry/escalation and fail immediately on failures.
  * `cmd/tzro-mcp/`: Refactor `tzro-mcp` to detached-spawn `tzrod` on boot. If the daemon is active, act as a client proxy forwarding run, status, and resume commands to the daemon's REST API, and stream progress SSE events back to the client. Bypasses starting in-process background agents unless daemon startup fails (resilient fallback).
  * `internal/server/`: Expose REST endpoints on `tzrod` (`POST /api/tasks/resume`, `POST /api/apps/install`, `POST /api/apps/uninstall`, `GET /api/apps`).
  * `internal/cli/app.go` & `internal/packagemanager/`: Wire up `tzro app` commands. Implement `LoadInstalledApps()` to load active apps on boot and reload them dynamically on daemon REST calls.
* **Data Models / APIs:**
  * Extended semantic checks for `PrivacyLevel` values in `CONTEXT.md`.
  * Daemon HTTP REST API endpoints for task execution proxying, Server-Sent Events (SSE) streaming, and package management.
  * `_tzro_apps` and `_tzro_migrations` SQLite tables for app state tracking.

## References

* **PRD:** [PRD.md](../../../.scratch/dual-audience-hardening/PRD.md)
* **Log Entry:** [Log Link](../log.md#[2026-06-15t205500-0700]-document---prd-dual-audience-hardening)
