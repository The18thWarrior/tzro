# Use Case: Strict-Local Privacy Mode

**Actor**: Developer configuring tzro to operate entirely offline with no cloud API calls.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`) / Config file
**Backend**: Internal routing engine — no external HTTP calls
**Priority**: P0

---

## Intent

A user with strict data privacy requirements wants to ensure that no task data, prompts, or model inference requests are ever sent to cloud APIs (Gemini, OpenAI, etc.). Setting `privacyLevel` to `strict-local` should hard-block all cloud fallback paths, even when the local model fails or confidence is low.

## Preconditions

- App is running with `privacyLevel: "strict-local"` in config
- A local model is downloaded and active
- Cloud API key may or may not be configured (should not matter)

## Success Criteria

- [ ] Setting `privacyLevel` to `strict-local` in config disables all cloud API calls
- [ ] Cloud-only mode (`modelMode: "cloud"`) returns a clear error under strict-local
- [ ] Cooperative mode does not escalate to cloud when local inference fails
- [ ] Confidence-based cloud fallback (`forceCloudFallback`) is suppressed
- [ ] `IsForceCloud()` always returns false under strict-local
- [ ] Error messages clearly state that cloud fallback is disabled due to privacy level
- [ ] Tasks complete using only the local model, even if slower or lower quality

## Edge Cases to Probe

- Configure both `strict-local` privacy AND a cloud API key — cloud should still be blocked
- Local model fails completely — user should get a local failure error, not a cloud escalation
- Switch from `strict-local` to `cooperative` mid-session — cloud should become available
- Cooperative mode with no cloud key — should behave the same as strict-local

## Anti-Patterns to Watch For

- [ ] Any HTTP request to cloud model providers when strict-local is active
- [ ] Silent fallback to cloud without error
- [ ] Vague error messages that don't mention the privacy level restriction
- [ ] Config change to strict-local not taking effect until restart
- [ ] Cloud-related code paths executing even partially (constructing requests, etc.)
