package tools

// web_search.go — Multi-engine metasearch implementation for the web_search tool.
//
// Search Engine Execution Matrix:
//   Startpage   HTML Scraper   Tier 1 (Parallel)   No key required
//   Brave       JSON API       Tier 1 (Parallel)   BRAVE_SEARCH_KEY
//   Bing        JSON API       Tier 2 (Sequential)  BING_SEARCH_KEY
//   DuckDuckGo  HTML Scraper   Tier 3 (Last Resort) No key required
//
// Execution logic:
//   1. Fire Startpage + Brave in parallel. First success wins.
//   2. If Tier 1 fails, try Bing (if key is configured).
//   3. If all else fails, scrape DuckDuckGo HTML endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// SearchResult represents a single search result from any engine.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ── User-Agent Rotation ──────────────────────────────────────────────────────

var searchUserAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Safari/605.1.15",
}

func randomUserAgent() string {
	return searchUserAgents[rand.Intn(len(searchUserAgents))]
}

// ── Compiled Regex Patterns ──────────────────────────────────────────────────

var (
	// Startpage — multiple patterns for resilience against markup changes
	startpageLinkPatterns = []*regexp.Regexp{
		// Design-doc pattern: both result-title and result-link classes, class before href
		regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result-title[^"]*result-link[^"]*"[^>]*href="(https?://[^"]+)"[^>]*>(.*?)</a>`),
		// Newer Startpage markup: w-gl__result-title class
		regexp.MustCompile(`(?s)<a[^>]*class="[^"]*w-gl__result-title[^"]*"[^>]*href="(https?://[^"]+)"[^>]*>(.*?)</a>`),
		// Reversed attribute order: href before class
		regexp.MustCompile(`(?s)<a[^>]*href="(https?://[^"]+)"[^>]*class="[^"]*result-link[^"]*"[^>]*>(.*?)</a>`),
	}
	startpageSnippetPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?s)<p[^>]*class="[^"]*search-item__SearchResult-description[^"]*"[^>]*>(.*?)</p>`),
		regexp.MustCompile(`(?s)<p[^>]*class="[^"]*w-gl__description[^"]*"[^>]*>(.*?)</p>`),
	}
	cssClassNoiseRe = regexp.MustCompile(`\.css-[a-z0-9]+\{[^}]*\}`)

	// DuckDuckGo
	ddgResultRe  = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)

	// Shared
	htmlTagRe = regexp.MustCompile(`<[^>]*>`)
)

// ── Per-tier timeout ─────────────────────────────────────────────────────────

// searchTierTimeout controls how long each tier gets before giving up.
// Exposed at package level so tests can shorten it.
var searchTierTimeout = 8 * time.Second

// ── HTTP Helper ──────────────────────────────────────────────────────────────

var searchHTTPClient = &http.Client{
	Timeout: 12 * time.Second,
}

func doSearchRequest(ctx context.Context, req *http.Request) ([]byte, error) {
	resp, err := searchHTTPClient.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

// ── Startpage Scraper (Tier 1) ───────────────────────────────────────────────

func searchStartpage(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	searchURL := "https://www.startpage.com/do/search?" + url.Values{"q": {query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", randomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	body, err := doSearchRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("startpage: %w", err)
	}

	rawHTML := string(body)
	// Clean Startpage's injected randomized CSS class noise
	rawHTML = cssClassNoiseRe.ReplaceAllString(rawHTML, "")

	// Try each link pattern until one produces results
	var linkMatches [][]string
	for _, re := range startpageLinkPatterns {
		linkMatches = re.FindAllStringSubmatch(rawHTML, limit*3)
		if len(linkMatches) > 0 {
			break
		}
	}

	// Try each snippet pattern
	var snippetMatches [][]string
	for _, re := range startpageSnippetPatterns {
		snippetMatches = re.FindAllStringSubmatch(rawHTML, limit*3)
		if len(snippetMatches) > 0 {
			break
		}
	}

	var results []SearchResult
	for i, m := range linkMatches {
		if len(results) >= limit {
			break
		}
		resultURL := m[1]
		title := cleanHTMLText(m[2])
		snippet := ""
		if i < len(snippetMatches) && len(snippetMatches[i]) > 1 {
			snippet = cleanHTMLText(snippetMatches[i][1])
		}
		if title == "" || resultURL == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   title,
			URL:     resultURL,
			Snippet: snippet,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("startpage: no results parsed from %d bytes", len(body))
	}
	return results, nil
}

// ── Brave Search API (Tier 1) ────────────────────────────────────────────────

func searchBrave(ctx context.Context, query string, limit int, apiKey string) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("brave: no API key configured")
	}

	params := url.Values{
		"q":     {query},
		"count": {fmt.Sprintf("%d", limit)},
	}
	searchURL := "https://api.search.brave.com/res/v1/web/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	body, err := doSearchRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("brave: %w", err)
	}

	var resp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("brave: parse error: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.Web.Results {
		if len(results) >= limit {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("brave: API returned 0 results")
	}
	return results, nil
}

// ── Bing Search API (Tier 2) ─────────────────────────────────────────────────

func searchBing(ctx context.Context, query string, limit int, apiKey string) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("bing: no API key configured")
	}

	params := url.Values{
		"q":     {query},
		"count": {fmt.Sprintf("%d", limit)},
	}
	searchURL := "https://api.bing.microsoft.com/v7.0/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", apiKey)

	body, err := doSearchRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("bing: %w", err)
	}

	var resp struct {
		WebPages struct {
			Value []struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"value"`
		} `json:"webPages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("bing: parse error: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.WebPages.Value {
		if len(results) >= limit {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Name,
			URL:     r.URL,
			Snippet: r.Snippet,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("bing: API returned 0 results")
	}
	return results, nil
}

// ── DuckDuckGo Scraper (Tier 3 — Last Resort) ───────────────────────────────

func searchDDG(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	searchURL := "https://html.duckduckgo.com/html/?" + url.Values{"q": {query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", randomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	body, err := doSearchRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ddg: %w", err)
	}

	rawHTML := string(body)
	linkMatches := ddgResultRe.FindAllStringSubmatch(rawHTML, limit*3)
	snippetMatches := ddgSnippetRe.FindAllStringSubmatch(rawHTML, limit*3)

	var results []SearchResult
	for i, m := range linkMatches {
		if len(results) >= limit {
			break
		}
		rawURL := m[1]

		// Strip DuckDuckGo's tracking redirect wrapper by extracting the
		// real destination from the "uddg" URL query parameter.
		if parsed, parseErr := url.Parse(rawURL); parseErr == nil {
			if uddg := parsed.Query().Get("uddg"); uddg != "" {
				rawURL = uddg
			}
		}

		title := cleanHTMLText(m[2])
		snippet := ""
		if i < len(snippetMatches) && len(snippetMatches[i]) > 1 {
			snippet = cleanHTMLText(snippetMatches[i][1])
		}
		if title == "" || rawURL == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   title,
			URL:     rawURL,
			Snippet: snippet,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("ddg: no results parsed from %d bytes", len(body))
	}
	return results, nil
}

// ── Metasearch Orchestrator ──────────────────────────────────────────────────

// WebSearchMetasearch executes a multi-engine search with parallel and
// sequential fallback tiers. Always returns a non-nil slice (may be empty)
// and the source engine name.
func WebSearchMetasearch(ctx context.Context, query string, limit int) ([]SearchResult, string) {
	if limit <= 0 {
		limit = 5
	}

	braveKey := os.Getenv("BRAVE_SEARCH_KEY")
	bingKey := os.Getenv("BING_SEARCH_KEY")

	// ── Tier 1: Parallel (Startpage + Brave) ────────────────────────
	type tierResult struct {
		results []SearchResult
		source  string
		err     error
	}

	tier1Ctx, tier1Cancel := context.WithTimeout(ctx, searchTierTimeout)
	defer tier1Cancel()

	ch := make(chan tierResult, 2)

	// Startpage goroutine — always fires (no API key needed)
	go func() {
		results, err := searchStartpage(tier1Ctx, query, limit)
		ch <- tierResult{results, "startpage", err}
	}()

	// Brave goroutine — only if key is configured
	if braveKey != "" {
		go func() {
			results, err := searchBrave(tier1Ctx, query, limit, braveKey)
			ch <- tierResult{results, "brave", err}
		}()
	} else {
		go func() {
			ch <- tierResult{nil, "brave", fmt.Errorf("no API key")}
		}()
	}

	// Collect Tier 1 results — first success wins
	var tier1Errors []string
	for i := 0; i < 2; i++ {
		select {
		case r := <-ch:
			if r.err == nil && len(r.results) > 0 {
				log.Printf("[web_search] tier-1 success via %s (%d results)", r.source, len(r.results))
				return r.results, r.source
			}
			if r.err != nil {
				tier1Errors = append(tier1Errors, r.err.Error())
			}
		case <-tier1Ctx.Done():
			tier1Errors = append(tier1Errors, "tier-1 timeout")
			goto tier2
		}
	}

tier2:
	log.Printf("[web_search] tier-1 exhausted: %s", strings.Join(tier1Errors, "; "))

	// ── Tier 2: Bing (Sequential Fallback) ──────────────────────────
	if bingKey != "" {
		tier2Ctx, tier2Cancel := context.WithTimeout(ctx, searchTierTimeout)
		defer tier2Cancel()
		results, err := searchBing(tier2Ctx, query, limit, bingKey)
		if err == nil && len(results) > 0 {
			log.Printf("[web_search] tier-2 success via bing (%d results)", len(results))
			return results, "bing"
		}
		if err != nil {
			log.Printf("[web_search] tier-2 bing failed: %v", err)
		}
	}

	// ── Tier 3: DuckDuckGo (Last Resort) ────────────────────────────
	tier3Ctx, tier3Cancel := context.WithTimeout(ctx, searchTierTimeout)
	defer tier3Cancel()
	results, err := searchDDG(tier3Ctx, query, limit)
	if err == nil && len(results) > 0 {
		log.Printf("[web_search] tier-3 success via ddg (%d results)", len(results))
		return results, "ddg"
	}
	if err != nil {
		log.Printf("[web_search] tier-3 ddg failed: %v", err)
	}

	// All tiers exhausted
	log.Printf("[web_search] all tiers exhausted for query: %q", query)
	return []SearchResult{}, "none"
}

// ── HTML Cleanup ─────────────────────────────────────────────────────────────

// cleanHTMLText strips HTML tags and unescapes HTML entities from a raw
// HTML fragment, returning plain text suitable for search result display.
func cleanHTMLText(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	// Collapse runs of whitespace (common after tag stripping)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}
