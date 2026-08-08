package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── FM3: URL Validation tests ────────────────────────────────────────────────

func TestValidateURLs_DeadURL(t *testing.T) {
	// Start a test server that returns 404
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	issues := ValidateURLs(context.Background(), []string{srv.URL + "/nonexistent"})

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue for dead URL, got %d", len(issues))
	}
	if issues[0].Type != "dead_url" {
		t.Errorf("expected type 'dead_url', got %q", issues[0].Type)
	}
	if !strings.Contains(issues[0].Reason, "404") {
		t.Errorf("expected reason to contain '404', got %q", issues[0].Reason)
	}
}

func TestValidateURLs_LiveURL(t *testing.T) {
	// Start a test server that returns 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	issues := ValidateURLs(context.Background(), []string{srv.URL + "/ok"})

	if len(issues) != 0 {
		t.Errorf("expected 0 issues for live URL, got %d", len(issues))
	}
}

func TestValidateURLs_Timeout(t *testing.T) {
	// Use a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	issues := ValidateURLs(ctx, []string{"http://192.0.2.1:12345/timeout"})

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue for timed-out URL, got %d", len(issues))
	}
	if issues[0].Type != "dead_url" {
		t.Errorf("expected type 'dead_url', got %q", issues[0].Type)
	}
}

func TestValidateURLs_EmptyList(t *testing.T) {
	issues := ValidateURLs(context.Background(), nil)
	if issues != nil {
		t.Errorf("expected nil for empty URL list, got %v", issues)
	}
}

// ── FM3: Quote Validation tests ──────────────────────────────────────────────

func TestValidateQuotes_ExactMatch(t *testing.T) {
	source := "The system uses a DAG-based execution model with three primary components."
	quotes := []string{"DAG-based execution model with three primary components"}

	issues := ValidateQuotes(quotes, source)

	if len(issues) != 0 {
		t.Errorf("expected 0 issues for matching quote, got %d", len(issues))
	}
}

func TestValidateQuotes_Fabricated(t *testing.T) {
	source := "The system uses a DAG-based execution model."
	quotes := []string{"The system uses a microservice architecture with event sourcing"}

	issues := ValidateQuotes(quotes, source)

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue for fabricated quote, got %d", len(issues))
	}
	if issues[0].Type != "fabricated_quote" {
		t.Errorf("expected type 'fabricated_quote', got %q", issues[0].Type)
	}
}

func TestValidateQuotes_EmptySource(t *testing.T) {
	issues := ValidateQuotes([]string{"some quote here for testing"}, "")
	if issues != nil {
		t.Errorf("expected nil when source context is empty, got %v", issues)
	}
}

func TestValidateQuotes_ShortQuoteSkipped(t *testing.T) {
	// Quotes shorter than 10 chars should be skipped
	issues := ValidateQuotes([]string{"short"}, "this is source context")
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for short quote, got %d", len(issues))
	}
}

// ── FM3: URL/Quote Extraction tests ──────────────────────────────────────────

func TestExtractURLs(t *testing.T) {
	text := "Check https://example.com/api and http://docs.go.dev/pkg for details."
	urls := ExtractURLs(text)

	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs, got %d", len(urls))
	}
	if urls[0] != "https://example.com/api" {
		t.Errorf("expected first URL 'https://example.com/api', got %q", urls[0])
	}
}

func TestExtractQuotes(t *testing.T) {
	text := `The author noted "this is a significant finding in the research" and added "the results confirm the hypothesis is valid".`
	quotes := ExtractQuotes(text)

	if len(quotes) != 2 {
		t.Fatalf("expected 2 quotes, got %d", len(quotes))
	}
	if quotes[0] != "this is a significant finding in the research" {
		t.Errorf("expected first quote, got %q", quotes[0])
	}
}

// ── FM3: ValidateContent integration test ────────────────────────────────────

func TestValidateContent_Integration(t *testing.T) {
	// Live server (200)
	liveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer liveSrv.Close()

	// Dead server (404)
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer deadSrv.Close()

	source := "The system uses a DAG-based execution model."
	synthesis := `## Report

See ` + liveSrv.URL + `/ok for live docs and ` + deadSrv.URL + `/broken for broken docs.

The author stated "The system uses a DAG-based execution model" which is correct.
However, they also claimed "The system uses microservices with event sourcing" which is fabricated.`

	issues := ValidateContent(context.Background(), synthesis, source)

	// Expect: 1 dead URL + 1 fabricated quote = 2 issues
	deadURLCount := 0
	fabQuoteCount := 0
	for _, issue := range issues {
		switch issue.Type {
		case "dead_url":
			deadURLCount++
		case "fabricated_quote":
			fabQuoteCount++
		}
	}

	if deadURLCount != 1 {
		t.Errorf("expected 1 dead URL issue, got %d", deadURLCount)
	}
	if fabQuoteCount != 1 {
		t.Errorf("expected 1 fabricated quote issue, got %d", fabQuoteCount)
	}
}
