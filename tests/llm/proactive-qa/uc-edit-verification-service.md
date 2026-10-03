# Use Case: Edit Verification Service

**Actor**: AI coding agent (Antigravity, Claude Code, Cursor) performing multi-file modifications, or developer running automated CLI verification
**Route**: CLI — `tzro edit-and-verify [--request <path>|-]` | MCP — tool `tzro_edit_and_verify` | Internal Go API — `pkg/verification.Service.ApplyAndVerify`
**Backend**: Edit verification service (`pkg/verification/`) with workspace file access, exact command runner, and local SQLite store
**Priority**: P0

---

## Intent

An AI coding agent wants to modify code and verify changes without spending multiple conversational turns running separate bash commands and reading terminal output. The edit-and-verify service fuses multi-file text editing with repository-defined verification presets (`.tzro/verification.yaml`) into a single atomic operation. It applies edits with in-memory preflight validation, executes checks (with dependency ordering and cancellation support), retains failure diagnostics and evidence in the local store, and returns a structured Verification Summary in a single turn. On test failure, changes are preserved so diagnostics match the workspace.

## Preconditions

- `tzro` binary is installed and on PATH (or MCP server active)
- Workspace directory is initialized and accessible
- Target files exist for replace edits; parent directories exist or are creatable for create edits
- Optional `.tzro/verification.yaml` defining checks, execution timeouts, dependency DAGs (`depends_on`), or parallel groups
- Optional `.tzro/store.db` available for retaining large failure output evidence

## Success Criteria

- [ ] Multi-file literal text replacements (`kind: "replace"`) locate `old_text` and substitute `new_text` across one or more files
- [ ] New file creation (`kind: "create"`) creates a new file with specified `content` and standard permissions (`0644`)
- [ ] In-memory preflight validation ensures all edits match expected counts (`expected_matches`, default 1) before modifying any file on disk
- [ ] If any edit fails preflight (e.g. `old_text` not found or count mismatch), the entire batch is rejected with `application: "rejected"` and zero files are modified
- [ ] Path traversal (`../`) and absolute paths are rejected, and symlink targets outside the workspace are blocked (`resolveSafe`)
- [ ] Mixing `create` and `replace` on the same file in a single batch is rejected
- [ ] If a write error occurs mid-application, the service reports `application: "partial"`, identifies `changed_files` and `uncertain_files`, and aborts verification checks
- [ ] Preset checks configured in `.tzro/verification.yaml` execute sequentially or in parallel groups with exact arguments (no shell interpolation)
- [ ] Dependent checks configured with `depends_on` are skipped with status `blocked` if their prerequisite check fails
- [ ] Circular dependencies in `.tzro/verification.yaml` are detected during preset loading and return an explicit configuration error
- [ ] Check failure does NOT roll back applied files; changes are retained so compiler and test diagnostics reflect the current state
- [ ] Check outputs over 500 bytes have raw logs retained in `.tzro/store.db` with an `evidence_ref`, while summary provides representative diagnostics (capped at 10 lines) and an expand reference
- [ ] Whole-operation deadline (`Options.Timeout` or preset timeout) stops running checks cleanly using process group cancellation (`SIGKILL`)
- [ ] MCP cancellation notification (`$/cancelRequest`) cleanly aborts active command execution and marks status `cancelled`
- [ ] Returns a structured JSON summary conforming to `tzro.verification.v1` schema with `application`, `verification`, `termination`, `retention`, and `checks` arrays
- [ ] Works identically via CLI (`tzro edit-and-verify --request -`) and MCP tool (`tzro_edit_and_verify`)
- [ ] If `.tzro/verification.yaml` is absent, edits apply successfully and summary reports `verification: "not_configured"`

## Edge Cases to Probe

- Submitting an empty edit batch (rejected with `rejection_reason: "empty edit batch"`)
- Target text appears multiple times when `expected_matches: 1` (rejected during preflight)
- Target text appears multiple times and `expected_matches` is set to the exact count (replaces all occurrences)
- Creating a file that already exists without specifying replace (rejected during preflight)
- Creating an empty file (`content: ""`)
- Writing to a read-only file or failing mid-write (reports `partial` application, logs `uncertain_files`)
- Preset with slow command exceeding deadline (aborts process group, marks `timed_out`, returns `termination: "deadline"`)
- Preset with startup error (e.g. command binary not found) records `status: "failed"` with startup error diagnostics

## Anti-Patterns to Watch For

- [ ] Applying edits to disk before preflighting the entire batch (breaking atomicity)
- [ ] Rolling back valid code edits when verification test checks fail
- [ ] Shell interpolation vulnerabilities in preset check commands (must execute exact `argv`)
- [ ] Hanging or orphaned zombie processes when checks time out or context is cancelled
- [ ] Inundating the agent's context window with megabytes of raw test output instead of capped diagnostics and evidence pointers
- [ ] Silent failure or silent omission when an edit path escapes the workspace root
