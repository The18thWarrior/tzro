# Use Case: Hook Auto-Installer

**Actor**: Developer setting up Tzro for the first time with their AI coding agents
**Route**: CLI — `tzro init [--hooks <targets>] [--workspace]`
**Backend**: Local filesystem configuration detection and hook file generation
**Priority**: P1

---

## Intent

A developer wants to configure Tzro lifecycle hooks for their AI coding agents with a single command. The init command auto-detects which agent environments are active (Antigravity, Claude Code, Hermes, Copilot, Pi-Coder), generates the appropriate hook configuration files, and registers Tzro as the lifecycle hook handler.

## Preconditions

- Tzro binary is installed and on PATH
- At least one supported agent environment is configured on the system

## Success Criteria

- [ ] `tzro init` auto-detects active agent environments and configures hooks
- [ ] `tzro init --hooks claude,antigravity` configures only specified targets
- [ ] `tzro init --hooks all` forces configuration for all supported harnesses
- [ ] `tzro init --workspace` writes hooks to current directory instead of home
- [ ] Each configured harness reports success status with config file path
- [ ] No active environments detected shows a helpful message with manual options
- [ ] Generated hook configs point to the correct `tzro hook` commands
- [ ] Existing configurations are updated, not duplicated

## Edge Cases to Probe

- Run init when no agent environments are installed — should suggest `--hooks all`
- Run init twice — should update cleanly without duplicating entries
- Run init with `--workspace` in a directory without write permissions
- Specify an unknown hook target (e.g., `--hooks foobar`) — should error clearly

## Anti-Patterns to Watch For

- [ ] Init overwrites user's existing hook configurations without backup
- [ ] Init creates hook files with wrong permissions (not executable)
- [ ] Init hardcodes absolute paths to tzro binary instead of using PATH
- [ ] Silent success when no hooks were actually configured
