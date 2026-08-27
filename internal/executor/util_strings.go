package executor

import (
	"encoding/json"
	"regexp"
	"strings"

	cfgpkg "tzro/internal/config"
)

// truncate shortens a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// parseActionFromResponse parses <ACTION>tool_name(args)</ACTION> or <ACTION>{"tool":"...", ...}</ACTION> or signals synthesis readiness.
func parseActionFromResponse(response string) (action, toolName string, args map[string]interface{}) {
	if strings.Contains(response, "<SYNTHESIZE_READY>") {
		return "synthesize", "", nil
	}
	// Check for <ACTION>{"tool": "...", "arguments": {...}}</ACTION>
	jsonRe := regexp.MustCompile(`(?s)<ACTION>\s*(\{.*?\})\s*</ACTION>`)
	if m := jsonRe.FindStringSubmatch(response); len(m) == 2 {
		var parsed struct {
			Tool      string                 `json:"tool"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if json.Unmarshal([]byte(m[1]), &parsed) == nil && parsed.Tool != "" {
			if parsed.Arguments == nil {
				parsed.Arguments = make(map[string]interface{})
			}
			return "tool_call", parsed.Tool, parsed.Arguments
		}
	}
	// Check for <ACTION>tool_name(args)</ACTION>
	re := regexp.MustCompile(`(?s)<ACTION>\s*([a-zA-Z0-9_-]+)\((.*?)\)\s*</ACTION>`)
	if m := re.FindStringSubmatch(response); len(m) == 3 {
		tool := m[1]
		rawArgs := strings.TrimSpace(m[2])
		var parsedArgs map[string]interface{}
		if rawArgs != "" {
			_ = json.Unmarshal([]byte(rawArgs), &parsedArgs)
		}
		if parsedArgs == nil {
			parsedArgs = make(map[string]interface{})
		}
		return "tool_call", tool, parsedArgs
	}
	return "synthesize", "", nil
}

// hybridSynthesisThreshold returns the configured context size (in chars) above
// which synthesis uses a two-phase approach: local outline + cloud polish.
// Reads from config.json ("hybridSynthesisThresholdChars"), defaults to 50000.
func hybridSynthesisThreshold() int {
	return cfgpkg.GetHybridSynthesisThresholdChars()
}
