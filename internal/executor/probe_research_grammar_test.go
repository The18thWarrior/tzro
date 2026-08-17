package executor

import (
	"strings"
	"testing"
)

func TestBuildResearchMarkdownGrammar(t *testing.T) {
	grammar := buildResearchMarkdownGrammar("Compare LLM frameworks")

	if !strings.HasPrefix(strings.TrimSpace(grammar), "root ::=") {
		t.Errorf("expected GBNF grammar to start with root ::=, got:\n%s", grammar)
	}

	if !strings.Contains(grammar, "table-section") {
		t.Errorf("expected grammar to enforce table-section, got:\n%s", grammar)
	}

	if !strings.Contains(grammar, "sources-section") {
		t.Errorf("expected grammar to enforce sources-section, got:\n%s", grammar)
	}

	if !strings.Contains(grammar, "table-divider") {
		t.Errorf("expected grammar to enforce markdown table divider, got:\n%s", grammar)
	}
}
