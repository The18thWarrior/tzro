package symbols

// anchor_check.go — Symbol Anchor Check for post-synthesis hallucination detection.
//
// After the Recall Node produces its synthesis, the Symbol Anchor Check parses
// the output for referenced symbol names and diffs them against the Symbol Index.
// Uses a two-tier filter: dot-qualified external references (e.g., context.Context)
// are skipped; only unqualified identifiers absent from the Index are flagged.
// See ADR-0047.

import (
	"fmt"
	"regexp"
	"strings"
)

// AnchorCheckResult contains the outcome of diffing synthesis output against
// the Symbol Index.
type AnchorCheckResult struct {
	TotalReferenced  int      // total symbol-like identifiers found in output
	Anchored         int      // count matching the Symbol Index
	Unanchored       []string // names not found in the index
	ExternalSkipped  int      // dot-qualified names skipped (e.g., context.Context)
	HallucinationPct float64  // unanchored / totalReferenced (externals already excluded from count)
	NeedsCorrection  bool     // hallucinationPct > threshold
}

// DefaultAnchorThreshold is the hallucination percentage above which
// a targeted correction pass is triggered.
const DefaultAnchorThreshold = 0.20

// CheckAnchoring parses the output text for symbol references and
// checks each against the provided Symbol Index.
//
// Detection strategy:
//   - Extract PascalCase identifiers (exported Go symbols) from code blocks and bold text
//   - Skip dot-qualified references (e.g., context.Context, sync.Mutex)
//   - Skip common Go/language built-in type names
//   - Compare remaining identifiers against the Symbol Index
func CheckAnchoring(output string, index []Symbol, threshold float64) AnchorCheckResult {
	if threshold <= 0 {
		threshold = DefaultAnchorThreshold
	}

	// Build index lookup set
	indexSet := make(map[string]bool, len(index))
	for _, sym := range index {
		indexSet[sym.Name] = true
	}

	// Extract symbol-like references from the output
	references := extractReferences(output)

	var result AnchorCheckResult
	seen := make(map[string]bool) // deduplicate

	for _, ref := range references {
		if seen[ref] {
			continue
		}
		seen[ref] = true

		// Tier 1: Skip dot-qualified external references
		if strings.Contains(ref, ".") {
			result.ExternalSkipped++
			continue
		}

		// Skip common built-in types
		if isBuiltinType(ref) {
			result.ExternalSkipped++
			continue
		}

		result.TotalReferenced++

		if indexSet[ref] {
			result.Anchored++
		} else {
			result.Unanchored = append(result.Unanchored, ref)
		}
	}

	// Calculate hallucination percentage
	denominator := result.TotalReferenced
	if denominator > 0 {
		result.HallucinationPct = float64(len(result.Unanchored)) / float64(denominator)
	}

	result.NeedsCorrection = denominator > 0 && result.HallucinationPct > threshold

	return result
}

// BuildCorrectionPrompt generates a short prompt for surgical replacement
// of hallucinated names with real ones from the index.
func BuildCorrectionPrompt(output string, unanchored []string, index []Symbol) string {
	var sb strings.Builder

	sb.WriteString("The following names appear in your output but don't exist in the codebase:\n")
	for _, name := range unanchored {
		sb.WriteString("- ")
		sb.WriteString(name)
		sb.WriteString("\n")
	}

	sb.WriteString("\nThe actual symbols in the codebase are:\n")
	for _, sym := range index {
		sb.WriteString(fmt.Sprintf("- %s (%s): %s [%s:%d]\n", sym.Name, sym.Kind, sym.Signature, sym.File, sym.Line))
	}

	sb.WriteString("\nCorrect the output by replacing the hallucinated names with the real ones from the list above. ")
	sb.WriteString("Only fix the names — do not change the structure or add new content.\n\n")
	sb.WriteString("Original output:\n")
	sb.WriteString(output)

	return sb.String()
}

// --- Internal helpers ---

// identifierRe matches PascalCase identifiers (exported Go symbols)
// that look like type/function names (at least 2 characters, starts with uppercase letter).
var identifierRe = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9_]{1,})\b`)

// codeBlockIdentifierRe matches identifiers inside backtick code spans,
// including dot-qualified references like context.Context
var codeBlockIdentifierRe = regexp.MustCompile("`([A-Za-z][A-Za-z0-9_.]*[A-Za-z0-9_])`")

// boldIdentifierRe matches bold identifiers like **TypeName**
var boldIdentifierRe = regexp.MustCompile(`\*\*([A-Z][a-zA-Z0-9_]+)\*\*`)

// extractReferences extracts symbol-like identifiers from markdown output.
func extractReferences(output string) []string {
	seen := make(map[string]bool)
	var refs []string

	addRef := func(name string) {
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}

	// Extract from inline code: `TypeName`
	for _, match := range codeBlockIdentifierRe.FindAllStringSubmatch(output, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	// Extract from bold text: **TypeName**
	for _, match := range boldIdentifierRe.FindAllStringSubmatch(output, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	// Extract PascalCase identifiers from code blocks
	lines := strings.Split(output, "\n")
	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			for _, match := range identifierRe.FindAllStringSubmatch(line, -1) {
				if len(match) > 1 {
					addRef(match[1])
				}
			}
		}
	}

	return refs
}

// builtinTypes are common type names that appear in documentation
// but are language primitives, not codebase symbols.
var builtinTypes = map[string]bool{
	// Go
	"Context": true, "String": true, "Error": true, "Reader": true, "Writer": true,
	"Handler": true, "Server": true, "Client": true, "Request": true, "Response": true,
	"Mutex": true, "WaitGroup": true, "Once": true,
	// General
	"Object": true, "Array": true, "Map": true, "List": true, "Set": true,
	"Integer": true, "Boolean": true, "Float": true, "Double": true,
	"Type": true, "Interface": true, "Function": true, "Class": true,
	// Common markdown words that look like PascalCase
	"The": true, "This": true, "Each": true, "All": true, "Some": true,
	"None": true, "True": true, "False": true, "Null": true, "Nil": true,
	"NOTE": true, "TODO": true, "FIXME": true, "WARNING": true,
}

func isBuiltinType(name string) bool {
	return builtinTypes[name]
}
