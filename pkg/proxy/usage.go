package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// TokenUsage tracks empirical measured token metrics vs estimates.
type TokenUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`      // nil represents "unknown"
	CompletionTokens *int64 `json:"completion_tokens"`  // nil represents "unknown"
	TotalTokens      *int64 `json:"total_tokens"`       // nil represents "unknown"
	NativeCacheRead  *int64 `json:"native_cache_read"`  // Provider-native cache read hit
	TzroPrefixLocked *int64 `json:"tzro_prefix_locked"` // Tzro-attributed prefix lock gain
	IsMeasured       bool   `json:"is_measured"`        // True if observed from provider payload
}

// ExtractUsageFromJSON extracts usage stats from a complete JSON response body.
func ExtractUsageFromJSON(body []byte) (TokenUsage, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenUsage{IsMeasured: false}, false
	}

	u := TokenUsage{IsMeasured: false}

	// 1. OpenAI / Responses / Local format: "usage": { "prompt_tokens": X, "completion_tokens": Y, "total_tokens": Z, "prompt_tokens_details": {"cached_tokens": C} }
	if usageRaw, ok := payload["usage"].(map[string]any); ok {
		u.IsMeasured = true
		if pt, ok := getInt64(usageRaw, "prompt_tokens"); ok {
			u.PromptTokens = &pt
		} else if it, ok := getInt64(usageRaw, "input_tokens"); ok {
			u.PromptTokens = &it
		}

		if ct, ok := getInt64(usageRaw, "completion_tokens"); ok {
			u.CompletionTokens = &ct
		} else if ot, ok := getInt64(usageRaw, "output_tokens"); ok {
			u.CompletionTokens = &ot
		}

		if tt, ok := getInt64(usageRaw, "total_tokens"); ok {
			u.TotalTokens = &tt
		}

		if ptd, ok := usageRaw["prompt_tokens_details"].(map[string]any); ok {
			if cr, ok := getInt64(ptd, "cached_tokens"); ok {
				u.NativeCacheRead = &cr
			}
		}

		// Anthropic cache_read_input_tokens / cache_creation_input_tokens
		if cr, ok := getInt64(usageRaw, "cache_read_input_tokens"); ok {
			u.NativeCacheRead = &cr
		}
		if tzroLocked, ok := getInt64(usageRaw, "tzro_prefix_locked_tokens"); ok {
			u.TzroPrefixLocked = &tzroLocked
		}
		return u, true
	}

	// 2. Gemini format: "usageMetadata": { "promptTokenCount": X, "candidatesTokenCount": Y, "totalTokenCount": Z, "cachedContentTokenCount": C }
	if metaRaw, ok := payload["usageMetadata"].(map[string]any); ok {
		u.IsMeasured = true
		if pt, ok := getInt64(metaRaw, "promptTokenCount"); ok {
			u.PromptTokens = &pt
		}
		if ct, ok := getInt64(metaRaw, "candidatesTokenCount"); ok {
			u.CompletionTokens = &ct
		}
		if tt, ok := getInt64(metaRaw, "totalTokenCount"); ok {
			u.TotalTokens = &tt
		}
		if cr, ok := getInt64(metaRaw, "cachedContentTokenCount"); ok {
			u.NativeCacheRead = &cr
		}
		return u, true
	}

	return u, false
}

// ExtractUsageFromSSE inspects an SSE stream chunk or buffer and extracts usage if present.
func ExtractUsageFromSSE(chunk []byte) (TokenUsage, bool) {
	lines := bytes.Split(chunk, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("data:")) {
			data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
			if bytes.Equal(data, []byte("[DONE]")) {
				continue
			}
			if u, ok := ExtractUsageFromJSON(data); ok && u.IsMeasured {
				return u, true
			}
			// Anthropic SSE message_delta with usage
			var event map[string]any
			if err := json.Unmarshal(data, &event); err == nil {
				if deltaUsage, ok := event["usage"].(map[string]any); ok {
					var u TokenUsage
					u.IsMeasured = true
					if ot, ok := getInt64(deltaUsage, "output_tokens"); ok {
						u.CompletionTokens = &ot
					}
					if it, ok := getInt64(deltaUsage, "input_tokens"); ok {
						u.PromptTokens = &it
					}
					if cr, ok := getInt64(deltaUsage, "cache_read_input_tokens"); ok {
						u.NativeCacheRead = &cr
					}
					return u, true
				}
			}
		}
	}
	return TokenUsage{IsMeasured: false}, false
}

func getInt64(m map[string]any, key string) (int64, bool) {
	val, exists := m[key]
	if !exists || val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
	}
	return 0, false
}

// FormatUsageDisplay outputs a human-readable display of measured vs unknown fields.
func (u TokenUsage) FormatUsageDisplay() string {
	var sb strings.Builder
	formatVal := func(p *int64) string {
		if p == nil {
			return "unknown"
		}
		return strings.TrimSpace(string(jsonNumber(*p)))
	}

	sb.WriteString("Token Metrics (Measured):\n")
	sb.WriteString("  Input Tokens:        " + formatVal(u.PromptTokens) + "\n")
	sb.WriteString("  Output Tokens:       " + formatVal(u.CompletionTokens) + "\n")
	sb.WriteString("  Total Tokens:        " + formatVal(u.TotalTokens) + "\n")
	sb.WriteString("  Native Cache Reads:  " + formatVal(u.NativeCacheRead) + "\n")
	sb.WriteString("  Tzro Prefix Locked:  " + formatVal(u.TzroPrefixLocked) + "\n")
	return sb.String()
}

func jsonNumber(n int64) []byte {
	b, _ := json.Marshal(n)
	return b
}
