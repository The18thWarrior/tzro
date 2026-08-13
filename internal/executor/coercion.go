package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"tzro/internal/tools"
)

func isPlaceholderValue(val, fieldName string) bool {
	trimmed := strings.TrimSpace(val)

	// Empty or whitespace-only
	if trimmed == "" {
		return true
	}

	// Value equals its own field name (model echoed the key as value)
	if strings.EqualFold(trimmed, fieldName) {
		return true
	}

	// Unresolved template literal: {path}, {query}, {value}/, etc.
	if templatePattern.MatchString(trimmed) {
		return true
	}

	// Red-team FM-5 fix: Detect garbled Unicode in short identifier-like values.
	// Cloud retries occasionally produce CJK or other non-ASCII characters in
	// fields that should be column names, operators, or identifiers. Reject
	// values under 200 chars that contain non-ASCII runes — real column names
	// and tool parameters are always ASCII.
	if len(trimmed) < 200 {
		for _, r := range trimmed {
			if r > 127 {
				fmt.Fprintf(os.Stderr, "[Executor F1] Rejecting garbled Unicode value %q for field %q\n", trimmed, fieldName)
				return true
			}
		}
	}

	// Red-team FM-10 fix: Field-specific validation for cacheId.
	// The model frequently hallucinates file paths (e.g., "/Users/.../file.csv")
	// as the cacheId instead of the actual cache_NNNN identifier. File paths
	// look like substantive values to the generic placeholder check, so we need
	// an explicit guard. Rejecting here forces Pass 2 GBNF refinement which
	// will extract the correct cacheId from the accumulated context.
	if strings.EqualFold(fieldName, "cacheId") || strings.EqualFold(fieldName, "cacheid") || strings.EqualFold(fieldName, "cache_id") {
		if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
			fmt.Fprintf(os.Stderr, "[Executor F1] FM-10: Rejecting file path %q as %s value\n", trimmed, fieldName)
			return true
		}
		if !strings.HasPrefix(trimmed, "cache_") {
			fmt.Fprintf(os.Stderr, "[Executor F1] FM-10: Rejecting non-cache value %q for %s (expected cache_NNNN)\n", trimmed, fieldName)
			return true
		}
	}

	// Generic placeholder words when they're the entire value
	placeholders := []string{"value", "path", "query", "content", "data", "input", "output", "result", "column", "field", "name", "file"}
	lower := strings.ToLower(trimmed)
	for _, p := range placeholders {
		if lower == p {
			return true
		}
	}

	return false
}

// resolveDynamicBindings resolves a node's DynamicBindings by looking up upstream
// node RawOutput values from the database. Each binding maps a parameter name to
// an upstream path in the format "nodeId.output.propertyName". Returns a map of
// paramName → ResolvedBinding (value + resolution tier) for all successfully
// resolved bindings.
//
// Uses a three-tier resolution cascade (ADR-0029 Response Resolver):
//  1. Recursive key search — parse JSON and walk the tree for an exact key match at any depth
//     1.5. Fuzzy key search — suffix/substring containment on JSON keys
//     1.6. Node-type-aware plain-text fallback — for probe/synthesis/recall nodes whose output is raw text
//  2. KV-line key search — fall back to "key: value" per-line parsing for non-JSON outputs
//  3. Semantic fallback — invoke the Local Model to semantically match the binding key
//
// The returned Tier metadata enables the Proactive Binding Splice (ADR-0030) to
// determine whether each resolved value can bypass inference (high-confidence)
// or should be injected as a prompt hint (low-confidence).

// numericLiteralRegex matches signed integers and decimals in natural language instruction text.
// It requires a word boundary or whitespace before the number to avoid matching inside identifiers.
var numericLiteralRegex = regexp.MustCompile(`(?:^|[\s,:(=])(-?\d+(?:\.\d+)?)(?:[\s,;.!?)\]]|$)`)

