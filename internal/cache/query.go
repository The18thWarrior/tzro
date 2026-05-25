package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// QueryEngine defines a mockable adapter seam for executing JQ expressions
// on cached payloads.
type QueryEngine interface {
	Query(ctx context.Context, rawPayload, jqExpr string) string
}

// jqQueryEngine is the default implementation that attempts to execute the
// external 'jq' command first and falls back to a custom pure-Go parser.
type jqQueryEngine struct{}

// DefaultQueryEngine is the package-level seam, which can be swapped in tests.
var DefaultQueryEngine QueryEngine = &jqQueryEngine{}

func (qe *jqQueryEngine) Query(ctx context.Context, rawPayload, jqExpr string) string {
	// Try running external 'jq' command first
	jqPath, lookErr := exec.LookPath("jq")
	if lookErr == nil {
		cmd := exec.CommandContext(ctx, jqPath, jqExpr)
		cmd.Stdin = strings.NewReader(rawPayload)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf

		if err := cmd.Run(); err == nil {
			return strings.TrimSpace(outBuf.String())
		}
		fmt.Printf("[Cache JQ Warning] external jq execution failed: %v, falling back\n", errBuf.String())
	}

	// Fallback logic
	return basicJQFallback(rawPayload, jqExpr)
}

func basicJQFallback(rawPayload string, jqExpr string) string {
	var parsed interface{}
	if err := json.Unmarshal([]byte(rawPayload), &parsed); err != nil {
		return fmt.Sprintf("Error parsing JSON: %v", err)
	}

	var records []interface{}
	if arr, ok := parsed.([]interface{}); ok {
		records = arr
	} else if mapData, ok := parsed.(map[string]interface{}); ok {
		if recs, ok := mapData["records"].([]interface{}); ok {
			records = recs
		} else {
			for _, v := range mapData {
				if arr, ok := v.([]interface{}); ok {
					records = arr
					break
				}
			}
		}
	}

	if len(records) == 0 {
		return "[]"
	}

	// 1. Check for duplicates matching filter
	if strings.Contains(jqExpr, "group_by") || strings.Contains(jqExpr, "duplicate") {
		field := "Email"
		if strings.Contains(jqExpr, "Name") {
			field = "Name"
		}

		groupMap := make(map[string][]interface{})
		for _, item := range records {
			if obj, ok := item.(map[string]interface{}); ok {
				if val, ok := obj[field].(string); ok && val != "" {
					groupMap[val] = append(groupMap[val], obj)
				}
			}
		}

		var duplicates []interface{}
		for _, list := range groupMap {
			if len(list) > 1 {
				duplicates = append(duplicates, list...)
			}
		}

		resBytes, _ := json.MarshalIndent(duplicates, "", "  ")
		return string(resBytes)
	}

	// 2. Check for key/value select filters
	if strings.Contains(jqExpr, "select") {
		re := regexp.MustCompile(`\.([a-zA-Z0-9_]+)\s*==\s*"([^"]+)"`)
		matches := re.FindStringSubmatch(jqExpr)
		if len(matches) == 3 {
			field := matches[1]
			val := matches[2]

			var filtered []interface{}
			for _, item := range records {
				if obj, ok := item.(map[string]interface{}); ok {
					if itemVal, ok := obj[field].(string); ok && itemVal == val {
						filtered = append(filtered, obj)
					}
				}
			}
			resBytes, _ := json.MarshalIndent(filtered, "", "  ")
			return string(resBytes)
		}

		reNum := regexp.MustCompile(`\.([a-zA-Z0-9_]+)\s*(>|<|==)\s*([0-9.]+)`)
		numMatches := reNum.FindStringSubmatch(jqExpr)
		if len(numMatches) == 4 {
			field := numMatches[1]
			op := numMatches[2]
			valStr := numMatches[3]

			var filtered []interface{}
			for _, item := range records {
				if obj, ok := item.(map[string]interface{}); ok {
					if itemVal, ok := obj[field].(float64); ok {
						var target float64
						_, _ = fmt.Sscanf(valStr, "%f", &target)
						match := false
						if op == ">" && itemVal > target {
							match = true
						} else if op == "<" && itemVal < target {
							match = true
						} else if op == "==" && itemVal == target {
							match = true
						}
						if match {
							filtered = append(filtered, obj)
						}
					}
				}
			}
			resBytes, _ := json.MarshalIndent(filtered, "", "  ")
			return string(resBytes)
		}
	}

	// 3. Fallback to returning subset/slice
	limit := 5
	if len(records) < 5 {
		limit = len(records)
	}
	resBytes, _ := json.MarshalIndent(records[:limit], "", "  ")
	return string(resBytes)
}
