package executor

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ContentIssue represents a flagged issue in synthesis content (FM3).
type ContentIssue struct {
	Type    string `json:"type"`    // "dead_url" | "fabricated_quote" | "unknown_source"
	Content string `json:"content"` // The flagged URL or quote
	Reason  string `json:"reason"`  // Human-readable explanation
}

// urlPattern extracts HTTP(S) URLs from text.
var urlPattern = regexp.MustCompile(`https?://[^\s)>\]"]+`)

// ValidateURLs runs concurrent HTTP HEAD requests on extracted URLs.
// Returns dead URLs (4xx/5xx status or connection failure).
// Respects context cancellation and limits concurrent requests.
func ValidateURLs(ctx context.Context, urls []string) []ContentIssue {
	if len(urls) == 0 {
		return nil
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	var mu sync.Mutex
	var issues []ContentIssue
	var wg sync.WaitGroup

	// Limit concurrency to 10 requests
	sem := make(chan struct{}, 10)

	for _, u := range urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
			if err != nil {
				mu.Lock()
				issues = append(issues, ContentIssue{
					Type:    "dead_url",
					Content: url,
					Reason:  fmt.Sprintf("invalid URL: %v", err),
				})
				mu.Unlock()
				return
			}

			resp, err := client.Do(req)
			if err != nil {
				mu.Lock()
				issues = append(issues, ContentIssue{
					Type:    "dead_url",
					Content: url,
					Reason:  fmt.Sprintf("connection failed: %v", err),
				})
				mu.Unlock()
				return
			}
			resp.Body.Close()

			if resp.StatusCode >= 400 {
				mu.Lock()
				issues = append(issues, ContentIssue{
					Type:    "dead_url",
					Content: url,
					Reason:  fmt.Sprintf("HTTP %d", resp.StatusCode),
				})
				mu.Unlock()
			}
		}(u)
	}

	wg.Wait()
	return issues
}

// ValidateQuotes checks if quoted claims appear verbatim in source context.
// Returns fabricated quotes that do NOT appear in the source material.
func ValidateQuotes(quotes []string, sourceContext string) []ContentIssue {
	if len(quotes) == 0 || sourceContext == "" {
		return nil
	}

	var issues []ContentIssue
	lowerSource := strings.ToLower(sourceContext)

	for _, quote := range quotes {
		trimmed := strings.TrimSpace(quote)
		if trimmed == "" || len(trimmed) < 10 {
			continue // Skip very short quotes
		}
		if !strings.Contains(lowerSource, strings.ToLower(trimmed)) {
			issues = append(issues, ContentIssue{
				Type:    "fabricated_quote",
				Content: trimmed,
				Reason:  "quote not found in source context",
			})
		}
	}

	return issues
}

// ExtractURLs extracts all HTTP(S) URLs from text.
func ExtractURLs(text string) []string {
	return urlPattern.FindAllString(text, -1)
}

// quotePattern extracts text between quotation marks that looks like a quote
// (at least 10 chars, likely a sentence fragment).
var quotePattern = regexp.MustCompile(`"([^"]{10,})"`)

// ExtractQuotes extracts quoted text from synthesis output.
func ExtractQuotes(text string) []string {
	matches := quotePattern.FindAllStringSubmatch(text, -1)
	var quotes []string
	for _, m := range matches {
		if len(m) > 1 {
			quotes = append(quotes, m[1])
		}
	}
	return quotes
}

// citationPattern extracts bracketed source citation indices like [1], [2], [10].
var citationPattern = regexp.MustCompile(`\[(\d+)\]`)

// ValidateCitationAttribution verifies that inline citation markers [N]
// in research synthesis match valid entries in the Citation Preamble (ADR-0082).
func ValidateCitationAttribution(text string, numSources int) []ContentIssue {
	if numSources <= 0 || len(text) < 100 {
		return nil
	}

	matches := citationPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 && len(text) > 300 {
		return []ContentIssue{
			{
				Type:    "missing_citations",
				Content: "",
				Reason:  "synthesis text contains no inline numbered citations [N]",
			},
		}
	}

	var issues []ContentIssue
	seenInvalid := make(map[int]bool)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		var idx int
		fmt.Sscanf(m[1], "%d", &idx)
		if (idx < 1 || idx > numSources) && !seenInvalid[idx] {
			seenInvalid[idx] = true
			issues = append(issues, ContentIssue{
				Type:    "invalid_citation",
				Content: fmt.Sprintf("[%d]", idx),
				Reason:  fmt.Sprintf("citation [%d] exceeds available sources count (%d)", idx, numSources),
			})
		}
	}

	return issues
}

// ValidateContent checks URLs and quoted claims against source material.
// Runs URL liveness checks and quote verification in parallel.
func ValidateContent(ctx context.Context, synthesis string, sourceContext string) []ContentIssue {
	var allIssues []ContentIssue

	// Extract and validate URLs
	urls := ExtractURLs(synthesis)
	urlIssues := ValidateURLs(ctx, urls)
	allIssues = append(allIssues, urlIssues...)

	// Extract and validate quotes
	quotes := ExtractQuotes(synthesis)
	quoteIssues := ValidateQuotes(quotes, sourceContext)
	allIssues = append(allIssues, quoteIssues...)

	return allIssues
}
