package codegen

import (
	"fmt"
	"strings"
)

// QualityGateResult holds the outcome of a post-generation quality check.
type QualityGateResult struct {
	Pass    bool   // true if the output passes structural validation
	Reason  string // explanation if fail
}

// RunStructuralQualityGate performs a lightweight structural validation of
// generated code output. It checks for common failure modes without requiring
// a running LLM or compiler:
//
//  1. Non-empty output
//  2. No markdown fences remaining after stripping
//  3. No prose-like content (explanations, commentary mixed in)
//  4. Contains at least one language-specific keyword
//
// This is the first layer of the quality gate. The local-model inference check
// and WS3 compilation gate compose on top of this when available.
func RunStructuralQualityGate(output, language string) QualityGateResult {
	trimmed := strings.TrimSpace(output)

	// Check 1: non-empty
	if trimmed == "" {
		return QualityGateResult{Pass: false, Reason: "output is empty"}
	}

	// Check 2: still contains markdown fences (model didn't produce raw code)
	if strings.Contains(trimmed, "```") {
		return QualityGateResult{Pass: false, Reason: "output contains markdown fences"}
	}

	// Check 3: prose indicators — lines starting with common explanation patterns
	proseIndicators := []string{
		"Here is", "Here's", "I'll", "I will", "Let me",
		"The following", "This code", "Note:", "Note that",
		"Explanation:", "Summary:",
	}
	firstLine := strings.SplitN(trimmed, "\n", 2)[0]
	for _, indicator := range proseIndicators {
		if strings.HasPrefix(firstLine, indicator) {
			return QualityGateResult{
				Pass:   false,
				Reason: fmt.Sprintf("output starts with prose: %q", firstLine),
			}
		}
	}

	// Check 4: contains at least one language-specific keyword
	keywords := languageKeywords(language)
	if len(keywords) > 0 {
		found := false
		for _, kw := range keywords {
			if strings.Contains(trimmed, kw) {
				found = true
				break
			}
		}
		if !found {
			return QualityGateResult{
				Pass:   false,
				Reason: fmt.Sprintf("output contains no %s keywords", language),
			}
		}
	}

	return QualityGateResult{Pass: true}
}

// languageKeywords returns common keywords expected in valid source code
// for the given language. Returns nil for unknown languages (skips check).
func languageKeywords(language string) []string {
	switch language {
	case "go":
		return []string{"package ", "func ", "type ", "import ", "var ", "const "}
	case "typescript", "javascript":
		return []string{"export ", "import ", "function ", "const ", "class ", "interface ", "type "}
	case "python":
		return []string{"def ", "class ", "import ", "from ", "return "}
	case "rust":
		return []string{"fn ", "struct ", "impl ", "use ", "pub ", "mod "}
	default:
		return nil
	}
}
