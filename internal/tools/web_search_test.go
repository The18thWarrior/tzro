package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebSearch_202FastFailover(t *testing.T) {
	// Set short tier timeout for test speed
	origTimeout := searchTierTimeout
	searchTierTimeout = 500 * time.Millisecond
	defer func() { searchTierTimeout = origTimeout }()

	// Mock server returning HTTP 202 for rate-limited / challenged scraper requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "lite") || strings.Contains(r.URL.Path, "duckduckgo") {
			w.WriteHeader(http.StatusAccepted) // 202
			w.Write([]byte("<!-- Challenge required --><html><body>202 Accepted</body></html>"))
			return
		}
		// Return sample startpage/search results for other paths
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`
			<a class="result-title result-link" href="https://docs.example.com/item">Example Doc Title</a>
			<p class="search-item__SearchResult-description">Example snippet describing the documentation item.</p>
		`))
	}))
	defer server.Close()

	// Direct test on doSearchRequest: verify 202 is treated as an error indicating rate-limit
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/lite", nil)
	_, err := doSearchRequest(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error on 202 status code, got nil")
	}
	if !strings.Contains(err.Error(), "202") {
		t.Errorf("expected error message to mention 202, got: %v", err)
	}

	// Test successful fallback path
	reqSuccess, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/startpage", nil)
	body, err := doSearchRequest(context.Background(), reqSuccess)
	if err != nil {
		t.Fatalf("expected success on 200, got: %v", err)
	}
	if len(body) == 0 {
		t.Errorf("expected non-empty response body")
	}
}

func TestWebSearch_CleanHTMLText(t *testing.T) {
	raw := `<p>This is a <b>bold</b> &amp; <i>italic</i> description &quot;with quotes&quot;.</p>`
	cleaned := cleanHTMLText(raw)
	expected := `This is a bold & italic description "with quotes".`
	if cleaned != expected {
		t.Errorf("cleanHTMLText mismatch.\nExpected: %q\nGot:      %q", expected, cleaned)
	}
}
