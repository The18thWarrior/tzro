# Use Case: Git Hook Management

**Actor**: Developer who wants advisory pre-commit impact analysis on every commit
**Route**: CLI — `tzro hook [install|uninstall|status] pre-commit`
**Backend**: Git hook manager (`pkg/hooks/git_hook.go`)
**Priority**: P1

---

## Intent

A developer wants an advisory pre-commit hook that automatically shows the blast radius of staged changes before each commit, helping catch unintended side effects early. The hook is non-blocking — it displays impact information but never prevents the commit from proceeding.

## Preconditions

- Tzro binary is installed and on PATH
- Working directory is inside a git repository
- The `.git/hooks/` directory is writable

## Success Criteria

- [ ] `tzro hook install pre-commit` installs a pre-commit hook in the repository's `.git/hooks/`
- [ ] The hook runs `tzro impact --staged` and displays a formatted blast radius summary
- [ ] The hook is advisory-only — it never blocks or rejects a commit
- [ ] Existing pre-commit hooks are backed up to `.tzro.backup` and chained in execution order
- [ ] `tzro hook uninstall pre-commit` removes the hook and restores the original backup
- [ ] `tzro hook status` reports which hooks are installed and their health
- [ ] Hook works correctly in linked git worktrees and submodules
- [ ] Hook output uses TTY formatting when connected to a terminal

## Edge Cases to Probe

- Installing when a pre-commit hook already exists from another tool (husky, pre-commit framework)
- Running in a git repository with a non-standard hooks directory (`core.hooksPath`)
- Uninstalling when the backup file has been manually deleted
- Installing in a bare repository (should reject gracefully)

## Anti-Patterns to Watch For

- [ ] Hook blocks commits by exiting with non-zero status
- [ ] Original pre-commit hook is overwritten without backup
- [ ] Hook fails silently when tzro binary is not on PATH
- [ ] Hook hangs or takes >5 seconds on large staged changesets
- [ ] Uninstall leaves orphaned backup files or corrupted hook state
