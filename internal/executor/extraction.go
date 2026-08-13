package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// stripSchemaProperties removes the named properties from a JSON tool schema string,
// returning the modified schema. Used by the Proactive Binding Splice (ADR-0030) to
// prevent the model from generating values that are already deterministically known.
// If parsing or modification fails, returns the original schema unchanged.
func stripSchemaProperties(schemaStr string, keysToStrip []string) string {
	if len(keysToStrip) == 0 || schemaStr == "" {
		return schemaStr
	}

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		return schemaStr
	}

	// Build a set for O(1) lookup
	stripSet := make(map[string]bool, len(keysToStrip))
	for _, k := range keysToStrip {
		stripSet[k] = true
	}

	// Navigate to tool_arguments.properties (standard schema structure)
	toolArgs, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return schemaStr
	}
	toolArgsObj, ok := toolArgs["tool_arguments"].(map[string]interface{})
	if !ok {
		// Try flat schema (no tool_arguments wrapper)
		toolArgsObj = schema
	}

	props, ok := toolArgsObj["properties"].(map[string]interface{})
	if !ok {
		return schemaStr
	}

	// Remove properties
	for _, key := range keysToStrip {
		delete(props, key)
	}

	// Remove from required array if present
	if reqRaw, ok := toolArgsObj["required"].([]interface{}); ok {
		filtered := make([]interface{}, 0, len(reqRaw))
		for _, r := range reqRaw {
			if rStr, ok := r.(string); ok && !stripSet[rStr] {
				filtered = append(filtered, r)
			}
		}
		toolArgsObj["required"] = filtered
	}

	modified, err := json.Marshal(schema)
	if err != nil {
		return schemaStr
	}
	return string(modified)
}

// templatePattern matches unresolved template literals like "{path}", "{query}", "{value}".
var templatePattern = regexp.MustCompile(`^\{[a-zA-Z_]+\}/?$`)

// pass1SatisfiesSchema performs a deterministic structural check on Pass 1
// extracted arguments against the tool schema. Returns true when all required
// fields are present with non-placeholder values — meaning Pass 2 GBNF
// refinement can be safely skipped.
//
// Placeholder detection rejects:
//   - Empty strings
//   - Values that equal their own field name (model echoed the key)
//   - Unresolved {template} patterns like "{path}/" or "{query}"
//
// This prevents Pass 2 from clobbering correct parameters with garbled
// re-extractions (observed: cloud-extracted paths overwritten with "{path}/"
// templates by the 4B router model).

func pass1SatisfiesSchema(args map[string]interface{}, schemaStr string, nodeID string) bool {
	// Parse the schema to extract required fields
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		return false
	}

	// Navigate to the tool_arguments properties and required fields
	toolArgs, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return false
	}
	toolArgsSchema, ok := toolArgs["tool_arguments"].(map[string]interface{})
	if !ok {
		// Flat schema (no tool_arguments wrapper)
		toolArgsSchema = schema
	}

	// Get required fields
	requiredRaw, _ := toolArgsSchema["required"].([]interface{})
	if len(requiredRaw) == 0 {
		// No required fields — nothing to validate against, skip Pass 2
		return true
	}

	// Check each required field is present with a substantive value
	for _, r := range requiredRaw {
		fieldName, ok := r.(string)
		if !ok {
			continue
		}

		val, exists := args[fieldName]
		if !exists {
			fmt.Fprintf(os.Stderr, "[Executor F1] Pass 1 schema check: required field %q missing for %s\n", fieldName, nodeID)
			return false
		}

		// Check the value is substantive (not a placeholder)
		strVal := fmt.Sprintf("%v", val)
		if isPlaceholderValue(strVal, fieldName) {
			fmt.Fprintf(os.Stderr, "[Executor F1] Pass 1 schema check: required field %q has placeholder value %q for %s\n", fieldName, strVal, nodeID)
			return false
		}
	}

	return true
}

// isPlaceholderValue returns true if the value looks like a placeholder rather
// than a real extracted parameter. Checks for empty strings, field name echoes,
// unresolved template patterns, and garbled Unicode output.

func extractToolArguments(raw string) map[string]interface{} {
	// Try JSON parsing first
	startIdx := strings.Index(raw, "{")
	endIdx := strings.LastIndex(raw, "}")
	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(raw[startIdx:endIdx+1]), &parsed) == nil {
			// Recursively unwrap tool_arguments nesting to handle double/triple wrapping
			// caused by bridge GBNF schema + exec node interpolation
			for {
				args, ok := parsed["tool_arguments"].(map[string]interface{})
				if !ok {
					break
				}
				parsed = args
			}
			return parsed
		}
	}

	// Try XML parsing: extract <key>value</key> pairs from the raw output.
	// This handles semantic_validator XML output when GBNF refinement is unavailable.
	xmlArgs := extractXMLToolArguments(raw)
	if len(xmlArgs) > 0 {
		return xmlArgs
	}

	return map[string]interface{}{"query": raw}
}

// xmlArgRegex matches simple XML tag pairs: <tagName>value</tagName>
// It captures the tag name and inner text value for flat key-value extraction.
var xmlArgRegex = regexp.MustCompile(`<(\w+)>([^<]*)</\w+>`)

// extractXMLToolArguments parses flat XML key-value pairs from a raw string.
// It focuses on the content inside <tool_arguments>...</tool_arguments> if present,
// falling back to matching any <key>value</key> pairs in the full string.
// Values are type-coerced: "true"/"false" → bool, numeric strings → float64.

func extractXMLToolArguments(raw string) map[string]interface{} {
	// Narrow scope to innermost wrapper block: try <params>, then <tool_arguments>
	searchStr := raw

	// Try <params> first (the current instruction format)
	if pStart := strings.LastIndex(raw, "<params>"); pStart != -1 {
		if pEnd := strings.Index(raw[pStart:], "</params>"); pEnd != -1 {
			searchStr = raw[pStart+len("<params>") : pStart+pEnd]
		}
	} else if taStart := strings.LastIndex(raw, "<tool_arguments>"); taStart != -1 {
		// Fall back to <tool_arguments> (legacy format)
		if taEnd := strings.Index(raw[taStart:], "</tool_arguments>"); taEnd != -1 {
			searchStr = raw[taStart+len("<tool_arguments>") : taStart+taEnd]
		}
	}

	matches := xmlArgRegex.FindAllStringSubmatch(searchStr, -1)
	if len(matches) == 0 {
		return nil
	}

	args := make(map[string]interface{})
	for _, m := range matches {
		if len(m) >= 3 {
			key := m[1]
			val := strings.TrimSpace(m[2])

			// Skip structural tags like <tool_arguments> or <params> itself
			if key == "tool_arguments" || key == "tool" || key == "args" || key == "params" {
				continue
			}

			// Type coercion
			if val == "true" {
				args[key] = true
			} else if val == "false" {
				args[key] = false
			} else if num, err := strconv.ParseFloat(val, 64); err == nil {
				// Preserve integers as integers for cleaner JSON
				if num == float64(int64(num)) {
					args[key] = int64(num)
				} else {
					args[key] = num
				}
			} else {
				args[key] = val
			}
		}
	}

	if len(args) == 0 {
		return nil
	}
	return args
}

