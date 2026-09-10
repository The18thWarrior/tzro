# Use Case: System and Gateway Diagnostics

**Actor**: Developer or AI coding agent encountering proxy timeouts, route issues, or environment misconfiguration
**Route**: CLI — `tzro doctor`
**Backend**: Diagnostic and synthetic health probe engine (`pkg/doctor/doctor.go`)
**Priority**: P1

---

## Intent

A developer or agent experiencing unexpected LLM gateway errors, upstream timeouts, hook interception problems, or database failures needs an instant, zero-cost diagnostic check to pinpoint the issue. Running `tzro doctor` executes synthetic health checks across the proxy daemon, upstream provider routing, SQLite FTS5 capability, and agent lifecycle hook registrations, outputting a clear green/amber/red status report.

## Preconditions

- Tzro binary is installed and on PATH
- Project directory contains `.tzro/` or `.agents/` configuration

## Success Criteria

- [ ] `tzro doctor` verifies whether the local proxy daemon is running on port 7878
- [ ] Synthetic probes test upstream provider endpoints (Anthropic, OpenAI, OpenRouter) for DNS and route reachability
- [ ] SQLite database is tested for WAL mode, write permissions, and FTS5 extension support
- [ ] Agent lifecycle hook configurations (`.agents/hooks.json`, Claude Code settings) are validated for syntax and executable targets
- [ ] Output displays a clear checklist format with green checkmarks and actionable remediation tips for failures
- [ ] Command completes in under 2 seconds without hanging on unreachable networks
- [ ] Non-zero exit code is returned if critical subsystems are degraded or offline

## Edge Cases to Probe

- Running `tzro doctor` completely offline with no internet access
- Running doctor when proxy daemon is stopped
- Corrupted `.tzro/config.json` or `.agents/hooks.json`
- SQLite database locked by another process
- Missing environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`)

## Anti-Patterns to Watch For

- [ ] Doctor hangs for 30+ seconds when an upstream endpoint is down
- [ ] Doctor reports all systems green when the proxy port is occupied by another unrelated service
- [ ] Error messages provide raw Go stack traces instead of actionable configuration remedies
- [ ] Doctor requires cloud tokens to run diagnostic health checks
