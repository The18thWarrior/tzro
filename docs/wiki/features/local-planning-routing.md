# Feature: Dynamic Local Planning and Routing

## Problem & Solution

* **Context:** `tzro` currently uses either a pure cloud-based strategist (when a cloud API key is present) or a pure local fallback. This either/or routing lacks the nuance to dynamically assess task complexity or respect data privacy guidelines.
* **Value:** The Dynamic Planner Router allows hybrid planning. It evaluates incoming user tasks at runtime, keeping sensitive codebase queries and simple execution loops strictly on-device, while dynamically escalating complex strategic prompts to cloud models (provided it is allowed by the workspace privacy settings).

## Technical Design Summary

* **Core Modules:**
  * `internal/task/task.go`: Refactor the `Plan()` compiler logic.
  * `internal/config/config.go`: Add new config fields for `PrivacyLevel`, `RestrictedDirectories`, and `ComplexityThreshold`.
  * `internal/inference/routing.go`: Integrate the router grading rules with `LocalModelManager`.
* **Data Models / APIs:**
  * Config updates to the `EngineConfig` struct.
  * Extends task intake payloads with `RestrictedDirectories` and active directory path boundary details.

## References

* **PRD:** [PRD.md](../../../.scratch/local-planning-routing/PRD.md)
* **Log Entry:** [Log Link](../log.md#2026-06-08-1915-document--prd-dynamic-local-planning-and-routing)
