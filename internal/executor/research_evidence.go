package executor

import (
	"fmt"
	"strings"
	"tzro/internal/memory"
)

// isWebResearchTool returns true if the tool name corresponds to web search or browsing.
func isWebResearchTool(toolName string) bool {
	switch strings.ToLower(toolName) {
	case "web_search", "web_browse", "fetch_web_page", "fetch_url", "browse":
		return true
	default:
		return false
	}
}

// extractResearchEvidence extracts uncompacted, high-fidelity web search and browsing outputs
// from SQLite thought steps to prevent loss of source URLs and specific figures during synthesis.
func extractResearchEvidence(probeID string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 12288
	}

	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil || len(steps) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, s := range steps {
		if !isWebResearchTool(s.ToolName) || s.ToolOutput == "" {
			continue
		}
		// Skip tool errors
		if strings.HasPrefix(s.ToolOutput, "Error:") || strings.HasPrefix(s.ToolOutput, "error:") {
			continue
		}

		sb.WriteString(fmt.Sprintf("### Source Evidence (Step %d - %s):\nQuery/Args: %s\nFindings/Output:\n%s\n\n",
			s.StepIndex, s.ToolName, s.ToolArgs, s.ToolOutput))
	}

	if sb.Len() == 0 {
		return ""
	}

	res := sb.String()
	if len(res) > maxChars {
		res = res[:maxChars] + "\n[... additional web search evidence truncated ...]\n"
	}

	return res
}