// coerceNumericArguments is a deterministic post-extraction validator for numeric tool arguments.
// When the GBNF bridge extracts a 0 value for a numeric argument, but the instruction text
// contains an explicit non-zero numeric literal, this function substitutes the literal value.
// This avoids the need for prompt injection to fix negative number extraction failures.
func coerceNumericArguments(args map[string]interface{}, instruction string) {
	// Extract all numeric literals from the instruction text
	matches := numericLiteralRegex.FindAllStringSubmatch(instruction, -1)
	if len(matches) == 0 {
		return
	}

	var instructionNums []float64
	for _, m := range matches {
		if len(m) >= 2 {
			if num, err := strconv.ParseFloat(m[1], 64); err == nil {
				instructionNums = append(instructionNums, num)
			}
		}
	}

	if len(instructionNums) == 0 {
		return
	}

	for key, val := range args {
		// Only coerce numeric arguments
		numVal, isNum := val.(float64)
		if !isNum {
			if intVal, ok := val.(int); ok {
				numVal = float64(intVal)
				isNum = true
			} else if int64Val, ok := val.(int64); ok {
				numVal = float64(int64Val)
				isNum = true
			} else if float32Val, ok := val.(float32); ok {
				numVal = float64(float32Val)
				isNum = true
			}
		}
		if !isNum {
			continue
		}

		// Search for a numeric literal in the instruction that contextually relates to this key.
		// Strategy: look for the key name (or a word-boundary variant) near a numeric literal.
		keyLower := strings.ToLower(key)
		keyWords := strings.Split(strings.ReplaceAll(keyLower, "_", " "), " ")

		bestNum := 0.0
		bestDist := len(instruction) + 1

		for _, num := range instructionNums {
			if num == 0 {
				continue // Skip zero literals — they wouldn't correct a zero extraction
			}

			// Find the position of this literal in the instruction
			numStr := strconv.FormatFloat(num, 'f', -1, 64)
			numIdx := strings.Index(instruction, numStr)
			if numIdx == -1 {
				continue
			}

			// Check proximity of any key word to this literal
			for _, word := range keyWords {
				if len(word) < 3 {
					continue // Skip very short words to avoid false matches
				}
				wordIdx := strings.Index(strings.ToLower(instruction), word)
				if wordIdx == -1 {
					continue
				}
				dist := numIdx - wordIdx
				if dist < 0 {
					dist = -dist
				}
				if dist < bestDist {
					bestDist = dist
					bestNum = num
				}
			}
		}

		// Apply coercion if we found a contextually relevant non-zero literal
		// within a reasonable proximity (200 chars accounts for natural language phrasing).
		// We coerce if the current value is 0 OR if there is a sign mismatch (e.g. positive vs negative).
		if bestNum != 0 && bestDist < 200 {
			signMismatch := (numVal > 0 && bestNum < 0) || (numVal < 0 && bestNum > 0)
			if numVal == 0 || signMismatch {
				fmt.Fprintf(os.Stderr, "[Executor Coercion] Correcting argument '%s': %v -> %v (from instruction literal)\n", key, numVal, bestNum)
				args[key] = bestNum
			}
		}
	}
}

// labeledQuotedRegex matches labeled key-value patterns where the value is quoted.
// Patterns: "key: 'value'", "key: \"value\""
var labeledQuotedRegex = regexp.MustCompile(`(?i)(\w[\w\s]*?)\s*[:=]\s*["']([^"'\n]+?)["']`)

// labeledUnquotedRegex matches labeled key-value patterns with unquoted values.
// Patterns: "key: value", "key = value" (value ends at punctuation/comma/newline)
var labeledUnquotedRegex = regexp.MustCompile(`(?i)(\w[\w\s]*?)\s*[:=]\s*([^\s"',;.!?\n][^,;.!?\n]*?)(?:[,;.!?\s]|$)`)

// identifierRegex matches structured identifiers (IDs, codes, emails, version tags)
// that are likely tool argument values rather than natural language.
var identifierRegex = regexp.MustCompile(`(?:^|[\s:=])([A-Z][A-Z0-9_-]{2,}(?:-[A-Za-z0-9]+)*|[a-z][a-z0-9_.+-]+@[a-z0-9.-]+\.[a-z]{2,}|v\d+\.\d+(?:\.\d+)?|#[\w-]+|[a-z]+_[a-z0-9_]+_\d+)(?:[\s,;.!?)]|$)`)

// coerceStringArguments is a deterministic post-extraction validator for string tool arguments.
// When the GBNF bridge extracts an empty string or a hallucinated value for a string argument,
// but the instruction text contains an explicit string value near the argument key name,
// this function substitutes the instruction literal. This mirrors coerceNumericArguments
// but for the string domain, and is the primary fix for the 79% parameter mismatch failures
// discovered in the 2026-05-30 11:32 100-case benchmark.

