---
name: write-use-case-spec
description: Use when new features are staged in git and need corresponding proactive QA use case specs. Reads staged files, identifies affected features, routes, and components, then generates intent-based use case specs in tests/llm/proactive-qa/. Trigger when user says "write use case specs", "generate use cases for staged changes", or "write specs for my changes".
---

# Write Use Case Specs from Staged Changes

Generate intent-based use case specifications for the `proactive-qa` system by analyzing currently staged git changes.

---

## 1. When to Use

- New feature code is staged (`git add`) and needs QA coverage
- User says "write use case specs", "generate specs for staged", or "write specs for my changes"
- After a PR or feature branch is ready and needs proactive QA specs before merge

## 2. Output Location

```
tests/llm/proactive-qa/uc-<feature-slug>.md
```

---

## 3. Execution Protocol

### Phase 1 — Discover Staged Changes

Run `git diff --cached --name-only` to get the list of staged files.

If no files are staged, report to the user and ask whether to:

- Analyze `git diff --name-only HEAD~1` (last commit) instead
- Analyze specific files the user points to

### Phase 2 — Classify Changes

For each staged file, classify it into one of these layers:

| Layer           | Patterns                                                    | What It Tells You                        |
| :-------------- | :---------------------------------------------------------- | :--------------------------------------- |
| **Route**       | `app/(feature)/*/page.tsx`, `app/settings/*/page.tsx`       | A user-facing page was added or changed  |
| **Component**   | `components/**/*.tsx`                                       | UI behavior was added or changed         |
| **Backend API** | `services/go-api/**/*.go`                                   | New endpoint, handler, or business logic |
| **Tool**        | `packages/tools/**/*.ts`, `packages/chat-core/src/tools/**` | New AI tool capability                   |
| **Hook/Lib**    | `packages/renderer/lib/**`, `hooks/**`                      | Supporting logic for a feature           |
| **Type/Schema** | `packages/types/**`, `schemas/**`                           | Data shape changed                       |
| **Config**      | `*.json`, `*.yaml`, `infra/**`                              | Infrastructure or configuration          |

### Phase 3 — Identify Feature Clusters

Group the classified files into **feature clusters** — sets of files that together represent one user-facing capability. Use these heuristics:

1. **Same route prefix** → same feature (e.g., all files under `app/(feature)/agents/` → "Agents" feature)
2. **Component used by a route** → belongs to that route's cluster
3. **Backend handler + frontend page** → same feature (e.g., `proposals/` handler + `proposals/page.tsx`)
4. **Tool + tool-renderer** → same feature
5. **Standalone backend change with no frontend** → may not need a use case (API-only), but flag for review

If a cluster has **no user-facing route**, it likely doesn't need a use case spec. Flag it as "backend-only" and skip unless the user explicitly requests it.

### Phase 4 — Read Existing Specs

Before writing, check what already exists:

```bash
ls tests/llm/proactive-qa/uc-*.md
```

If a spec already covers the feature cluster, **update it** rather than creating a duplicate. Read the existing spec to understand its current scope.

### Phase 5 — Read the Changed Code

For each feature cluster, read the actual staged diffs to understand:

1. **What route(s)** — the URL path users navigate to
2. **What the user can do** — actions, inputs, buttons, forms
3. **What API calls are made** — fetch calls, mutation hooks, backend endpoints
4. **What states exist** — loading, empty, error, success, in-progress
5. **What edge cases the code handles** — error boundaries, validation, fallbacks
6. **What the code does NOT handle** — missing error handling, no loading state, no validation

Use `git diff --cached -- <file>` to read the actual changes.

### Phase 6 — Generate Use Case Specs

For each feature cluster, write a `uc-<feature-slug>.md` file following this **exact template**:

