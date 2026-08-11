package executor

import (
	"regexp"
	"strings"
)

// CoverageResult reports how many required items from the goal are present
// in the synthesis output (FM5 mitigation).
type CoverageResult struct {
	TotalRequired int      `json:"totalRequired"`
	Covered       int      `json:"covered"`
	Missing       []string `json:"missing,omitempty"`
}

// numberedListPattern matches numbered list items (e.g., "1.", "2.", "3.")
var numberedListPattern = regexp.MustCompile(`(?m)^\s*\d+\.\s+(.+)$`)

// bulletListPattern matches bullet list items (e.g., "- item", "* item")
var bulletListPattern = regexp.MustCompile(`(?m)^\s*[-*]\s+(.+)$`)

// CheckCoverage extracts required items from the goal and verifies each
// appears in the synthesis output. Only checks when the goal contains
// extractable item lists (numbered or bulleted).
//
// Returns nil when the goal has no extractable items (skip check).
func CheckCoverage(goal string, synthesis string) *CoverageResult {
	items := extractGoalItems(goal)
	if len(items) == 0 {
		return nil
	}

	lowerSynthesis := strings.ToLower(synthesis)
	result := &CoverageResult{
		TotalRequired: len(items),
	}

	for _, item := range items {
		// Normalize the item for matching: extract key terms
		keyTerms := extractCoverageKeyTerms(item)
		if coverageKeyTermsPresent(keyTerms, lowerSynthesis) {
			result.Covered++
		} else {
			result.Missing = append(result.Missing, item)
		}
	}

	return result
}

// extractGoalItems extracts enumerated/bulleted items from the goal text.
func extractGoalItems(goal string) []string {
	var items []string
	seen := make(map[string]bool)

	// Extract numbered list items
	for _, match := range numberedListPattern.FindAllStringSubmatch(goal, -1) {
		if len(match) > 1 {
			item := strings.TrimSpace(match[1])
			if item != "" && !seen[item] {
				items = append(items, item)
				seen[item] = true
			}
		}
	}

	// Extract bullet list items
	for _, match := range bulletListPattern.FindAllStringSubmatch(goal, -1) {
		if len(match) > 1 {
			item := strings.TrimSpace(match[1])
			if item != "" && !seen[item] {
				items = append(items, item)
				seen[item] = true
			}
		}
	}

	return items
}

// extractCoverageKeyTerms pulls significant words from an item for fuzzy matching.
// Strips common filler words and returns lowercased terms.
func extractCoverageKeyTerms(item string) []string {
	fillerWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"of": true, "in": true, "to": true, "for": true, "with": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"it": true, "its": true, "this": true, "that": true, "how": true,
		"what": true, "from": true, "by": true, "on": true, "at": true,
	}

	words := strings.Fields(strings.ToLower(item))
	var terms []string
	for _, w := range words {
		// Strip punctuation
		w = strings.Trim(w, ".,;:!?()[]{}\"'`")
		if w != "" && !fillerWords[w] && len(w) > 2 {
			terms = append(terms, w)
		}
	}
	return terms
}

// coverageKeyTermsPresent checks if a sufficient proportion of key terms from the
// goal item appear in the synthesis. Requires >50% of terms to match.
func coverageKeyTermsPresent(terms []string, lowerSynthesis string) bool {
	if len(terms) == 0 {
		return true // No key terms = vacuously present
	}

	matched := 0
	for _, term := range terms {
		if strings.Contains(lowerSynthesis, term) {
			matched++
		}
	}

	// Require majority of terms to be present
	return float64(matched)/float64(len(terms)) > 0.5
}
