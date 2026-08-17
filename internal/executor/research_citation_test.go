package executor

import (
	"strings"
	"testing"
)

// --- Slice 6 RED (Run 32 fix): Research citation injection ---

// TestBuildCitationPreamble_IncludesAllURLs verifies that the citation preamble
// contains all scraped URLs as markdown links with a ## Verified Sources header.
func TestBuildCitationPreamble_IncludesAllURLs(t *testing.T) {
	sources := []ScrapedSource{
		{Title: "OpenAI Blog", URL: "https://openai.com/blog"},
		{Title: "Google AI Research", URL: "https://ai.google/research"},
	}

	preamble := buildCitationPreamble(sources)

	if !strings.Contains(preamble, "## Verified Sources") {
		t.Error("expected '## Verified Sources' section in preamble")
	}
	if !strings.Contains(preamble, "https://openai.com/blog") {
		t.Error("expected first URL in preamble")
	}
	if !strings.Contains(preamble, "https://ai.google/research") {
		t.Error("expected second URL in preamble")
	}
	if !strings.Contains(preamble, "OpenAI Blog") {
		t.Error("expected first source title in preamble")
	}
}

// TestBuildCitationPreamble_EmptyURLs_ReturnsEmpty verifies graceful handling
// when no sources were successfully scraped.
func TestBuildCitationPreamble_EmptyURLs_ReturnsEmpty(t *testing.T) {
	preamble := buildCitationPreamble(nil)
	if preamble != "" {
		t.Errorf("expected empty preamble for nil sources, got: %q", preamble)
	}
}

// TestBuildCitationPreamble_URLOnlySource_IncludesURL verifies that a source
// with no title falls back to the URL as the link text.
func TestBuildCitationPreamble_URLOnlySource_IncludesURL(t *testing.T) {
	sources := []ScrapedSource{
		{URL: "https://example.com"},
	}
	preamble := buildCitationPreamble(sources)
	if !strings.Contains(preamble, "https://example.com") {
		t.Error("expected URL in preamble even when title is empty")
	}
}
