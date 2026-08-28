package hooks

import (
	"encoding/json"
	"io"
	"strings"

	"tzro/pkg/store"
)

// HermesPreToolInput represents the input received on stdin from Hermes pre_tool_call hook.
type HermesPreToolInput struct {
	SessionID  string         `json:"session_id,omitempty"`
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
}

// HermesPreToolOutput represents the response returned to Hermes.
type HermesPreToolOutput struct {
	Proceed            bool           `json:"proceed"`
	Reason             string         `json:"reason,omitempty"`
	ModifiedParameters map[string]any `json:"modified_parameters,omitempty"`
}

// HermesPostToolInput represents the input received from Hermes post_tool_call hook.
type HermesPostToolInput struct {
	SessionID  string         `json:"session_id,omitempty"`
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Output     any            `json:"output,omitempty"`
}

// HermesPostToolOutput represents the response returned to Hermes to compact tool outputs.
type HermesPostToolOutput struct {
	Output any `json:"output,omitempty"`
}

// HandleHermesPreTool processes Hermes pre-tool execution.
func HandleHermesPreTool(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input HermesPreToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		// Fail-open: default to proceed
		return json.NewEncoder(w).Encode(HermesPreToolOutput{Proceed: true})
	}

	output := HermesPreToolOutput{Proceed: true}

	if strings.Contains(strings.ToLower(input.Tool), "command") || strings.Contains(strings.ToLower(input.Tool), "bash") {
		if cmd, ok := input.Parameters["command"].(string); ok {
			trimmed := strings.TrimSpace(cmd)
			if strings.HasPrefix(trimmed, "grep ") || strings.HasPrefix(trimmed, "find ") {
				output.Reason = "Tzro Token Shield active: Consider using `tzro probe` for sub-millisecond AST discovery."
			}
		}
	}

	return json.NewEncoder(w).Encode(output)
}

// HandleHermesPostTool processes Hermes post-tool outputs and compacts verbose output.
func HandleHermesPostTool(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input HermesPostToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		// If raw string on stdin, compact directly
		compacted := CompactOrIntercept(string(data), "", s)
		_, err := w.Write([]byte(compacted))
		return err
	}

	toolName := input.Tool
	switch v := input.Output.(type) {
	case string:
		processed := CompactOrIntercept(v, toolName, s)
		return json.NewEncoder(w).Encode(HermesPostToolOutput{Output: processed})
	default:
		return json.NewEncoder(w).Encode(HermesPostToolOutput{Output: input.Output})
	}
}
