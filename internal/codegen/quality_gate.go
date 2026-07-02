package codegen

import (
	"fmt"
	"strings"

	"tzro/internal/tools"
)

// QualityGateResult holds the outcome of a post-generation quality check.
type QualityGateResult struct {
	Pass   bool   // true if the output passes structural validation
	Reason string // explanation if fail
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
// This is the first layer of the quality gate. The compilation gate
// (RunCompilationGate) composes on top of this when a toolchain is available.
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

// RunCompilationGate runs the language-appropriate compiler/type-checker against
// the file at filePath and returns a pass/fail result. This is the second layer
// of the quality gate, composing after RunStructuralQualityGate.
//
// If no compilation command is available for the language (e.g., unknown or
// unsupported), the gate is skipped gracefully (returns Pass: true).
func RunCompilationGate(language, filePath string) QualityGateResult {
	command, available := CompilationCommand(language, filePath)
	if !available {
		return QualityGateResult{Pass: true, Reason: "no compilation command available — skipped"}
	}

	// Replace {{targetFile}} placeholder with actual path
	command = strings.ReplaceAll(command, "{{targetFile}}", filePath)

	result, err := tools.ValidateCode(command, filePath)
	if err != nil {
		return QualityGateResult{
			Pass:   false,
			Reason: fmt.Sprintf("compilation command failed to execute: %v", err),
		}
	}

	if !result.Passed {
		// Truncate long error output for the Reason field
		errors := result.Errors
		if len(errors) > 500 {
			errors = errors[:500] + "..."
		}
		return QualityGateResult{
			Pass:   false,
			Reason: fmt.Sprintf("compilation failed (%d errors): %s", result.ErrorCount, errors),
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
