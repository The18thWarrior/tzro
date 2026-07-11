---
name: prepare-release
description: Use when preparing a release, finalizing a feature branch, or when the user says "prepare release", "ship it", "release prep", or "get this ready for release". Orchestrates the full pre-release pipeline from staging through commit.
---

# Prepare Release

Orchestrate the complete pre-release pipeline: stage → spec → test → QA → lint → release notes → commit. Each phase gates the next — failures must be resolved before proceeding.

---

## Parameters

| Parameter         | Required | Description                                                                                                                                                                                                                                                                                     |
| :---------------- | :------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--from <commit>` | No       | A commit SHA, tag, or ref. When provided, all diff analysis (staging, specs, release notes) covers the range `<commit>..HEAD` instead of only the current working-directory changes. Use this when preparing a release that spans multiple commits — e.g., `--from v0.2.0` or `--from abc1234`. |

If the user provides a commit reference (e.g., "from commit abc1234", "since the last release tag", "changes from d795199 to now"), treat it as `FROM_COMMIT=<ref>`.

If no `--from` is provided, behavior is unchanged — the skill operates on the working directory / staged files as before.

---

## 1. Shell Preamble

**Prepend to EVERY terminal command:**

```bash
source ~/.nvm/nvm.sh && nvm use 22 > /dev/null && export PATH="/usr/local/go/bin:$PATH"
```

---

## 2. Execution Pipeline

Execute these phases **in strict order**. Never skip a phase. Never proceed past a failing phase.

```
┌─────────────────────────────────────────────────────────────────────┐
│  Phase 1: Stage All Files                                          │
│  Phase 2: Generate Use Case Specs  (write-use-case-spec skill)     │
│  Phase 3: Run Tests + Fix Failures (pnpm test, loop until green)   │
│  Phase 4: Proactive QA             (tzro MCP QA, fix loop)         │
│  Phase 5: Lint + Format            (pnpm prepare:format, fix)      │
│  Phase 6: Update Architecture Docs  (docs/ARCHITECTURE.md)         │
│  Phase 7: Update README             (README.md)                    │
│  Phase 8: Generate Release Notes   (docs/release-notes/)           │
│  Phase 9: Stage + Commit           (git add, commit)               │
└─────────────────────────────────────────────────────────────────────┘
```

---

### Phase 1 — Stage All Files

```bash
git add -A
```

**If `FROM_COMMIT` is set:**

Report the full change range:

```bash
git diff --stat $FROM_COMMIT..HEAD
git log --oneline $FROM_COMMIT..HEAD
```

This becomes the canonical change set for Phases 2 and 6. Store the commit list and diff stat for later phases.

**If `FROM_COMMIT` is NOT set:**

Report what was staged: `git diff --cached --stat`

If nothing is staged and working directory is clean, inform the user: **"Nothing to release — working directory is clean."** Stop.

---

### Phase 2 — Generate Use Case Specs

**REQUIRED SUB-SKILL:** Execute `write-use-case-spec`

Read `.agents/skills/write-use-case-spec/SKILL.md` and follow its full execution protocol against the staged files. This generates or updates `uc-*.md` specs in `tests/llm/proactive-qa/` for any new user-facing features.

After specs are generated, stage them:

```bash
git add tests/llm/proactive-qa/
```

---

### Phase 3 — Run Tests + Fix Failures

Run the test suite:

```bash
pnpm test
```

**If tests pass:** Proceed to Phase 4.

**If tests fail:** Enter the fix loop:

1. Read the test output — identify failing tests and error messages
2. Investigate the source files responsible for the failure
3. Apply the minimal fix
4. Re-run `pnpm test`
5. Repeat until all tests pass

**Cap:** Maximum 5 fix iterations. If tests still fail after 5 attempts, **stop** and report the remaining failures to the user with a summary of what was tried.

Stage any fixes:

```bash
git add -A
```

---

### Phase 4 — Proactive QA via MCP

**Prerequisite check:** Verify the tzro daemon is reachable:

```bash
curl -sf http://localhost:36888/health > /dev/null 2>&1 && echo "tzro daemon: OK" || echo "tzro daemon: NOT RUNNING"
```

If the daemon is not running, start it:

```bash
TZRO_DIR=$(pwd) ./bin/tzrod &
```

Wait a few seconds, then re-check health.

**Step 1 — Gather use case specs:**

List the `uc-*.md` files in `tests/llm/proactive-qa/`. These define the QA scope.

**Step 2 — Delegate QA to tzro:**

Call `tzro_run` with a prompt that instructs the engine to:
- Read each `uc-*.md` spec in `tests/llm/proactive-qa/`
- For each spec, verify the success criteria against the current codebase using Probe Nodes
- Classify findings by severity (P0 critical / P1 major / P2 minor / P3 cosmetic)
- Produce a structured QA report with file paths and line numbers for each finding

Example prompt:
```
Read each use case spec (uc-*.md) in tests/llm/proactive-qa/. For each spec,
verify the success criteria against the current codebase. Classify findings as
P0/P1/P2/P3. Produce a structured QA report.
```

**Step 3 — Wait for completion:**

Follow the Wait Protocol:
1. Stop calling other tools after `tzro_run` returns
2. Set a one-shot timer via `schedule` (60–120 seconds)
3. When notified, check `tzro_status` for the task
4. Resume only when status is `completed`
5. Consume **only** `terminal_synthesis` — do not read individual node outputs

**Step 4 — Evaluate findings from `terminal_synthesis`:**

- If **0 findings** at P0 or P1: Proceed to Phase 5.
- If **any P0 or P1 findings**: Enter the fix loop:
  1. For each P0/P1 finding, investigate the root cause in the codebase
  2. Apply the fix
  3. Re-delegate scoped QA via `tzro_run` for affected specs only (not the full suite)
  4. Wait for completion using the same Wait Protocol
  5. Repeat until no P0/P1 findings remain

**Cap:** Maximum 3 QA fix iterations. If P0/P1 findings persist after 3 iterations, **stop** and report them to the user.

P2/P3 findings are logged in the report but do not block the release.

Stage any fixes:

```bash
git add -A
```

---

### Phase 5 — Lint + Format

```bash
pnpm run prepare:format
```

This runs `pnpm lint:fix && npx prettier ./ --write`.

**If lint errors remain after auto-fix:** Read the lint output, fix the errors manually, then re-run.

**Cap:** Maximum 3 lint fix iterations.

Stage formatting changes:

```bash
git add -A
```

---

### Phase 6 — Update Architecture Documentation

Inspect the changes in the release (either using `git diff --cached` or `git diff $FROM_COMMIT..HEAD` depending on whether `FROM_COMMIT` is set) to check if any files related to core subsystems have been modified.

**Files of interest:**
- Go modules/packages: `internal/compiler/`, `internal/executor/`, `internal/memory/`, `internal/agent/`, `internal/sentinel/`, `internal/observer/`, `internal/proactivity/`, `internal/packagemanager/`, `internal/wasm/`, `internal/mcp/`
- Tool schemas & SDK entrypoints: `cmd/tzro-mcp/`, `cmd/tzro/`
- Manifests/configurations: `go.mod`

**Execution protocol:**
1. If any of the files of interest are modified, review [ARCHITECTURE.md](file:///Users/jp/Desktop/Repos/tzro/docs/ARCHITECTURE.md) sections against the modified code.
2. Update the system description, Go SDK examples, hooks description, or JSON schemas inside `docs/ARCHITECTURE.md` to keep the architecture documentation synchronized with the implementation.
3. If no architectural changes are present in the diff, proceed silently.
4. Stage the updated architecture document:
   ```bash
   git add docs/ARCHITECTURE.md
   ```

---

### Phase 7 — Update README

Inspect the changes in the release (either `git diff --cached` or `git diff $FROM_COMMIT..HEAD` depending on whether `FROM_COMMIT` is set) and review [README.md](file:///Users/jp/Desktop/Repos/tzro/README.md) for any sections that are now stale.

**Sections to check:**

| README Section | What to verify |
|:---|:---|
| **MCP Tool Interface** (tool count & tables) | If tools were added, removed, renamed, or moved between tiers, update the tool tables and the `## 📡 MCP Tool Interface — N Tools` heading count |
| **Version Highlights** (`## 🆕 vX.Y.Z Highlights`) | If a new version is being released, replace the highlights section with a bullet list summarizing the major user-facing changes in this release |
| **Quickstart** (`## ⚡ Quickstart`) | If install scripts, binary names, or setup steps changed, update the quickstart commands |
| **Architecture diagram** | If the delegation flow changed (new MCP tiers, new sidecar components), update the ASCII diagram |
| **Performance matrix** | If benchmark numbers or resource footprint changed materially, update the comparison table |
| **MCP Server Integration** (config examples) | If environment variables, binary paths, or supported clients changed, update the JSON snippets |
| **Development & Testing** | If build/test commands changed (e.g., new test runner, new lint script), update the commands |

