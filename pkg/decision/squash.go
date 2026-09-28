package decision

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// StateSquasher compacts decision state to fit strictly within a token budget.
type StateSquasher struct {
	MaxTokens int
}

// NewStateSquasher creates a squasher with the specified maximum tokens.
// Defaults to 2,048 tokens if non-positive.
func NewStateSquasher(maxTokens int) *StateSquasher {
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	return &StateSquasher{MaxTokens: maxTokens}
}

// EstimateTokens calculates an approximate token count based on byte length.
func EstimateTokens(text string) int {
	return int(math.Ceil(float64(len(text)) / 3.8))
}

// CompactState applies priority budgeting to fit state within MaxTokens.
func (s *StateSquasher) CompactState(rawState map[string]interface{}) (map[string]interface{}, int, error) {
	state := deepCopyMap(rawState)

	compactMap(state)

	tokens := estimateMapTokens(state)
	if tokens <= s.MaxTokens {
		return state, tokens, nil
	}

	strictCompactMap(state)

	tokens = estimateMapTokens(state)
	if tokens > s.MaxTokens {
		// Final fallback: truncate large string fields aggressively
		emergencyCompactMap(state)
		tokens = estimateMapTokens(state)
		if tokens > s.MaxTokens {
			return state, tokens, fmt.Errorf("state squashing failed: estimated tokens %d > %d", tokens, s.MaxTokens)
		}
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
	_ = json.Unmarshal(b, &res)
	return res
}

func compactMap(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if k == "diagnostic" || k == "log" || k == "error" {
				m[k] = compactLog(val, 50, 25, 25)
			} else if len(val) > 2000 {
				m[k] = val[:2000] + "... [truncated]"
			}
		case []interface{}:
			if k == "candidates" || k == "files" {
				if len(val) > 20 {
					m[k] = val[:20]
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
			if k == "diagnostic" || k == "log" || k == "error" {
				m[k] = compactLog(val, 20, 10, 10)
			} else if len(val) > 800 {
				m[k] = val[:800] + "... [truncated]"
			}
		case []interface{}:
			if k == "candidates" || k == "files" {
				if len(val) > 10 {
					m[k] = val[:10]
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

func emergencyCompactMap(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if len(val) > 300 {
				m[k] = val[:300] + "... [elided]"
			}
		case []interface{}:
			if len(val) > 5 {
				m[k] = val[:5]
			}
		}
	}
}

func compactLog(log string, maxLines, head, tail int) string {
	lines := strings.Split(log, "\n")
	if len(lines) <= maxLines {
		return log
	}

	var sb strings.Builder
	for i := 0; i < head && i < len(lines); i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}

	elidedCount := len(lines) - head - tail
	if elidedCount > 0 {
		sb.WriteString(fmt.Sprintf("... [%d lines elided] ...\n", elidedCount))
	}

	startTail := len(lines) - tail
	if startTail < head {
		startTail = head
	}
	for i := startTail; i < len(lines); i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
