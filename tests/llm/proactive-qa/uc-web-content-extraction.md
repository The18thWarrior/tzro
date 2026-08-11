# Use Case: Web Content and Document Extraction

**Actor**: Developer running research tasks through the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer runs a research task that needs to follow URLs from search results and extract readable content from web pages and documents. The web browse tool fetches HTTP URLs, strips HTML to clean text, and returns the page content for the model to synthesize. The document extractor handles Office formats (DOCX, XLSX, PPTX) by extracting text content. Together, these tools give the research pipeline the ability to go beyond search snippets and read full source material.

## Preconditions

- The `tzro` daemon is running with web_search and web_browse tools available.
- Network access is available for HTTP fetches.
- For Office format extraction, the target files must be accessible on the local filesystem.

## Success Criteria

- [ ] The `web_browse` tool accepts a URL parameter and returns the page content as cleaned plain text.
- [ ] HTML-to-text conversion strips script, style, and noscript blocks entirely before removing other tags.
- [ ] Excessive whitespace and newlines are collapsed to readable paragraph formatting.
- [ ] HTTP redirects are followed up to a maximum of 5 hops.
- [ ] HTTP fetch timeout is enforced (15 seconds) to prevent hanging on unresponsive servers.
- [ ] The research compiler automatically provisions `web_browse` alongside `web_search` for research-type tasks.
- [ ] Office document extraction handles DOCX, XLSX, and PPTX formats.
- [ ] Web image filtering removes non-informative images from content extraction results.
- [ ] Extracted content is truncated to a reasonable size to avoid overwhelming the model's context window.

## Edge Cases to Probe

- Fetching a URL that returns a 403 Forbidden — verify clean error message, not a crash.
- Fetching a URL that redirects 6 times — verify the redirect limit is enforced with a clear error.
- Fetching a page with only JavaScript-rendered content (SPA) — verify the tool returns whatever static HTML is available.
- Fetching a very large page (>1MB of HTML) — verify content truncation prevents context overflow.
- Office file with password protection — verify clean error message.
- URL with non-UTF-8 encoding — verify content is handled without garbled characters.

## Anti-Patterns to Watch For

- [ ] The web_browse tool is called without the user's task requesting web research, wasting time on unnecessary fetches.
- [ ] Script tag content leaks into the extracted text, polluting the model's context with JavaScript code.
- [ ] HTTP errors (4xx, 5xx) are silently swallowed, returning empty strings instead of error messages.
- [ ] The tool follows redirects to file:// or other non-HTTP protocols.
- [ ] Office extraction crashes on malformed ZIP archives instead of returning a clean error.
- [ ] Raw HTML tags appear in the extracted text output.
