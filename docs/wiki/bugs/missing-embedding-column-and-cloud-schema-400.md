# Bug Post-Mortem: Missing Embedding SQLite Column & Cloud Schema Format HTTP 400

## Symptom

- **Description**:
  1. During conversational execution (simple chat), the application threw SQL errors:
     `[Memory Error] Failed to query memories: SQL logic error: no such column: embedding (1)` and `Failed to query nodes: SQL logic error: no such column: embedding (1)`.
  2. Background telemetry Observer audits failed on Knowledge Graph extraction with warning:
     `[Observer Warning] KG extraction LLM call failed: cloud API returned status 400: ... Invalid JSON payload received. Unknown name "schema" at 'response_format': Cannot find field.`
- **Reproduction**:
  1. DB: Starting `tzro` on a pre-existing `tzro.db` database created before the local ONNX vector memory features were introduced (which lacked the `embedding` column on tables `fact_memories` and `kg_nodes`).
  2. API: Triggering background Observer audits calling standard cloud LLM APIs (Gemini OpenAI-compatible endpoint or standard OpenAI endpoint) with a custom `schema` field placed directly inside the `response_format` JSON payload of type `"json_object"`.

## Diagnosis

- **Hypotheses**:
  1. The SQLite tables `fact_memories` and `kg_nodes` already existed on the user's filesystem from an older version, so `CREATE TABLE IF NOT EXISTS` didn't execute, leaving the `embedding` column missing.
  2. Gemini's OpenAI-compatibility endpoint (`generativelanguage.googleapis.com`) and standard OpenAI chat completion spec do not support a custom `"schema"` key at the root of `response_format` when type is `"json_object"`.
- **Root Cause**:
  1. Missing automated migration paths for existing SQLite tables when new feature columns are added.
  2. The cloud completion API request constructor (`internal/inference/cloud_model.go`) hard-coded the `llama-server`-specific JSON schema format parameter `"schema"` directly inside the `"json_object"` `response_format` payload, causing remote APIs to reject the payload with `HTTP 400 Bad Request`.

## Resolution

- **Fix**:
  1. **SQLite Migrations**: Implemented `ensureColumnExists` in `internal/memory/memory.go` utilizing SQLite's standard `PRAGMA table_info(table_name)` introspection. It dynamically checks if the `embedding` column is missing from `fact_memories` and `kg_nodes` tables during initialization, executing `ALTER TABLE ... ADD COLUMN ...` on old databases.
  2. **Cloud API Schema Compatibility**: Updated `callCloudModel` and `CallCloudModelStream` inside `internal/inference/cloud_model.go` to omit the custom `"schema"` field in `response_format` payloads when type is `"json_object"`. It relies on the robust schema definition detailed in system/user prompts, which is fully compatible with both OpenAI and Gemini OpenAI-compatible endpoints.
- **Regression Prevention**: 2. Added a robust integration test `TestSqliteDatabase_SchemaMigration` in `internal/memory/memory_test.go` that manually creates a database with pre-vector schemas (no `embedding` columns), initializes it with the new code, and asserts that missing columns are safely auto-added and writable.

## Long-term Prevention

- Enforce schema versioning and programmatic columns check for SQLite initialization in future feature expansions.
- Avoid passing non-standard/custom vendor extensions (such as llama.cpp-specific GBNF fields) to standard public Cloud API handlers, maintaining separate, well-defined payload serializers for local and cloud models.
