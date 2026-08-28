package hooks

import (
	"encoding/json"
	"io"
	"strings"

	"tzro/pkg/store"
)

// PiCoderPreToolInput represents the input received on stdin from Pi-Coder's extension hook.
type PiCoderPreToolInput struct {
	SessionID string         `json:"session_id,omitempty"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input,omitempty"`
}

// PiCoderPreToolOutput represents the response returned to the Pi-Coder extension.
type PiCoderPreToolOutput struct {
	Allow         bool           `json:"allow"`
	Reason        string         `json:"reason,omitempty"`
	ModifiedInput map[string]any `json:"modified_input,omitempty"`
}

// PiCoderPostToolInput represents the input received from Pi-Coder post-execution hook.
type PiCoderPostToolInput struct {
	SessionID  string         `json:"session_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolInput  map[string]any `json:"tool_input,omitempty"`
	ToolOutput any            `json:"tool_output,omitempty"`
}

// PiCoderPostToolOutput represents the response returned to Pi-Coder to compact tool outputs.
type PiCoderPostToolOutput struct {
	ToolOutput any `json:"tool_output,omitempty"`
}

// HandlePiCoderPreTool processes Pi-Coder pre-tool execution.
func HandlePiCoderPreTool(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input PiCoderPreToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		// Fail-open: default to allow
		return json.NewEncoder(w).Encode(PiCoderPreToolOutput{Allow: true})
	}

	output := PiCoderPreToolOutput{Allow: true}

	// Optimization: If the tool is a shell/bash/command execution with grep/find, suggest tzro probe
	toolLower := strings.ToLower(input.ToolName)
	if strings.Contains(toolLower, "bash") || strings.Contains(toolLower, "command") || strings.Contains(toolLower, "terminal") || strings.Contains(toolLower, "shell") {
		if cmd, ok := input.ToolInput["command"].(string); ok {
			trimmed := strings.TrimSpace(cmd)
			if strings.HasPrefix(trimmed, "grep ") || strings.HasPrefix(trimmed, "find ") {
				output.Reason = "Tzro Token Shield active: Consider using `tzro probe` for sub-millisecond AST discovery."
			}
		}
	}

	return json.NewEncoder(w).Encode(output)
}

// HandlePiCoderPostTool processes Pi-Coder post-tool outputs and compacts verbose output.
func HandlePiCoderPostTool(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input PiCoderPostToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		// If raw string on stdin, compact directly
		compacted := CompactOrIntercept(string(data), "", s)
		_, err := w.Write([]byte(compacted))
		return err
	}

	toolName := input.ToolName
	switch v := input.ToolOutput.(type) {
	case string:
		processed := CompactOrIntercept(v, toolName, s)
		return json.NewEncoder(w).Encode(PiCoderPostToolOutput{ToolOutput: processed})
	default:
		return json.NewEncoder(w).Encode(PiCoderPostToolOutput{ToolOutput: input.ToolOutput})
	}
}
