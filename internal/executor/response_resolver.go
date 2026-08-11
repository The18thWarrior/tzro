package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/inference"
)

// keyMatch represents a single match found during recursive key search.
// Path is the dot-notation path to the value (e.g., "data.contact.email").
// Value is the matched value at that path.
type keyMatch struct {
	Path  string
	Value interface{}
}

// recursiveKeySearch walks a parsed JSON structure (map or array) recursively,
// searching for all keys matching targetKey at any depth. Returns all matches
// as (path, value) pairs — the caller decides what to do with single vs.
// multiple matches.
//
// This is a pure function: no side effects, no database access, no inference calls.
func recursiveKeySearch(data interface{}, targetKey string) []keyMatch {
	var matches []keyMatch
	recursiveKeySearchHelper(data, targetKey, "", &matches)
	return matches
}

func recursiveKeySearchHelper(data interface{}, targetKey string, currentPath string, matches *[]keyMatch) {
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			fullPath := key
			if currentPath != "" {
				fullPath = currentPath + "." + key
			}
			if key == targetKey {
				*matches = append(*matches, keyMatch{Path: fullPath, Value: val})
			}
			// Continue recursing even after a match — there may be deeper matches
			recursiveKeySearchHelper(val, targetKey, fullPath, matches)
		}
	case []interface{}:
		for i, item := range v {
			indexedPath := fmt.Sprintf("%s[%d]", currentPath, i)
			if currentPath == "" {
				indexedPath = fmt.Sprintf("[%d]", i)
			}
			recursiveKeySearchHelper(item, targetKey, indexedPath, matches)
		}
	}
}

// resolveBindingSemantic uses the Local Model to semantically match a binding key
// to a value in the raw tool output. This is the Tier 3 fallback in the resolution
// cascade, invoked when deterministic methods (recursive key search, KV-line search)
// fail or produce key collisions.
//
// The prompt is designed for minimal token consumption (~100 input tokens, ~20 output tokens).
// Works on any output format — JSON, XML, plain text, KV lines.
func resolveBindingSemantic(ctx context.Context, rawOutput string, bindingKey string) (string, error) {
	if rawOutput == "" {
		return "", fmt.Errorf("empty raw output — cannot resolve binding '%s' semantically", bindingKey)
	}

	// Truncate very large outputs to keep inference fast. 4000 chars is ~1000 tokens,
	// which is more than enough context for value extraction.
	truncatedOutput := rawOutput
	if len(truncatedOutput) > 4000 {
		truncatedOutput = truncatedOutput[:4000] + "\n...[truncated]"
	}

	systemPrompt := "You are a precise data extraction assistant. Given a tool output, identify the value that best matches a requested property name. Return ONLY the raw value, copied EXACTLY character-for-character from the output. Do NOT modify, abbreviate, rephrase, or correct the value in any way. Copy it verbatim — every character, slash, dash, and letter must match exactly."
	userPrompt := fmt.Sprintf("Tool output:\n%s\n\nWhich value corresponds to '%s'? Copy the exact value from the output above. Do not change any characters.", truncatedOutput, bindingKey)

	req := inference.NewSimpleRequest(systemPrompt, userPrompt, "")
	req.TaskID = "" // No task association — this is a lightweight utility call

	result, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return "", fmt.Errorf("semantic binding resolution failed for '%s': %w", bindingKey, err)
	}

	// Clean up the response — strip whitespace, surrounding quotes, and common prefixes
	resolved := strings.TrimSpace(result)
	resolved = strings.Trim(resolved, "\"'`")
	resolved = strings.TrimSpace(resolved)

	// Degeneration guard: reject if a single token/phrase is repeated excessively.
	// The local model sometimes degenerates into repeating the binding key itself
	// hundreds of times (e.g., "synthesis\nsynthesis\nsynthesis\n...").
	if isDegenerateRepetition(resolved) {
		fmt.Fprintf(os.Stderr, "[Response Resolver] Semantic fallback REJECTED for '%s' — degenerate repetition detected\n", bindingKey)
		return "", fmt.Errorf("semantic resolver returned degenerate repetition for binding '%s'", bindingKey)
	}

	if resolved == "" || strings.EqualFold(resolved, "null") || strings.EqualFold(resolved, "none") || strings.EqualFold(resolved, "n/a") {
		return "", fmt.Errorf("semantic resolver returned empty/null for binding '%s'", bindingKey)
	}

	fmt.Fprintf(os.Stderr, "[Response Resolver] Semantic fallback resolved '%s' → %q\n", bindingKey, resolved)
	return resolved, nil
}

