package laya

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type StateAssembler struct {
	MaxTokens int
}

func NewStateAssembler(maxTokens int) *StateAssembler {
	if maxTokens <= 0 {
		maxTokens = 450
	}
	return &StateAssembler{MaxTokens: maxTokens}
}

func EstimateTokens(text string) int {
	return int(math.Ceil(float64(len(text)) / 3.8))
}

func (sa *StateAssembler) CompactState(rawState map[string]interface{}) (map[string]interface{}, int, error) {
	state := deepCopyMap(rawState)

	compactMap(state)

	tokens := estimateMapTokens(state)
	if tokens <= sa.MaxTokens {
		return state, tokens, nil
	}

	strictCompactMap(state)

	tokens = estimateMapTokens(state)
	if tokens > sa.MaxTokens {
		return state, tokens, fmt.Errorf("assertion failed: estimated tokens %d > %d", tokens, sa.MaxTokens)
	}

	return state, tokens, nil
}

func estimateMapTokens(m map[string]interface{}) int {
	b, _ := json.Marshal(m)
	return EstimateTokens(string(b))
}

func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	b, _ := json.Marshal(m)
	var res map[string]interface{}
	json.Unmarshal(b, &res)
	return res
}

func compactMap(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if k == "diagnostic" || k == "log" {
				m[k] = compactLog(val, 10, 5, 3)
			} else if len(val) > 500 {
				m[k] = val[:500] + "..."
			}
		case []interface{}:
			if k == "candidates" || k == "files" {
				if len(val) > 5 {
					m[k] = val[:5]
				}
			}
			for _, item := range val {
				if childMap, ok := item.(map[string]interface{}); ok {
					compactMap(childMap)
				}
			}
		case map[string]interface{}:
			compactMap(val)
		}
	}
}

func strictCompactMap(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			val = stripDocstrings(val)
			if len(val) > 200 {
				val = val[:200]
			}
			m[k] = val
		case []interface{}:
			if k == "candidates" {
				if len(val) > 3 {
					m[k] = val[:3]
				}
			}
			for _, item := range val {
				if childMap, ok := item.(map[string]interface{}); ok {
					strictCompactMap(childMap)
				}
			}
		case map[string]interface{}:
			strictCompactMap(val)
		}
	}
}

func compactLog(s string, maxLines, keepFirst, keepLast int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	res := append([]string{}, lines[:keepFirst]...)
	res = append(res, fmt.Sprintf("[... %d lines elided ...]", len(lines)-keepFirst-keepLast))
	res = append(res, lines[len(lines)-keepLast:]...)
	return strings.Join(res, "\n")
}

func stripDocstrings(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