func coerceStringArguments(args map[string]interface{}, instruction string, toolName string) {
	if len(args) == 0 || instruction == "" {
		return
	}

	// Retrieve tool schema to know expected argument types
	var schemaProps map[string]interface{}
	if schemaStr, err := tools.GetSchema(toolName); err == nil && schemaStr != "" {
		var schemaMap map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schemaMap) == nil {
			if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
				if toolArgs, ok := props["tool_arguments"].(map[string]interface{}); ok {
					if taProps, ok := toolArgs["properties"].(map[string]interface{}); ok {
						schemaProps = taProps
					}
				} else {
					schemaProps = props
				}
			}
		}
	}

	// Extract all labeled value pairs from the instruction using both quoted and unquoted patterns
	type labeledValue struct {
		key   string
		value string
		pos   int
	}
	var labeledPairs []labeledValue

	// Quoted values: key: "value" or key: 'value'
	for _, m := range labeledQuotedRegex.FindAllStringSubmatch(instruction, -1) {
		if len(m) >= 3 {
			key := strings.TrimSpace(m[1])
			val := strings.TrimSpace(m[2])
			if val != "" && len(val) > 1 {
				pos := strings.Index(instruction, m[0])
				labeledPairs = append(labeledPairs, labeledValue{key: key, value: val, pos: pos})
			}
		}
	}
	// Unquoted values: key: value or key = value
	for _, m := range labeledUnquotedRegex.FindAllStringSubmatch(instruction, -1) {
		if len(m) >= 3 {
			key := strings.TrimSpace(m[1])
			val := strings.TrimSpace(m[2])
			if val != "" && len(val) > 1 {
				pos := strings.Index(instruction, m[0])
				labeledPairs = append(labeledPairs, labeledValue{key: key, value: val, pos: pos})
			}
		}
	}

	// Extract identifiers from instruction (IDs, emails, codes, tags)
	idMatches := identifierRegex.FindAllStringSubmatch(instruction, -1)
	var identifiers []struct {
		value string
		pos   int
	}
	for _, m := range idMatches {
		if len(m) >= 2 {
			val := strings.TrimSpace(m[1])
			pos := strings.Index(instruction, val)
			identifiers = append(identifiers, struct {
				value string
				pos   int
			}{value: val, pos: pos})
		}
	}

	instructionLower := strings.ToLower(instruction)

	for key, val := range args {
		// sql_cached_data.sql arguments are already validated by GBNF Pass 2.
		// StringCoercion's fuzzy matching treats valid SQL as "hallucinated"
		// and truncates it to a single keyword (e.g., "FROM"), destroying the query.
		if toolName == "sql_cached_data" && key == "sql" {
			continue
		}

		// cacheId arguments with cache_ prefix are dynamically generated identifiers
		// (e.g., "cache_1785202015624") that are never present in the original
		// instruction text. StringCoercion would treat them as "hallucinated"
		// and corrupt them. Bypass unconditionally when the prefix matches.
		if key == "cacheId" {
			if strVal, ok := val.(string); ok && strings.HasPrefix(strVal, "cache_") {
				continue
			}
		}

		// Only coerce string arguments
		strVal, isStr := val.(string)
		if !isStr {
			continue
		}

		// Check if schema expects a string type for this key
		if schemaProps != nil {
			if prop, ok := schemaProps[key].(map[string]interface{}); ok {
				if propType, ok := prop["type"].(string); ok && propType != "string" {
					continue
				}
			}
		}

		// Only coerce if the value is empty OR not found anywhere in the instruction
		valLower := strings.ToLower(strVal)
		isEmpty := strVal == "" || strVal == "null" || strVal == "undefined"
		isHallucinated := !isEmpty && !strings.Contains(instructionLower, valLower)

		if !isEmpty && !isHallucinated {
			continue
		}

		// Protection: If the value looks like a valid path or identifier, don't coerce it
		// even if it's "hallucinated" (i.e. not in the prose instruction)
		if !isEmpty && (strings.Contains(strVal, "/") || strings.Contains(strVal, "\\") || strings.HasSuffix(strVal, ".md") || strings.HasSuffix(strVal, ".go")) {
			continue
		}

		keyLower := strings.ToLower(key)
		keyWords := strings.Split(strings.ReplaceAll(keyLower, "_", " "), " ")

		// Strategy 1: Check labeled pairs for key-name proximity match
		bestValue := ""
		bestScore := -1

		for _, lp := range labeledPairs {
			lpKeyLower := strings.ToLower(lp.key)
			score := 0

			// Direct key match
			if lpKeyLower == keyLower || strings.ReplaceAll(lpKeyLower, " ", "_") == keyLower {
				score = 100
			} else {
				// Partial key word match
				for _, kw := range keyWords {
					if len(kw) >= 3 && strings.Contains(lpKeyLower, kw) {
						score += 30
					}
				}
			}

			if score > bestScore {
				bestScore = score
				bestValue = lp.value
			}
		}

		// Strategy 2: If no labeled match, try identifier proximity
		if bestScore < 30 {
			for _, word := range keyWords {
				if len(word) < 3 {
					continue
				}
				keyIdx := strings.Index(instructionLower, word)
				if keyIdx == -1 {
					continue
				}
				for _, id := range identifiers {
					dist := id.pos - keyIdx
					if dist < 0 {
						dist = -dist
					}
					if dist < 150 {
						proximityScore := 150 - dist
						if proximityScore > bestScore {
							bestScore = proximityScore
							bestValue = id.value
						}
					}
				}
			}
		}

		if bestValue != "" && bestScore >= 30 {
			if isEmpty {
				fmt.Fprintf(os.Stderr, "[Executor StringCoercion] Filling empty argument '%s': '' -> %q (score: %d)\n", key, bestValue, bestScore)
			} else {
				fmt.Fprintf(os.Stderr, "[Executor StringCoercion] Correcting hallucinated argument '%s': %q -> %q (score: %d)\n", key, strVal, bestValue, bestScore)
			}
			args[key] = bestValue
		}
	}
}

// GetNodeStateTolerant retrieves node state by attempting direct lookup,
// followed by suffix fallback mappings (_exec, _bridge) to remain robust
// against SCT graph expansions.