**Execution protocol:**
1. Diff the files of interest against the README content. Identify any sections where the README contradicts or omits the current state.
2. Apply **only factual corrections** — do not rewrite prose style, marketing copy, or section ordering unless something is factually wrong.
3. If no README sections are stale, proceed silently.
4. Stage the updated README:
   ```bash
   git add README.md
   ```

---

### Phase 8 — Generate Release Notes

**REQUIRED SUB-SKILL:** Execute `writing-release-notes`

Read `.agents/skills/writing-release-notes/SKILL.md` and follow its full execution protocol.

**If `FROM_COMMIT` is set:** Pass the commit range to the release notes skill. It will use `git diff $FROM_COMMIT..HEAD` and `git log $FROM_COMMIT..HEAD` instead of `git diff --cached` to gather the full scope of changes across all commits in the range.

**If `FROM_COMMIT` is NOT set:** Behavior is unchanged — the skill analyzes staged diffs.

The QA Summary section should incorporate findings from Phase 4.

---

### Phase 9 — Stage + Commit

**Step 1 — Final stage:**

```bash
git add -A
```

**Step 2 — Generate commit message:**

Format:

```
release: v<VERSION> — <1-line summary>

<Bulleted list of key changes>

QA: <N> use cases tested, <N> findings resolved
Tests: All passing
Lint: Clean
```

