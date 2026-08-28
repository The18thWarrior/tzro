# Use Case: KV-Cache Prefix Lock Guard

**Actor**: Developer using AI coding agents through the Tzro proxy
**Route**: Proxy — `http://127.0.0.1:7878` (transparent, no user action needed)
**Backend**: Prefix normalizer in the reverse proxy layer
**Priority**: P0

---

## Intent

A developer using multiple AI coding agent turns wants to maximize KV-cache hit rates at the LLM provider. The proxy normalizes prompt prefixes (system prompts, tool schemas, conversation preambles) to ensure byte-for-byte reproducibility across turns, preventing cache invalidation from reordered schemas, whitespace drift, or tool definition changes. This locks the cache prefix and achieves 70–99% cache read hit rates, reducing costs by up to 12.5× compared to cache misses.

## Preconditions

- Tzro proxy is running via `tzro start`
- Agent traffic is routed through `http://127.0.0.1:7878`
- Upstream provider supports prompt caching (Anthropic, OpenAI)

## Success Criteria

- [ ] System prompt prefix is byte-stable across consecutive turns
- [ ] Tool schemas are normalized (sorted, whitespace-consistent) before forwarding
- [ ] Cache read hit rates are 70–99% under steady-state multi-turn conversations
- [ ] KV-cache metrics are reported via `tzro status` or `/metrics` endpoint
- [ ] Prefix normalization does not alter the semantic meaning of prompts
- [ ] Both Anthropic and OpenAI cache headers are correctly passed through
- [ ] Performance overhead of normalization is negligible (<5ms per request)

## Edge Cases to Probe

- First request in a new session (cold start) — should write cache, subsequent reads should hit
- Agent adds a new tool mid-conversation — prefix should remain stable for unchanged portions
- Very large system prompt (>50k tokens) — normalization should still be fast
- Upstream returns cache-miss headers — proxy should log for diagnostics

## Anti-Patterns to Watch For

- [ ] Proxy reorders or modifies user message content (only system/tool prefixes should be normalized)
- [ ] Cache normalization introduces invalid JSON
- [ ] Proxy strips provider-specific cache control headers
- [ ] Normalization causes semantic changes to tool parameter descriptions
- [ ] Metrics report inflated hit rates that don't match actual provider cache behavior
