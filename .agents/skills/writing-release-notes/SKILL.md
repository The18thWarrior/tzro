---
name: writing-release-notes
description: Use when generating changelog or release notes for a version, summarizing staged git changes for users, or when the user says "write release notes", "changelog", or "what changed in this release"
---

# Writing Release Notes

Generate user-facing release notes from staged git diffs, QA reports, and version metadata. Output goes to `docs/release-notes/v<VERSION>.md`.

---

## 1. Shell Preamble

**Prepend to EVERY terminal command:**

```bash
source ~/.nvm/nvm.sh && nvm use 22 > /dev/null && export PATH="/usr/local/go/bin:$PATH"
```

---

## 2. Execution Steps

### Step 1 — Determine Version

Read the current version from the root `package.json` `"version"` field:

```bash
node -e "console.log(require('./package.json').version)"
```

Decide the next version using semver:

| Change Type                     | Bump  | Example       |
| :------------------------------ | :---- | :------------ |
| Bug fixes only                  | patch | 0.2.0 → 0.2.1 |
| New user-facing features        | minor | 0.2.0 → 0.3.0 |
| Breaking changes / architecture | major | 0.2.0 → 1.0.0 |

**If unsure:** Ask the user before proceeding. Never guess the bump level.

### Step 2 — Gather Change Context

**If `FROM_COMMIT` is provided** (passed from `prepare-release --from` or user instruction):

Use the commit range to gather the full scope of changes:

```bash
git diff --stat $FROM_COMMIT..HEAD
git diff --name-only $FROM_COMMIT..HEAD
git log --oneline --no-merges $FROM_COMMIT..HEAD
```

Read actual diffs for user-visible changes using `git diff $FROM_COMMIT..HEAD -- <path>` for specific files of interest.

**If `FROM_COMMIT` is NOT provided** (standalone invocation):

Use staged files:

```bash
git diff --cached --stat
git diff --cached --name-only
```

Read actual diffs for user-visible changes.

---

**In both cases:** Group changes into: **New features**, **Bug fixes**, **Improvements**, **Technical** (only if user-relevant).

**Skip:** test files, `.agents/`, pure docs, CI/CD config.

### Step 3 — Check for QA Report

If this skill is invoked as part of `prepare-release`, a Proactive QA report will exist from Phase 4. Incorporate its summary:

- Number of use cases tested
- P0/P1 findings resolved
- P2/P3 findings deferred

If no QA report exists (standalone invocation), omit the QA Summary section entirely. **Do not fabricate QA data.**

### Step 4 — Write the Release Notes

File: `docs/release-notes/v<VERSION>.md`

```markdown
# X v<VERSION> Release Notes

**Release Date**: <YYYY-MM-DD>

---

## ✨ New Features

- **<Feature Name>** — <1-2 sentence user-facing description>

## 🐛 Bug Fixes

- **<Fix Summary>** — <What was broken and how it's fixed>

## 🔧 Improvements

- **<Improvement>** — <What changed and why it matters to users>

## 📋 QA Summary

- **Use Cases Tested**: <N>
- **Findings Resolved**: <N P0/P1 fixed>
- **Remaining**: <N P2/P3 deferred>

---

## Technical Details

<Brief summary of internal changes relevant to developers, if any>
```

**Omit any section that has zero entries.** Don't leave empty sections.

### Step 5 — Stage the File

```bash
git add docs/release-notes/
```

---

## 3. Writing Rules

- Write for the **end user** — no file paths, no function names, no component names
- Focus on **capability**: what the user can now **do** that they couldn't before
- Group related changes: 5 CSS tweaks → 1 bullet: "Improved light mode readability"
- Omit invisible changes (refactors, tests, infra) unless they affect UX

**Good vs. Bad:**

```markdown
# ❌ Fixed `useReasoningStream` hook to clear stale planning messages

# ✅ **Task Progress** — Steps now update in real-time without stale indicators

# ❌ Added `packages/renderer/components/db-explorer/data-table.tsx`

# ✅ **Database Explorer** — Browse and query local databases from the app
```

---

## 4. Common Mistakes

- **Listing every file** — Group by feature instead
- **Copy-pasting commits** — Rewrite in user language
- **Including test/CI changes** — Omit unless user-visible
- **Empty sections in template** — Remove if zero entries
- **Fabricating QA data** — Only include real QA results
- **Wrong version bump** — Ask user if ambiguous

---

## 5. Red Flags — STOP

- Writing release notes before changes are staged → Stage first
- Including notes about changes you haven't read the diff for → Read the diff
- Guessing what changed from file names alone → Read the actual code
- Fabricating QA metrics → Only report real data
- Using developer terminology in user-facing bullets → Rewrite

**All of these mean: STOP. Go back and do it properly.**
