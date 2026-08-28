# Use Case: Token Shield Transparent Proxy

**Actor**: Developer running AI coding agents (Cursor, Claude Code, Antigravity, Aider) on localhost
**Route**: CLI — `tzro start --port 7878`
**Backend**: `http://127.0.0.1:7878` (loopback proxy)
**Priority**: P0

---

## Intent

A developer wants to reduce cloud API costs and avoid rate limits by routing all LLM traffic through the Tzro proxy. The proxy transparently intercepts requests, normalizes prompt prefixes for KV-cache stability, redacts secrets via DLP, and forwards the cleaned request to Anthropic or OpenAI upstream endpoints.

## Preconditions

- Tzro binary is installed and on PATH
- Network connectivity to upstream LLM providers (Anthropic, OpenAI)
- No other process listening on the configured port (default 7878)

## Success Criteria

- [ ] Running `tzro start` launches a proxy on `http://127.0.0.1:7878`
- [ ] The proxy accepts both Anthropic-style (`/v1/messages`) and OpenAI-style (`/v1/chat/completions`) requests
- [ ] Requests are forwarded to the correct upstream based on path and API key headers
- [ ] The proxy normalizes system prompt prefixes for KV-cache hit stability
- [ ] DLP scanning redacts secrets (API keys, tokens, passwords) before egress
- [ ] `tzro status` reports active metrics (request counts, bytes shielded, memory usage, uptime)
- [ ] Memory footprint stays under 50 MB RSS during steady-state operation
- [ ] The proxy handles concurrent requests without data races or panics
- [ ] Custom upstream URLs can be configured via `--upstream-anthropic` and `--upstream-openai` flags
- [ ] Custom port can be configured via `--port` flag

## Edge Cases to Probe

- Start the proxy when the port is already in use — should fail with a clear error
- Send a malformed request body — should return a proper HTTP error, not crash
- Send requests with no API key header — should forward as-is (proxy is transparent)
- Kill the proxy mid-request — should not corrupt the content-hash store

## Anti-Patterns to Watch For

- [ ] Proxy silently drops requests without logging
- [ ] Proxy modifies response bodies from the upstream provider
- [ ] Memory usage grows unboundedly over many requests (leak)
- [ ] Proxy panics on unexpected Content-Type or encoding
- [ ] DLP false positives redact legitimate code content
- [ ] Proxy hardcodes port 7878 and ignores `--port` flag
