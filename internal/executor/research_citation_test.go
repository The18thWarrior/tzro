package executor

import (
	"context"
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

	preamble := buildCitationPreamble(sources, nil)

	if !strings.Contains(preamble, "## Verified Research Evidence & Numbered Sources") {
		t.Error("expected '## Verified Research Evidence & Numbered Sources' section in preamble")
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
	if !strings.Contains(preamble, "[1]") || !strings.Contains(preamble, "[2]") {
		t.Error("expected numbered bibliography indices [1] and [2] in preamble")
	}
}

// TestBuildCitationPreamble_EmptyURLs_ReturnsEmpty verifies graceful handling
// when no sources were successfully scraped.
func TestBuildCitationPreamble_EmptyURLs_ReturnsEmpty(t *testing.T) {
	preamble := buildCitationPreamble(nil, nil)
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
	preamble := buildCitationPreamble(sources, nil)
	if !strings.Contains(preamble, "https://example.com") {
		t.Error("expected URL in preamble even when title is empty")
	}
}

func TestExtractEvidenceCardFromPage(t *testing.T) {
	content := `# Go Release Notes

- Go 1.23.1 addresses CVE-2024-34156 in encoding/gob with a critical fix.
- Memory throughput improved by 15% on Apple Silicon.
- Supported architecture includes arm64 and amd64.
`
	card := extractEvidenceCardFromPage(context.Background(), "https://go.dev/doc/devel/release", content, "Investigate Go CVEs and performance")
	if card.Title != "Go Release Notes" {
		t.Errorf("expected title 'Go Release Notes', got %q", card.Title)
	}
	if len(card.KeyFacts) < 2 {
		t.Errorf("expected at least 2 key facts, got %d", len(card.KeyFacts))
	}
}

func TestExtractEvidenceCardFromPage_ComplexTableAndKeyValue(t *testing.T) {
	content := `<title>LLM Orchestration Frameworks Comparison</title>

## Benchmark Results
| Framework | Latency (ms) | Throughput (t/s) |
| --- | --- | --- |
| LangChain | 120ms | 45.2 t/s |
| LlamaIndex | 85ms | 62.1 t/s |

**SDK Support:** Python, TypeScript, Go
**Pricing Tier:** Open Source Apache-2.0
- LangChain v0.3 introduces improved multi-agent state machines.
- LlamaIndex supports over 160 vector store integrations as of 2025.
`
	card := extractEvidenceCardFromPage(context.Background(), "https://example.com/llm-comparison", content, "Compare LLM Orchestration Frameworks")
	if card.Title != "LLM Orchestration Frameworks Comparison" {
		t.Errorf("expected parsed title, got %q", card.Title)
	}
	if len(card.KeyFacts) < 3 {
		t.Errorf("expected at least 3 high-density facts, got %d: %v", len(card.KeyFacts), card.KeyFacts)
	}

	foundTable := false
	for _, f := range card.KeyFacts {
		if strings.Contains(f, "LangChain") && strings.Contains(f, "120ms") {
			foundTable = true
		}
	}
	if !foundTable {
		t.Errorf("expected table row in extracted facts, got: %v", card.KeyFacts)
	}
}

func TestExtractSecondaryQueriesFromOutput(t *testing.T) {
	goal := "Investigate Go standard library security vulnerabilities and CVEs in 2024"
	toolOutput := `Found results:
- https://pkg.go.dev/vuln/GO-2024-0001
- https://opencve.io/cve/golang
`
	queries := extractSecondaryQueriesFromOutput(goal, toolOutput)
	if len(queries) == 0 {
		t.Fatalf("expected non-empty secondary queries")
	}

	hasPkgGoDev := false
	for _, q := range queries {
		if strings.Contains(q, "pkg.go.dev") {
			hasPkgGoDev = true
		}
	}
	if !hasPkgGoDev {
		t.Errorf("expected query targeting pkg.go.dev for Go vulnerability goal, got: %v", queries)
	}
}

func TestExtractEvidenceCardFromPage_JSONWrapped(t *testing.T) {
	jsonPayload := `{"url":"https://example.com/cve-report","content":"# Vulnerability Report\n\n- CVE-2024-24790 affects net/netip and enables unbounded resource consumption.\n- Patched in Go 1.22.4 and Go 1.21.11.\n- CVSS score is 7.5 High.\n","chars":200}`
	card := extractEvidenceCardFromPage(context.Background(), "https://example.com/cve-report", jsonPayload, "Analyze Go standard library CVEs and patched versions")
	if card.Title != "Vulnerability Report" {
		t.Errorf("expected parsed title 'Vulnerability Report', got %q", card.Title)
	}
	if len(card.KeyFacts) < 2 {
		t.Fatalf("expected at least 2 key facts extracted from JSON payload, got %d", len(card.KeyFacts))
	}
}

func TestExtractEvidenceCardFromPage_ToolResultEnvelope(t *testing.T) {
	envelopeJSON := `{"success":true,"data":{"url":"https://pkg.go.dev/vuln/GO-2024-24790","content":"# Go Security Advisory GO-2024-24790\n\n- Package net/netip is affected by denial of service.\n- Patched in version 1.22.4.\n- CVSS: 7.5.\n","chars":150},"_meta":{"tool":"web_browse"}}`
	card := extractEvidenceCardFromPage(context.Background(), "https://pkg.go.dev/vuln/GO-2024-24790", envelopeJSON, "Analyze Go standard library CVEs and patched versions")
	if card.Title != "Go Security Advisory GO-2024-24790" {
		t.Errorf("expected parsed title 'Go Security Advisory GO-2024-24790', got %q", card.Title)
	}
	if len(card.KeyFacts) < 2 {
		t.Fatalf("expected at least 2 key facts extracted from ToolResult envelope, got %d", len(card.KeyFacts))
	}
}


