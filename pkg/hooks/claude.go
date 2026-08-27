package hooks

import (
	"encoding/json"
	"io"
	"strings"

	"tzro/pkg/compactor"
	"tzro/pkg/store"
)

// ClaudePreToolUseInput represents the input received on stdin from Claude Code PreToolUse hook.
type ClaudePreToolUseInput struct {
	SessionID string         `json:"session_id,omitempty"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// ClaudePreToolUseOutput represents the response sent to Claude Code.
type ClaudePreToolUseOutput struct {
	Decision          string         `json:"decision,omitempty"`           // "allow" | "deny" | "modify"
	Reason            string         `json:"reason,omitempty"`
	ModifiedToolInput map[string]any `json:"modified_tool_input,omitempty"`
}

// ClaudePostToolUseInput represents the input received from Claude Code PostToolUse hook.
type ClaudePostToolUseInput struct {
	SessionID  string         `json:"session_id,omitempty"`
	ToolName   string         `json:"tool_name"`
	ToolInput  map[string]any `json:"tool_input,omitempty"`
	ToolOutput any            `json:"tool_output,omitempty"`
}

// ClaudePostToolUseOutput represents the response returned to Claude Code to compact tool outputs.
type ClaudePostToolUseOutput struct {
	ToolOutput any `json:"tool_output,omitempty"`
}

// HandleClaudePreToolUse processes Claude Code pre-tool execution.
func HandleClaudePreToolUse(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input ClaudePreToolUseInput
	if err := json.Unmarshal(data, &input); err != nil {
		// Fail-open: default to allow
		return json.NewEncoder(w).Encode(ClaudePreToolUseOutput{Decision: "allow"})
	}

	output := ClaudePreToolUseOutput{Decision: "allow"}

	// Optimization: If Bash tool runs raw find/grep, annotate suggestion
	if strings.EqualFold(input.ToolName, "Bash") {
		if cmd, ok := input.ToolInput["command"].(string); ok {
			trimmed := strings.TrimSpace(cmd)
			if strings.HasPrefix(trimmed, "grep ") || strings.HasPrefix(trimmed, "find ") {
				output.Reason = "Tzro Token Shield active: Consider using `tzro probe` for sub-millisecond AST discovery."
			}
		}
	}

	return json.NewEncoder(w).Encode(output)
}

// HandleClaudePostToolUse processes Claude Code post-tool outputs and compacts verbose output.
func HandleClaudePostToolUse(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input ClaudePostToolUseInput
	if err := json.Unmarshal(data, &input); err != nil {
		// If raw string instead of JSON, compact directly
		compacted := compactor.CompactLog(string(data))
		_, err := w.Write([]byte(compacted))
		return err
	}

	switch v := input.ToolOutput.(type) {
	case string:
		compacted := compactor.CompactLog(v)
		return json.NewEncoder(w).Encode(ClaudePostToolUseOutput{ToolOutput: compacted})
	default:
		return json.NewEncoder(w).Encode(ClaudePostToolUseOutput{ToolOutput: input.ToolOutput})
	}
}
