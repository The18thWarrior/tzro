package tools

// web_browse.go — HTTP URL content fetcher for research benchmarks.
//
// Fetches a URL via HTTP GET, strips HTML tags, and returns the page content
// as cleaned text. This gives the model the ability to follow search result
// URLs and extract page content for research synthesis.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ── HTML-to-text patterns ────────────────────────────────────────────────────

var (
	// Strip script, style, and noscript blocks entirely (content and tags).
	// Go's RE2 engine doesn't support backreferences, so we use separate patterns.
	scriptRe   = regexp.MustCompile(`(?si)<script[^>]*>.*?</script>`)
	styleRe    = regexp.MustCompile(`(?si)<style[^>]*>.*?</style>`)
	noscriptRe = regexp.MustCompile(`(?si)<noscript[^>]*>.*?</noscript>`)
	// Strip all remaining HTML tags
	allHTMLTagRe = regexp.MustCompile(`<[^>]*>`)
	// Collapse runs of whitespace (preserving newlines as paragraph breaks)
	multiNewlineRe = regexp.MustCompile(`\n{3,}`)
	multiSpaceRe   = regexp.MustCompile(`[ \t]{2,}`)
)

// browseHTTPClient has a generous timeout for page fetches.
var browseHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects (max 5)")
		}
		return nil
	},
}

// htmlToText converts raw HTML to readable plain text by stripping scripts,
// styles, tags, and collapsing whitespace.
func htmlToText(raw string) string {
	// 1. Remove script/style/noscript blocks
	text := scriptRe.ReplaceAllString(raw, "")
	text = styleRe.ReplaceAllString(text, "")
	text = noscriptRe.ReplaceAllString(text, "")
	// 2. Convert <br>, <p>, <div>, <li>, <tr>, <h*> to newlines for structure
	text = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</tr>|</h[1-6]>`).ReplaceAllString(text, "\n")
	text = regexp.MustCompile(`(?i)<li[^>]*>`).ReplaceAllString(text, "\n• ")
	// 3. Strip all remaining HTML tags
	text = allHTMLTagRe.ReplaceAllString(text, "")
	// 4. Unescape HTML entities
	text = strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&#39;", "'",
		"&nbsp;", " ",
		"&#x27;", "'",
		"&#x2F;", "/",
	).Replace(text)
	// 5. Collapse whitespace
	text = multiSpaceRe.ReplaceAllString(text, " ")
	text = multiNewlineRe.ReplaceAllString(text, "\n\n")
	// 6. Trim lines and overall
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}

// NewWebBrowseTool instantiates the web_browse tool structure.
// Fetches a URL via HTTP GET and returns the page content as cleaned text.
func NewWebBrowseTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "web_browse",
		description: "Fetch a web page URL and return its text content. Use after web_search to read full page content from a search result URL.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"url":      map[string]interface{}{"type": "string"},
			"maxChars": map[string]interface{}{"type": "integer"},
		}, []string{"url"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				URL      string `json:"url"`
				MaxChars *int   `json:"maxChars"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			if in.URL == "" {
				return ToolError("url parameter is required"), nil
			}

			// Validate scheme
			if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
				return ToolError("only http:// and https:// URLs are supported",
					WithHint("Provide a full URL starting with http:// or https://"),
				), nil
			}

			maxChars := 10000
			if in.MaxChars != nil && *in.MaxChars > 0 {
				maxChars = *in.MaxChars
				if maxChars > 50000 {
					maxChars = 50000 // Hard cap
				}
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
			if err != nil {
				return ToolError(fmt.Sprintf("invalid URL: %v", err)), nil
			}
			req.Header.Set("User-Agent", randomUserAgent())
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")

			resp, err := browseHTTPClient.Do(req)
			if err != nil {
				return ToolError(fmt.Sprintf("fetch failed: %v", err),
					WithHint("The URL may be unreachable or timed out. Try a different URL from the search results."),
					WithRelatedTools("web_search"),
				), nil
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return ToolError(fmt.Sprintf("HTTP %d from %s", resp.StatusCode, in.URL),
					WithHint("Try a different URL. This one returned a non-200 status."),
					WithRelatedTools("web_search"),
				), nil
			}

			// Read body with size limit (2x maxChars to account for HTML overhead)
			limitBytes := int64(maxChars * 2)
			if limitBytes < 100000 {
				limitBytes = 100000
			}
			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, limitBytes))
			if err != nil {
				return ToolError(fmt.Sprintf("failed to read response body: %v", err)), nil
			}

			// Convert HTML to text
			text := htmlToText(string(bodyBytes))

			// Truncate to maxChars
			if len(text) > maxChars {
				text = text[:maxChars] + "\n\n[... content truncated at " + fmt.Sprintf("%d", maxChars) + " characters ...]"
			}

			return ToolSuccess(map[string]interface{}{
				"url":     in.URL,
				"content": text,
				"chars":   len(text),
			}), nil
		},
	}
}