**Step 3 — Commit:**

```bash
git commit -m "<message>"
```

**Step 4 — Report to user:**

```
✅ Release v<VERSION> prepared and committed.

Changes:
- <key change 1>
- <key change 2>

Release notes: docs/release-notes/v<VERSION>.md
Commit: <short SHA>

Next steps:
- Review the commit: git show HEAD
- Push when ready: git push
- Tag for release: git tag v<VERSION> && git push --tags
```

---

## 3. Failure Protocol

If any phase cannot be completed after its iteration cap:

1. **Stop the pipeline** — do not proceed to later phases
2. **Stage and commit what's clean** — if Phase 3+ passed, those changes are safe to commit as a WIP
3. **Report clearly** — list what passed, what failed, and what the user needs to do
4. **Never silently skip failures**

---

## 4. Red Flags — STOP

- Committing with failing tests
- Committing with unresolved P0/P1 QA findings
- Skipping the QA phase "to save time"
- Writing release notes before QA completes
- Proceeding when services are not running
- **Using `git stash` or `git checkout -- .` while the dev server is running** (see §5)

**All of these mean: STOP. Do not commit. Resolve first.**

---

## 5. CRITICAL — Never `git stash` While Dev Server Is Running

> [!CAUTION]
> **`git stash` while `pnpm wails:dev` or `pnpm dev` is running WILL WIPE THE USER'S DATABASE.**
>
> The Wails/Go dev server uses hot-reload. When `git stash` temporarily reverts source files, the Go server restarts and re-executes all migrations from `001_canonical_schema.sql`, which drops and recreates all tables as empty. By the time `git stash pop` restores the code, the database has already been destroyed. This is **unrecoverable** without Time Machine or manual backups.

**Forbidden commands while any dev server is running:**

```
git stash
git stash pop
git checkout -- .
git reset --hard
git clean -fd
```

**Safe alternatives when you need to compare against baseline (e.g., checking if lint errors are pre-existing):**

```bash
# Instead of git stash, use git show to read the old file:
git show HEAD:path/to/file.tsx | npx eslint --stdin --stdin-filename file.tsx

# Or diff against HEAD to see what changed:
git diff HEAD -- path/to/file.tsx

# Or check the lint output against the commit range:
git diff --name-only $FROM_COMMIT..HEAD -- '*.tsx' '*.ts' | head -20
```

**This rule exists because of a real incident on 2026-05-13 that wiped the user's production database during a prepare-release run.**

---

## 6. Phase Dependencies

| Phase      | Requires                  | Produces               |
| :--------- | :------------------------ | :--------------------- |
| 1. Stage   | Working directory changes | Staged files           |
| 2. Specs   | Staged files              | `uc-*.md` specs        |
| 3. Tests   | Codebase                  | Green test suite       |
| 4. QA      | tzro daemon + specs       | QA report, bug fixes   |
| 5. Lint    | Codebase                  | Clean formatting       |
| 6. Arch    | Codebase + diff           | Updated ARCHITECTURE.md|
| 7. README  | Codebase + diff           | Updated README.md      |
| 8. Notes   | QA report + diff          | Release notes file     |
| 9. Commit  | All above green           | Git commit             |