// isDegenerateRepetition detects when text consists of a single word or short
// phrase repeated many times (e.g., "synthesis\nsynthesis\nsynthesis...").
// Returns true when a single token accounts for >80% of the content and appears
// 10+ times. This catches local model degeneration where the semantic fallback
// resolves a binding key to the key name itself repeated hundreds of times.
func isDegenerateRepetition(s string) bool {
	if len(s) < 50 {
		return false // Too short to be degenerate
	}

	// Split on whitespace and newlines, count token frequencies
	tokens := strings.Fields(strings.ReplaceAll(s, "\n", " "))
	if len(tokens) < 10 {
		return false
	}

	freq := make(map[string]int)
	for _, t := range tokens {
		freq[strings.ToLower(t)]++
	}

	// Find the most frequent token
	var maxCount int
	for _, count := range freq {
		if count > maxCount {
			maxCount = count
		}
	}

	// Degenerate if one token appears 10+ times AND accounts for >80% of all tokens
	return maxCount >= 10 && float64(maxCount)/float64(len(tokens)) > 0.8
}

// formatMatchValue converts a keyMatch value to its string representation.
// Maps and slices are JSON-marshaled; all other types use fmt.Sprintf.
func formatMatchValue(val interface{}) string {
	switch v := val.(type) {
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		return string(b)
	case []interface{}:
		b, _ := json.Marshal(v)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// fuzzyKeySearch performs a relaxed key match against a parsed JSON structure when
// the exact recursive key search finds 0 matches. It normalizes both the target key
// and all JSON keys by checking suffix/substring containment relationships.
//
// For example, if the binding asks for "receipt_code_path" but the JSON has "receipt_path",
// this will match because "receipt_path" is a suffix-contained substring of "receipt_code_path".
//
// Returns a single keyMatch if exactly one fuzzy match is found, nil otherwise.
// Multiple fuzzy matches are ambiguous — fall through to semantic fallback.
func fuzzyKeySearch(data interface{}, targetKey string) *keyMatch {
	// Collect all leaf keys from the JSON structure
	var allKeys []keyMatch
	collectAllKeys(data, "", &allKeys)

	if len(allKeys) == 0 {
		return nil
	}

	targetNorm := normalizeBindingKey(targetKey)
	var fuzzyMatches []keyMatch

	for _, km := range allKeys {
		// Extract just the leaf key name from the path
		leafKey := km.Path
		if dotIdx := strings.LastIndex(leafKey, "."); dotIdx != -1 {
			leafKey = leafKey[dotIdx+1:]
		}
		keyNorm := normalizeBindingKey(leafKey)

		// Check 1: Exact normalized match (after prefix stripping)
		if keyNorm == targetNorm {
			fuzzyMatches = append(fuzzyMatches, km)
		} else if strings.HasSuffix(targetNorm, keyNorm) || strings.HasSuffix(keyNorm, targetNorm) {
			// Check 2: Suffix containment
			fuzzyMatches = append(fuzzyMatches, km)
		} else if strings.Contains(targetNorm, keyNorm) && len(keyNorm) >= 4 {
			// Check 3: Substring containment with minimum length
			fuzzyMatches = append(fuzzyMatches, km)
		} else if segmentsAreOrderedSubset(keyNorm, targetNorm) {
			// Check 4: Segment-based matching. Split on '_' and check if one key's
			// segments are an ordered subset of the other. This catches:
			//   receipt_code_path → receipt_path (segments {receipt,path} ⊂ {receipt,code,path})
			fuzzyMatches = append(fuzzyMatches, km)
		}
	}

	if len(fuzzyMatches) == 1 {
		return &fuzzyMatches[0]
	}

	// Multiple or zero fuzzy matches — ambiguous, fall through
	return nil
}

// segmentsAreOrderedSubset checks if the segments of 'shorter' (split on '_') appear
// as an ordered subsequence within the segments of 'longer'. Both keys must have at
// least 2 segments each, and the shorter must have at least 2 matching segments.
// Example: "receipt_path" segments [receipt, path] ⊂ "receipt_code_path" [receipt, code, path] → true
func segmentsAreOrderedSubset(a, b string) bool {
	aParts := strings.Split(a, "_")
	bParts := strings.Split(b, "_")

	// Ensure both have at least 2 segments to avoid false matches on single-word keys
	if len(aParts) < 2 || len(bParts) < 2 {
		return false
	}

	// Determine which is shorter/longer
	shorter, longer := aParts, bParts
	if len(aParts) > len(bParts) {
		shorter, longer = bParts, aParts
	}

	// Shorter must be strictly shorter to be a meaningful subset
	if len(shorter) >= len(longer) {
		return false
	}

	// Check if shorter's segments appear in order within longer's segments
	j := 0
	matched := 0
	for i := 0; i < len(longer) && j < len(shorter); i++ {
		if longer[i] == shorter[j] {
			j++
			matched++
		}
	}
	// Require all segments of the shorter key to be found, with at least 2 matches
	return matched == len(shorter) && matched >= 2
}

// normalizeBindingKey strips common prefixes and suffixes used in binding key naming
// conventions to enable fuzzy matching between planner-generated and tool-output keys.
func normalizeBindingKey(key string) string {
	key = strings.ToLower(key)
	// Strip common decorative prefixes that planners add
	for _, prefix := range []string{"default_", "calculated_", "generated_", "resolved_", "primary_"} {
		key = strings.TrimPrefix(key, prefix)
	}
	return key
}

// collectAllKeys walks a parsed JSON structure and collects all leaf key-value pairs.
func collectAllKeys(data interface{}, currentPath string, results *[]keyMatch) {
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			fullPath := key
			if currentPath != "" {
				fullPath = currentPath + "." + key
			}
			// Check if val is a leaf (not a map or slice)
			switch val.(type) {
			case map[string]interface{}, []interface{}:
				collectAllKeys(val, fullPath, results)
			default:
				*results = append(*results, keyMatch{Path: fullPath, Value: val})
			}
		}
	case []interface{}:
		for i, item := range v {
			indexedPath := fmt.Sprintf("%s[%d]", currentPath, i)
			if currentPath == "" {
				indexedPath = fmt.Sprintf("[%d]", i)
			}
			collectAllKeys(item, indexedPath, results)
		}
	}
}

// isDerivedCacheBindingKey returns true if the property key is a generic data
// reference that planners commonly use to bind to upstream tabular data outputs.
// When these keys have 0 exact matches in the upstream JSON but a derivedCacheId
// exists, the resolver should use the derivedCacheId instead of falling through
// to the error-prone semantic fallback.
//
// Red-team FM-2 fix: The planner generates bindings like "data": "node.output.content"
// or "data": "node.output.filtered_data", but tool outputs use keys like "rows",
// "results", or just raw JSON arrays. The semantic fallback hallucinates garbage
// for these generic keys. Resolving to the derivedCacheId is deterministic and correct.
func isDerivedCacheBindingKey(key string) bool {
	switch strings.ToLower(key) {
	case "content", "data", "filtered_data", "csv_data", "raw_data",
		"result", "results", "output", "rows", "records":
		return true
	default:
		return false
	}
}
