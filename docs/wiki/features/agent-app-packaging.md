# Feature: Agent App Packaging Standard

## Problem & Solution

* **Context:** Today, extending `tzro` with new tools, prompts, or agents requires fragmented manual interventions (copying WASMs, setting config keys, running database SQL scripts).
* **Value:** The Agent App Packaging Standard (`.tzroapp`) standardizes application distribution. It bundles prompts, tools, SQLite migrations, and permission requests into a single distributable zip package, enabling plug-and-play local agent installations.

## Technical Design Summary

* **Core Modules:**
  * `internal/packagemanager/`: Parse manifests, run database migrations, extract assets safely.
  * `internal/tools/tools.go`: Scrape and load validated package tools dynamically.
  * `internal/cli/packagemanager.go`: Build installer commands (`install`, `uninstall`, `list`).
* **Data Models / APIs:**
  * The `tzro.manifest.json` schema declaring app metadata, tools, promotions, permissions, and database migration paths.

## References

* **PRD:** [PRD.md](../../../.scratch/agent-app-packaging/PRD.md)
* **Log Entry:** [Log Link](../log.md#2026-06-08-1915-document--prd-agent-app-packaging-standard)
