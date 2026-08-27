package hooks

import (
	"encoding/json"
	"io"
	"strings"

	"tzro/pkg/compactor"
	"tzro/pkg/store"
)

// CopilotPreToolInput represents the input received on stdin from GitHub Copilot agent hook.
type CopilotPreToolInput struct {
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input,omitempty"`
}

// CopilotPreToolOutput represents the response returned to Copilot agent.
type CopilotPreToolOutput struct {
	Allow         bool           `json:"allow"`
	Reason        string         `json:"reason,omitempty"`
	ModifiedInput map[string]any `json:"modified_input,omitempty"`
}

// CopilotPostToolInput represents the input received from Copilot post-execution hook.
type CopilotPostToolInput struct {
	Tool   string         `json:"tool,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
	Output any            `json:"output,omitempty"`
}

// CopilotPostToolOutput represents the response returned to Copilot agent to compact tool outputs.
type CopilotPostToolOutput struct {
	Output any `json:"output,omitempty"`
}

// HandleCopilotPreTool processes Copilot pre-tool execution.
func HandleCopilotPreTool(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input CopilotPreToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		// Fail-open: default to allow
		return json.NewEncoder(w).Encode(CopilotPreToolOutput{Allow: true})
	}

	output := CopilotPreToolOutput{Allow: true}

	if strings.Contains(strings.ToLower(input.Tool), "command") || strings.Contains(strings.ToLower(input.Tool), "bash") || strings.Contains(strings.ToLower(input.Tool), "terminal") {
		if cmd, ok := input.Input["command"].(string); ok {
			trimmed := strings.TrimSpace(cmd)
			if strings.HasPrefix(trimmed, "grep ") || strings.HasPrefix(trimmed, "find ") {
				output.Reason = "Tzro Token Shield active: Consider using `tzro probe` for sub-millisecond AST discovery."
			}
		}
	}

	return json.NewEncoder(w).Encode(output)
}

// HandleCopilotPostTool processes Copilot post-tool execution outputs and compacts logs.
func HandleCopilotPostTool(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input CopilotPostToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		// If raw string, compact directly
		compacted := compactor.CompactLog(string(data))
		_, err := w.Write([]byte(compacted))
		return err
	}

	switch v := input.Output.(type) {
	case string:
		compacted := compactor.CompactLog(v)
		return json.NewEncoder(w).Encode(CopilotPostToolOutput{Output: compacted})
	default:
		return json.NewEncoder(w).Encode(CopilotPostToolOutput{Output: input.Output})
	}
}
