package matcher

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"tzro/internal/tools"
)

type RelaxationPolicy struct {
	AllowedFilenames   []string
	StopWords          []string
	NumericEquivalence bool
	TimeParsingFormats []string
}

func DefaultRelaxationPolicy() RelaxationPolicy {
	return RelaxationPolicy{
		AllowedFilenames:   []string{"final_report.pdf", "log.txt", "current_directory"},
		StopWords:          []string{"the", "a", "an", "please", "now", "to", "for", "in", "of", "and", "under", "on", "at", "by", "with"},
		NumericEquivalence: true,
		TimeParsingFormats: []string{
			"02/01/2006 15:04",
			"02/01/2006",
			"2006-01-02 15:04:05",
			"2006-01-02 15:04",
			"2006-01-02",
			"15:04:05",
			"15:04",
		},
	}
}

func ToFloat64(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func normalizeFuzzy(s string, policy RelaxationPolicy) string {
	s = strings.ToLower(s)
	// Replace punctuation and common special chars with spaces
	puncs := []string{".", ",", "!", "?", "'", "\"", "`", "-", "(", ")", "[", "]", "{", "}", ":", ";"}
	for _, p := range puncs {
		s = strings.ReplaceAll(s, p, " ")
	}
	// Strip standard helper / stop words
	words := strings.Fields(s)
	var filtered []string
	for _, w := range words {
		isStop := false
		for _, stop := range policy.StopWords {
			if w == stop {
				isStop = true
				break
			}
		}
		if !isStop {
			filtered = append(filtered, w)
		}
	}
	return strings.Join(filtered, " ")
}

func tryParseTime(s string, policy RelaxationPolicy) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range policy.TimeParsingFormats {
		t, err := time.Parse(l, s)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func standardizeStringOfficial(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "'", "\"")
	var sb strings.Builder
	for _, r := range s {
		if r == ' ' || r == ',' || r == '.' || r == '/' || r == '-' || r == '_' || r == '*' || r == '^' {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func toInterfaceSlice(val interface{}) ([]interface{}, bool) {
	v := reflect.ValueOf(val)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return nil, false
	}
	l := v.Len()
	res := make([]interface{}, l)
	for i := 0; i < l; i++ {
		res[i] = v.Index(i).Interface()
	}
	return res, true
}

func CheckRelaxation(userMessage string, expectedVal, actualVal interface{}, policy RelaxationPolicy) bool {
	// If expectedVal is a slice of acceptable alternatives, match if actualVal matches ANY option
	if opts, ok := expectedVal.([]interface{}); ok {
		for _, opt := range opts {
			if checkRelaxationSingle(userMessage, opt, actualVal, policy) {
				return true
			}
		}
		return false
	}
	if opts, ok := expectedVal.([]string); ok {
		for _, opt := range opts {
			if checkRelaxationSingle(userMessage, opt, actualVal, policy) {
				return true
			}
		}
		return false
	}
	return checkRelaxationSingle(userMessage, expectedVal, actualVal, policy)
}

func checkRelaxationSingle(userMessage string, expectedVal, actualVal interface{}, policy RelaxationPolicy) bool {
	// 1. Recursive map validation
	expMap, expIsMap := expectedVal.(map[string]interface{})
	actMap, actIsMap := actualVal.(map[string]interface{})
	if expIsMap && actIsMap {
		allMatch := true
		for k, expV := range expMap {
			actV, exists := actMap[k]
			if !exists {
				// Check if expV is optional (e.g. nil or empty string is accepted)
				isOptional := false
				if opts, ok := expV.([]interface{}); ok {
					for _, opt := range opts {
						if s, ok := opt.(string); ok && s == "" {
							isOptional = true
							break
						}
					}
				} else if opts, ok := expV.([]string); ok {
					for _, opt := range opts {
						if opt == "" {
							isOptional = true
							break
						}
					}
				}
				if isOptional {
					continue
				}
				allMatch = false
				break
			}
			if !checkRelaxationSingle(userMessage, expV, actV, policy) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}

	// 2. Index-aligned slice-to-slice matching
	expSlice, expIsSlice := toInterfaceSlice(expectedVal)
	actSlice, actIsSlice := toInterfaceSlice(actualVal)
	if expIsSlice && actIsSlice {
		if len(expSlice) == len(actSlice) {
			allMatch := true
			for idx, expItem := range expSlice {
				if !checkRelaxationSingle(userMessage, expItem, actSlice[idx], policy) {
					allMatch = false
					break
				}
			}
			if allMatch {
				return true
			}
		}
	} else if expIsSlice && !actIsSlice && len(expSlice) == 1 {
		if checkRelaxationSingle(userMessage, expSlice[0], actualVal, policy) {
			return true
		}
	} else if actIsSlice && !expIsSlice && len(actSlice) == 1 {
		if checkRelaxationSingle(userMessage, expectedVal, actSlice[0], policy) {
			return true
		}
	}

	// 3. If they are exactly equal, return true
	if reflect.DeepEqual(expectedVal, actualVal) {
		return true
	}

	// 4. Numerical equivalence check
	if policy.NumericEquivalence {
		expNum, expIsNum := ToFloat64(expectedVal)
		actNum, actIsNum := ToFloat64(actualVal)
		if expIsNum && actIsNum && expNum == actNum {
			return true
		}
	}

	// 5. Date-Time equivalence check
	expectedStrRaw := strings.TrimSpace(fmt.Sprintf("%v", expectedVal))
	actualStrRaw := strings.TrimSpace(fmt.Sprintf("%v", actualVal))
	if tExp, okExp := tryParseTime(expectedStrRaw, policy); okExp {
		if tAct, okAct := tryParseTime(actualStrRaw, policy); okAct {
			if tExp.Equal(tAct) {
				return true
			}
		}
	}

	// 6. String-based matching with relaxation
	expectedStr := strings.ToLower(expectedStrRaw)
	actualStr := strings.ToLower(actualStrRaw)
	if expectedStr == actualStr {
		return true
	}

	if standardizeStringOfficial(expectedStrRaw) == standardizeStringOfficial(actualStrRaw) {
		return true
	}

	// Helper to check if a single clean string is in the user message or expected string
	checkSingleString := func(s string) bool {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			return true
		}
		// Dynamic Allowed Filenames / Stop Words relaxation
		isInAllowedFiles := false
		for _, filename := range policy.AllowedFilenames {
			if s == filename || strings.Contains(s, filename) {
				isInAllowedFiles = true
				break
			}
		}

		if strings.Contains(strings.ToLower(userMessage), s) || isInAllowedFiles {
			return true
		}
		// Fuzzy normalized relaxation
		normActual := normalizeFuzzy(s, policy)
		normExpected := normalizeFuzzy(expectedStr, policy)
		normUserMsg := normalizeFuzzy(userMessage, policy)
		if strings.Contains(normUserMsg, normActual) || strings.Contains(normExpected, normActual) || strings.Contains(normActual, normExpected) {
			return true
		}
		// Official standardized check (highly lenient variable & enum key comparisons)
		if standardizeStringOfficial(s) == standardizeStringOfficial(expectedStrRaw) ||
			strings.Contains(standardizeStringOfficial(expectedStrRaw), standardizeStringOfficial(s)) ||
			strings.Contains(standardizeStringOfficial(s), standardizeStringOfficial(expectedStrRaw)) {
			return true
		}
		return false
	}

	// 7. Handle slices (arrays) containment checks (as fallback)
	switch val := actualVal.(type) {
	case []interface{}:
		allContained := true
		for _, item := range val {
			itemStr := fmt.Sprintf("%v", item)
			if !checkSingleString(itemStr) {
				allContained = false
				break
			}
		}
		if allContained && len(val) > 0 {
			return true
		}
	case []string:
		allContained := true
		for _, item := range val {
			if !checkSingleString(item) {
				allContained = false
				break
			}
		}
		if allContained && len(val) > 0 {
			return true
		}
	}

	// Strip brackets as a fallback for slice-like string representations
	cleanActualStr := actualStr
	if strings.HasPrefix(cleanActualStr, "[") && strings.HasSuffix(cleanActualStr, "]") {
		cleanActualStr = cleanActualStr[1 : len(cleanActualStr)-1]
		// Try checking the elements separated by space
		words := strings.Fields(cleanActualStr)
		if len(words) > 0 {
			allWordsContained := true
			for _, w := range words {
				w = strings.Trim(w, "\",' ")
				if w != "" && !checkSingleString(w) {
					allWordsContained = false
					break
				}
			}
			if allWordsContained {
				return true
			}
		}
	}

	// Fallback to checking the single actual string
	return checkSingleString(actualStr)
}

func MatchParameters(toolName string, userMessage string, expectedArgs map[string]interface{}, actualArgs map[string]interface{}, policy RelaxationPolicy) bool {
	// Parse GBNF or raw schema properties for default/optional validation
	var schemaProps map[string]interface{}
	if schemaStr, err := tools.GetSchema(toolName); err == nil && schemaStr != "" {
		var schemaMap map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schemaMap) == nil {
			// Extract properties from wrapped GBNF schema format: properties.tool_arguments.properties
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

	// 1. Loop through expected keys to make sure they exist, or are optional, and values match
	for k, expectedVal := range expectedArgs {
		actualVal, exists := actualArgs[k]
		if !exists {
			// Check if empty string "" is one of the allowed alternatives (optional parameter)
			isOptional := false
			if opts, ok := expectedVal.([]interface{}); ok {
				for _, opt := range opts {
					if s, ok := opt.(string); ok && s == "" {
						isOptional = true
						break
					}
				}
			} else if opts, ok := expectedVal.([]string); ok {
				for _, opt := range opts {
					if opt == "" {
						isOptional = true
						break
					}
				}
			}
			// Symmetrical check: if missing but schema defines a default, treat it as optional
			if !isOptional && schemaProps != nil {
				if prop, ok := schemaProps[k].(map[string]interface{}); ok {
					if _, hasDefault := prop["default"]; hasDefault {
						isOptional = true
					}
				}
			}
			if isOptional {
				continue
			}
			// Missing required parameter
			fmt.Fprintf(os.Stderr, "[matchParameters Failure] Missing required parameter key %q. ExpectedVal: %+v, ActualArgs: %+v\n", k, expectedVal, actualArgs)
			return false
		}

		// Loop through expected alternative options
		matched := false
		if opts, ok := expectedVal.([]interface{}); ok {
			for _, opt := range opts {
				if checkRelaxationSingle(userMessage, opt, actualVal, policy) {
					matched = true
					break
				}
			}
		} else if opts, ok := expectedVal.([]string); ok {
			for _, opt := range opts {
				if checkRelaxationSingle(userMessage, opt, actualVal, policy) {
					matched = true
					break
				}
			}
		} else {
			// Fallback check
			if checkRelaxationSingle(userMessage, expectedVal, actualVal, policy) {
				matched = true
			}
		}

		if !matched {
			fmt.Fprintf(os.Stderr, "[matchParameters Failure] Key %q value mismatch. ExpectedVal: %+v, ActualVal: %+v\n", k, expectedVal, actualVal)
			return false
		}
	}

	// 2. Loop through actual keys to reject any unexpected parameters
	for k, actualVal := range actualArgs {
		if _, exists := expectedArgs[k]; !exists {
			// Tolerate unexpected parameter if it matches schema default or is an optional empty value
			if schemaProps != nil {
				if prop, ok := schemaProps[k].(map[string]interface{}); ok {
					if defaultVal, hasDefault := prop["default"]; hasDefault {
						// Check equality between generated actual value and schema default value
						if reflect.DeepEqual(actualVal, defaultVal) {
							continue
						}
						// Support numeric/casing relaxation for defaults as well
						if policy.NumericEquivalence {
							expNum, expIsNum := ToFloat64(defaultVal)
							actNum, actIsNum := ToFloat64(actualVal)
							if expIsNum && actIsNum && expNum == actNum {
								continue
							}
						}
						// Support string case-insensitive equivalence for defaults
						if strings.ToLower(fmt.Sprintf("%v", actualVal)) == strings.ToLower(fmt.Sprintf("%v", defaultVal)) {
							continue
						}
					}
					// If no default but is empty representation, treat as optional tolerated value
					if actualVal == nil || actualVal == "" {
						continue
					}
					if slice, isSlice := actualVal.([]interface{}); isSlice && len(slice) == 0 {
						continue
					}
					if slice, isSlice := actualVal.([]string); isSlice && len(slice) == 0 {
						continue
					}
					if m, isMap := actualVal.(map[string]interface{}); isMap && len(m) == 0 {
						continue
					}
				}
			}

			// Unexpected parameter generated by the model
			fmt.Fprintf(os.Stderr, "[matchParameters Failure] Unexpected parameter key %q. ExpectedArgs: %+v, ActualArgs: %+v\n", k, expectedArgs, actualArgs)
			return false
		}
	}

	return true
}