```markdown
# Use Case: [Short Descriptive Name]

**Actor**: [Who uses this — be specific about their context]
**Route**: [Primary frontend route, e.g., /agents]
**Backend**: [API endpoint if applicable, e.g., http://localhost:36888/api/agents]
**Priority**: [P0 | P1 | P2 — see priority guide below]

---

## Intent

[2-3 sentences describing what the user is trying to accomplish. Write from the user's
perspective, not the developer's. Focus on the goal, not the implementation.]

## Preconditions

- [Required app state before this use case can be tested]
- [e.g., "App is running, provider configured"]
- [e.g., "At least one integration connected"]

## Success Criteria

- [ ] [Observable outcome the QA agent can verify visually or via interaction]
- [ ] [Each criterion must be independently verifiable]
- [ ] [Use the user's perspective — "User sees X" not "Component renders X"]
- [ ] [Include both positive outcomes and state transitions]
- [ ] [Aim for 6-12 criteria per spec]

## Edge Cases to Probe

- [Specific deviation from the happy path to try]
- [Empty input, very long input, special characters]
- [Rapid repeated actions, navigation away and back]
- [Concurrent operations, stale state]
- [Aim for 3-6 edge cases]

## Anti-Patterns to Watch For

- [ ] [Thing that should NEVER happen — checkboxes for the QA agent]
- [ ] [Raw JSON, stack traces, "undefined", blank screens]
- [ ] [Dead buttons, frozen inputs, missing feedback]
- [ ] [Feature-specific bad behaviors from code review]
- [ ] [Aim for 4-8 anti-patterns]
```

### Phase 7 — Validate & Report

After generating specs, produce a summary:

```
## Specs Generated

| File | Feature | Priority | New/Updated | Files Analyzed |
|:---|:---|:---|:---|:---|
| uc-agents.md | Agent Management | P1 | New | 4 files |
| uc-task-creation.md | Task Creation | P0 | Updated | 2 files |

## Skipped (No User-Facing Route)

| Files | Reason |
|:---|:---|
| services/go-api/core/cors.go | Backend infrastructure, no UI |
```

---

## 4. Priority Assignment Guide

| Priority | Assign When                                                                                                  |
| :------- | :----------------------------------------------------------------------------------------------------------- |
| **P0**   | Feature is part of the core user loop (chat, tasks, workflows, onboarding, navigation, providers)            |
| **P1**   | Feature is important but not the core loop (settings, integrations, heartbeat, knowledge base, session mgmt) |
| **P2**   | Feature is supplementary or polish (themes, research, markdown rendering, activity logs)                     |

When in doubt, assign P1.

---

## 5. Naming Convention

Filenames: `uc-<feature-slug>.md`

| Feature            | Slug                       |
| :----------------- | :------------------------- |
| Agent management   | `uc-agent-management.md`   |
| Proposal review    | `uc-proposal-review.md`    |
| Memory settings    | `uc-memory-settings.md`    |
| Direct write tools | `uc-direct-write-tools.md` |

Rules:

- Lowercase, hyphen-separated
- 2-4 words maximum
- Describe the **user action**, not the component name
- No abbreviations unless universally understood (e.g., `api`, `crm`)

---

## 6. Quality Checks

Before finishing, verify each generated spec against these criteria:

- [ ] **Intent is user-centric** — describes what the user wants, not what the code does
- [ ] **Success criteria are observable** — a browser agent can verify each one visually
- [ ] **Edge cases come from the code** — derived from actual code paths, not generic
- [ ] **Anti-patterns are specific** — mention the exact bad behavior, not vague "errors"
- [ ] **No implementation details leak** — no component names, hook names, or internal state in user-facing criteria
- [ ] **Route is correct** — matches the actual app routing
- [ ] **Backend URL uses gateway** — always `http://localhost:36888`, not direct service ports
- [ ] **No duplicate coverage** — doesn't overlap significantly with an existing spec

---

## 7. Common Mistakes

| Mistake                                                 | Fix                                               |
| :------------------------------------------------------ | :------------------------------------------------ |
| Writing developer-centric criteria ("useState updates") | Rewrite as user-observable ("the list refreshes") |
| Copying component names into the spec                   | Use user-visible labels and descriptions          |
| Generic edge cases ("test with bad input")              | Derive from actual validation logic in the code   |
| Missing the backend URL                                 | Always include if the feature makes API calls     |
| Creating a spec for a pure refactor                     | Skip — refactors don't add user-facing behavior   |
| Duplicating an existing spec                            | Update the existing one instead                   |

---

## 8. Nomenclature Reference

Use project nomenclature from `AGENTS.md`:

| Concept    | Correct Name | Wrong Name     |
| :--------- | :----------- | :------------- |
| Chat       | `chat`       | "Task" (v1)    |
| Task       | `task`       | "Swarm"        |
| Workflow   | `workflow`   | "Mission"      |
| Automation | `automation` | "DAG Workflow" |
