package compactor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Symbolic In-Context Dictionary (ADR-0092)
// ---------------------------------------------------------------------------

// DictionaryEncoder provides transparent, lossless symbolic compression of
// highly repetitive in-context payloads (e.g. repeated directory paths, package
// paths, JSON schema headers, and method signatures) to reduce prefill tokens.
type DictionaryEncoder struct {
	MinTextBytes     int
	MinMatchLen      int
	MinOccurrences   int
	MinSavingsRatio  float64
	MaxDictionaryKeys int
}

// NewDictionaryEncoder creates a new DictionaryEncoder with default production settings.
func NewDictionaryEncoder() *DictionaryEncoder {
	return &DictionaryEncoder{
		MinTextBytes:      4096,
		MinMatchLen:       8,
		MinOccurrences:    3,
		MinSavingsRatio:   0.10, // 10% minimum net savings required
		MaxDictionaryKeys: 20,
	}
}

type candidateMatch struct {
	pattern    string
	count      int
	netSavings int
}

// Encode inspects the input text. If the text exceeds MinTextBytes and contains
// sufficient repetition, it extracts high-frequency substrings into a symbolic
// header dictionary and returns the encoded text and dictionary map.
// If compression threshold is not met, returns original text, nil, false.
func (d *DictionaryEncoder) Encode(text string) (string, map[string]string, bool) {
	if len(text) < d.MinTextBytes {
		return text, nil, false
	}

	candidates := d.discoverCandidates(text)
	if len(candidates) == 0 {
		return text, nil, false
	}

	// Select top candidates up to MaxDictionaryKeys
	selected := candidates
	if len(selected) > d.MaxDictionaryKeys {
		selected = selected[:d.MaxDictionaryKeys]
	}

	// Build dictionary map and substitutions
	dict := make(map[string]string)
	metaToPattern := make(map[string]string)

	for idx, c := range selected {
		metaKey := fmt.Sprintf("§%d", idx+1)
		metaToken := fmt.Sprintf("[%s]", metaKey)
		dict[metaKey] = c.pattern
		metaToPattern[metaToken] = c.pattern
	}

	jsonBytes, err := json.Marshal(dict)
	if err != nil {
		return text, nil, false
	}
	header := fmt.Sprintf("[DICTIONARY: %s]\n\n", string(jsonBytes))

	// Apply substitutions in descending order of pattern length
	encodedBody := text
	for idx := range selected {
		metaKey := fmt.Sprintf("§%d", idx+1)
		metaToken := fmt.Sprintf("[%s]", metaKey)
		pattern := dict[metaKey]
		encodedBody = strings.ReplaceAll(encodedBody, pattern, metaToken)
	}

	fullEncoded := header + encodedBody

	// Check net savings
	savedBytes := len(text) - len(fullEncoded)
	if float64(savedBytes)/float64(len(text)) < d.MinSavingsRatio {
		return text, nil, false
	}

	return fullEncoded, dict, true
}

// Decode restores the original text by substituting all meta-tokens back to their original strings.
func (d *DictionaryEncoder) Decode(encoded string, dict map[string]string) string {
	if len(dict) == 0 {
		return encoded
	}

	// Strip header if present
	body := encoded
	if idx := strings.Index(body, "[DICTIONARY:"); idx != -1 {
		if endIdx := strings.Index(body[idx:], "]\n\n"); endIdx != -1 {
			body = body[idx+endIdx+3:]
		}
	}

	for metaKey, pattern := range dict {
		metaToken := fmt.Sprintf("[%s]", metaKey)
		body = strings.ReplaceAll(body, metaToken, pattern)
	}

	return body
}

var dictHeaderRegex = regexp.MustCompile(`(?s)^\[DICTIONARY:\s*(\{.*?\})\]\n\n`)

// DecodeWithHeader automatically parses any embedded [DICTIONARY: ...] header and decodes the body.
func DecodeWithHeader(encoded string) (string, error) {
	match := dictHeaderRegex.FindStringSubmatch(encoded)
	if match == nil {
		return encoded, nil
	}

	headerJSON := match[1]
	body := encoded[len(match[0]):]

	var dict map[string]string
	if err := json.Unmarshal([]byte(headerJSON), &dict); err != nil {
		return encoded, fmt.Errorf("failed to parse dictionary header: %w", err)
	}

	for metaKey, pattern := range dict {
		metaToken := fmt.Sprintf("[%s]", metaKey)
		body = strings.ReplaceAll(body, metaToken, pattern)
	}

	return body, nil
}

// discoverCandidates scans text for repeated substrings suitable for compression.
func (d *DictionaryEncoder) discoverCandidates(text string) []candidateMatch {
	counts := make(map[string]int)

	// Whitelist & structural pattern extractors:
	// 1. Path-like segments (/foo/bar/baz/, github.com/...)
	pathRegex := regexp.MustCompile(`(?:[a-zA-Z0-9_\-\.]+/){2,}`)
	for _, match := range pathRegex.FindAllString(text, -1) {
		if len(match) >= d.MinMatchLen {
			counts[match]++
		}
	}

	// 2. Common JSON schema keys and repetitive prefixes
	schemaRegex := regexp.MustCompile(`\{"type":\s*"[a-zA-Z0-9_\-]+"(?:,\s*"[a-zA-Z0-9_\-]+":\s*\{[^}]+\})*\}`)
	for _, match := range schemaRegex.FindAllString(text, -1) {
		if len(match) >= d.MinMatchLen {
			counts[match]++
		}
	}

	// 3. Repeated word clusters / identifiers (e.g. repeated long function signatures or identifiers)
	identRegex := regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]{15,}`)
	for _, match := range identRegex.FindAllString(text, -1) {
		if len(match) >= d.MinMatchLen {
			counts[match]++
		}
	}

	// 4. Repeated line-level fragments
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= d.MinMatchLen && len(trimmed) <= 120 {
			counts[trimmed]++
		}
	}

	var candidates []candidateMatch
	for pattern, count := range counts {
		if count >= d.MinOccurrences {
			metaTokenLen := 4 // "[§1]" is 4 runes
			headerOverhead := len(pattern) + 6 // "§1=..., "
			savings := (len(pattern)-metaTokenLen)*count - headerOverhead
			if savings > 30 {
				candidates = append(candidates, candidateMatch{
					pattern:    pattern,
					count:      count,
					netSavings: savings,
				})
			}
		}
	}

	// Sort by descending net savings, then descending pattern length
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].netSavings != candidates[j].netSavings {
			return candidates[i].netSavings > candidates[j].netSavings
		}
		return len(candidates[i].pattern) > len(candidates[j].pattern)
	})

	// Filter overlapping candidates (e.g. substring of higher-ranked candidate)
	var filtered []candidateMatch
	for _, c := range candidates {
		isSubstring := false
		for _, kept := range filtered {
			if strings.Contains(kept.pattern, c.pattern) {
				isSubstring = true
				break
			}
		}
		if !isSubstring {
			filtered = append(filtered, c)
		}
	}

	return filtered
}
