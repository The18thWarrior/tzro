# Use Case: Agent Session Continuity and Handoff

**Actor**: AI coding agent pausing work or handing off an objective to a subsequent agent session
**Route**: CLI — `tzro session save / load / status` and `tzro pause [description]` / `tzro resume [id] [--format tty|plain|json]`
**Backend**: Session continuity engine (`pkg/session/session.go`, `git.go`, `resume.go`, `dashboard.go`) and Schema v2 store
**Priority**: P1

---

## Intent

When an agent reaches context limits, completes a milestone, or hands off work to a peer agent, it needs to preserve its verified objective, architectural decisions, executed checks, and active constraints without forcing the next agent to re-read megabytes of chat transcripts or re-probe the entire codebase. Running `tzro session save` creates a portable, git-aware manifest that `tzro session load` and `tzro session status` can validate for freshness and continuity.

## Preconditions

- Tzro binary is installed and on PATH
- Project directory is inside a git repository
- Active task context with objectives, constraints, or decisions

## Success Criteria

- [ ] `tzro session save --objective "<goal>" --constraints "<rules>"` serializes state to Schema v2 manifest
- [ ] Manifest records current git commit SHA, tree dirty state, and modified file hashes
- [ ] Decisions made, verified checks passed, and pending next steps are captured in structured fields
- [ ] `tzro session status` detects whether the repository state has drifted since the session was saved
- [ ] If files were modified externally since session save, stale evidence markers are surfaced
- [ ] `tzro session load <manifest.json>` restores working context and constraints into the local store
- [ ] Output provides a compact summary (<400 tokens) ready for immediate agent consumption
- [ ] `tzro pause "<description>"` creates a session snapshot with the given pause description
- [ ] `tzro resume` without arguments loads the most recent paused session and displays a resumption dashboard
- [ ] `tzro resume <id>` loads a specific session by ID
- [ ] Resumption dashboard displays git branch divergence (ahead/behind upstream)
- [ ] Dashboard shows file drift detection — files modified since pause
- [ ] Dashboard shows stale evidence markers for executed checks that are no longer fresh
- [ ] `--format tty|plain|json` controls resumption dashboard output format

## Edge Cases to Probe

- Saving session with uncommitted working directory changes
- Loading a session manifest from a different git branch
- Restoring a session where files referenced in decisions have been deleted
- Saving a session with empty constraints or multi-line objectives
- Inspecting session status in a detached HEAD state
- Pausing with no description — should use a default timestamp-based label
- Resuming when the git branch has been rebased since pause
- Resuming a session from a different machine or workspace root

## Anti-Patterns to Watch For

- [ ] Session manifest blindly trusts cached evidence when git tree state has deviated
- [ ] Session files contain unbounded chat logs instead of distilled objectives, decisions, and checks
- [ ] Git commit SHA is missing or recorded as dirty when tree is clean
- [ ] Manifest restore overwrites local uncommitted changes without warning
- [ ] Resume dashboard shows stale evidence as fresh without checking git tree state
- [ ] Pause overwrites a previous session snapshot without confirmation
