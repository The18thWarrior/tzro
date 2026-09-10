# Use Case: Agent Session Continuity and Handoff

**Actor**: AI coding agent pausing work or handing off an objective to a subsequent agent session
**Route**: CLI — `tzro session save / load / status`
**Backend**: Session continuity engine (`pkg/session/session.go`, `git.go`) and Schema v2 store
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

## Edge Cases to Probe

- Saving session with uncommitted working directory changes
- Loading a session manifest from a different git branch
- Restoring a session where files referenced in decisions have been deleted
- Saving a session with empty constraints or multi-line objectives
- Inspecting session status in a detached HEAD state

## Anti-Patterns to Watch For

- [ ] Session manifest blindly trusts cached evidence when git tree state has deviated
- [ ] Session files contain unbounded chat logs instead of distilled objectives, decisions, and checks
- [ ] Git commit SHA is missing or recorded as dirty when tree is clean
- [ ] Manifest restore overwrites local uncommitted changes without warning
