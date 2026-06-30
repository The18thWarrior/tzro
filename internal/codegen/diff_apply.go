package codegen

import (
	"fmt"
	"strings"
)

// ApplyDiffHunks applies structured hunks to existing file content.
// Returns the patched content or an error if any hunk fails to match.
//
// Matching strategy (per hunk):
//  1. Exact substring match (strings.Index)
//  2. Fuzzy match: normalize whitespace and retry (collapse runs of spaces/tabs)
//  3. Fail with error identifying the unmatched searchContent (first 80 chars)
//
// Hunks are applied sequentially in order. Each successful application updates
// the working content, so subsequent hunks match against the already-patched file.
func ApplyDiffHunks(existingContent string, hunks []DiffHunk) (string, error) {
	if existingContent == "" && len(hunks) > 0 {
		return "", fmt.Errorf("cannot apply diff hunks to an empty file; use full mode for new files")
	}

	content := existingContent

	for i, hunk := range hunks {
		if hunk.SearchContent == "" {
			return "", fmt.Errorf("hunk %d: searchContent cannot be empty", i)
		}

		patched, err := applyOneHunk(content, hunk, i)
		if err != nil {
			return "", err
		}
		content = patched
	}

	return content, nil
}

// errAmbiguousMatch is a sentinel type to distinguish ambiguity from no-match.
type errAmbiguousMatch struct {
	msg string
}

func (e *errAmbiguousMatch) Error() string { return e.msg }

// applyOneHunk applies a single hunk to the content, first trying exact match,
// then fuzzy whitespace match.
func applyOneHunk(content string, hunk DiffHunk, index int) (string, error) {
	// Strategy 1: exact substring match
	patched, err := applyExact(content, hunk, index)
	if err == nil {
		return patched, nil
	}
	// Ambiguity errors should not fall through to fuzzy match
	if _, ok := err.(*errAmbiguousMatch); ok {
		return "", err
	}

	// Strategy 2: fuzzy whitespace match
	patched, err = applyFuzzy(content, hunk, index)
	if err == nil {
		return patched, nil
	}
	// Propagate ambiguity errors from fuzzy match too
	if _, ok := err.(*errAmbiguousMatch); ok {
		return "", err
	}

	// Both strategies failed
	preview := hunk.SearchContent
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	return "", fmt.Errorf("hunk %d: searchContent not found in file: %q", index, preview)
}

// applyExact applies a hunk using exact substring matching.
func applyExact(content string, hunk DiffHunk, index int) (string, error) {
	firstIdx := strings.Index(content, hunk.SearchContent)
	if firstIdx == -1 {
		return "", fmt.Errorf("no exact match")
	}

	// Check for duplicate matches (ambiguity)
	secondIdx := strings.Index(content[firstIdx+1:], hunk.SearchContent)
	if secondIdx != -1 {
		preview := hunk.SearchContent
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		return "", &errAmbiguousMatch{
			msg: fmt.Sprintf(
				"hunk %d: searchContent matches multiple locations; include more context lines for uniqueness: %q",
				index, preview,
			),
		}
	}

	// Apply the replacement
	return content[:firstIdx] + hunk.ReplaceContent + content[firstIdx+len(hunk.SearchContent):], nil
}

// normalizeWhitespace collapses runs of spaces and tabs into single spaces.
func normalizeWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inWS := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inWS {
				b.WriteRune(' ')
				inWS = true
			}
		} else {
			b.WriteRune(r)
			inWS = false
		}
	}
	return b.String()
}

// applyFuzzy applies a hunk using whitespace-normalized matching.
// When a fuzzy match is found, the original (non-normalized) searchContent region
// in the file is replaced with the hunk's replaceContent.
func applyFuzzy(content string, hunk DiffHunk, index int) (string, error) {
	normContent := normalizeWhitespace(content)
	normSearch := normalizeWhitespace(hunk.SearchContent)

	firstIdx := strings.Index(normContent, normSearch)
	if firstIdx == -1 {
		return "", fmt.Errorf("no fuzzy match")
	}

	// Check for duplicate fuzzy matches
	secondIdx := strings.Index(normContent[firstIdx+1:], normSearch)
	if secondIdx != -1 {
		preview := hunk.SearchContent
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		return "", &errAmbiguousMatch{
			msg: fmt.Sprintf(
				"hunk %d: searchContent matches multiple locations (fuzzy); include more context lines for uniqueness: %q",
				index, preview,
			),
		}
	}

	// Map the normalized index back to the original content.
	// Walk both strings in parallel to find the corresponding original range.
	origStart, origEnd := mapNormalizedRange(content, normContent, firstIdx, firstIdx+len(normSearch))

	return content[:origStart] + hunk.ReplaceContent + content[origEnd:], nil
}

// mapNormalizedRange maps a [start, end) range in normalized content back to
// the corresponding range in the original content.
func mapNormalizedRange(original, normalized string, normStart, normEnd int) (int, int) {
	// Walk the original and normalized strings in parallel.
	// For each character in the normalized string, track which original index it came from.
	origIdx := 0
	normIdx := 0
	origStart := 0
	origEnd := len(original)

	origRunes := []rune(original)
	inWS := false

	for normIdx < len(normalized) && origIdx < len(origRunes) {
		origR := origRunes[origIdx]

		if origR == ' ' || origR == '\t' {
			if !inWS {
				// This whitespace char maps to the single space in normalized
				if normIdx == normStart {
					origStart = origIdx
				}
				normIdx++
				inWS = true
			}
			// Skip additional whitespace characters in original
			origIdx++
			continue
		}

		inWS = false
		if normIdx == normStart {
			origStart = origIdx
		}
		normIdx++
		if normIdx == normEnd {
			origEnd = origIdx + len(string(origR))
			break
		}
		origIdx++
	}

	// If we hit normEnd exactly at the boundary
	if normIdx >= normEnd && origEnd == len(original) {
		origEnd = origIdx
	}

	return origStart, origEnd
}
