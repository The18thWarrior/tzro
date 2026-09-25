# Use Case: Shell Integration and Command Capture

**Actor**: Developer who wants terminal command history captured for agent context
**Route**: CLI — `tzro shell [init|record|status|clear]`
**Backend**: Shell hook installer (`pkg/session/shell.go`) and command event store (`pkg/store/command_events.go`)
**Priority**: P1

---

## Intent

A developer wants their terminal commands (builds, tests, git operations) automatically recorded so that AI agents can access recent command history as workspace context. Running `tzro shell init zsh` installs a lightweight preexec/precmd hook that captures commands into SQLite with sub-millisecond overhead, while automatically redacting sensitive credentials.

## Preconditions

- Tzro binary is installed and on PATH
- User is running zsh or bash as their interactive shell
- SQLite store is available for command event storage

## Success Criteria

- [ ] `tzro shell init zsh` outputs a shell integration script for zsh
- [ ] `tzro shell init bash` outputs a shell integration script for bash
- [ ] Captured commands are filtered through a strict allowlist of development-relevant commands
- [ ] Sensitive tokens (API keys, bearer tokens, passwords) are automatically redacted before storage
- [ ] Chained or piped commands are excluded to prevent capturing ambiguous compound operations
- [ ] `tzro shell status` reports whether shell integration is active and shows recent capture count
- [ ] `tzro shell clear` purges captured command history from the local store
- [ ] Command capture overhead is sub-millisecond and does not block shell execution
- [ ] Retention pruning limits stored commands by count and age

## Edge Cases to Probe

- Running `tzro shell init` without specifying a shell — should detect current shell or prompt
- Installing when another preexec hook (e.g., zsh-autosuggestions) is already active
- Capturing a command containing environment variables with secrets (should redact)
- Running `tzro shell clear` with no captured commands

## Anti-Patterns to Watch For

- [ ] Capturing all commands indiscriminately including `ls`, `cd`, `cat` that add noise
- [ ] Storing raw API keys or tokens in the command history database
- [ ] Shell hook installation breaking existing shell configuration or startup time
- [ ] Commands captured without exit codes, making success/failure assessment impossible
- [ ] No retention limit causing unbounded database growth
