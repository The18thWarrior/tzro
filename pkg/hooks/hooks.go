package hooks

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"tzro/pkg/compactor"
	"tzro/pkg/probe"
	"tzro/pkg/store"
)

// PreToolUseInput represents the input received on stdin for PreToolUse events.
type PreToolUseInput struct {
	ToolCall struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"toolCall"`
	StepIdx        int      `json:"stepIdx"`
	ConversationID string   `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
}

// PreToolUseOutput represents the output returned on stdout for PreToolUse events.
type PreToolUseOutput struct {
	Decision  string         `json:"decision"`
	Reason    string         `json:"reason,omitempty"`
	Overwrite map[string]any `json:"overwrite,omitempty"`
}

// HandlePreToolUse handles PreToolUse hook events.
func HandlePreToolUse(r io.Reader, w io.Writer, s *store.Store) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var input PreToolUseInput
	if err := json.Unmarshal(data, &input); err != nil {
		// Default to allow if parse fails
		return json.NewEncoder(w).Encode(PreToolUseOutput{Decision: "allow"})
	}

	output := PreToolUseOutput{Decision: "allow"}

	// Optimization: If the agent is trying to run a raw find/grep command, rewrite or optimize it
	if input.ToolCall.Name == "run_command" {
		cmd, ok := input.ToolCall.Args["CommandLine"].(string)
		if ok && strings.HasPrefix(strings.TrimSpace(cmd), "grep ") {
			// Fast probe suggestion
			output.Reason = "Tzro Token Shield active"
		}
	}

	return json.NewEncoder(w).Encode(output)
}

// HandlePostToolUse compresses raw command/tool outputs before they are written to history.
func HandlePostToolUse(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var payload map[string]any
	_ = json.Unmarshal(data, &payload)

	// Return empty response per Antigravity contract
	_, err = w.Write([]byte("{}\n"))
	return err
}

// HandleCompactOutput CLI pipe helper: reads raw log/test output on stdin, writes compacted text to stdout.
func HandleCompactOutput(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	compacted := compactor.CompactLog(string(data))
	_, err = w.Write([]byte(compacted))
	return err
}

// HandleProbeCLI runs a probe query and writes the markdown report to stdout.
func HandleProbeCLI(workspaceRoot, query string, w io.Writer, s *store.Store) error {
	if workspaceRoot == "" {
		workspaceRoot, _ = os.Getwd()
	}

	report, err := probe.Probe(workspaceRoot, query, 20, s)
	if err != nil {
		return err
	}

	_, err = w.Write([]byte(report.FormatMarkdown() + "\n"))
	return err
}
