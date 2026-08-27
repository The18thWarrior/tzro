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
// Now also intercepts tabular data for SQLite import.
func HandlePostToolUse(r io.Reader, w io.Writer, s *store.Store) error {
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

// CompactOrIntercept is the shared helper for all post-tool handlers.
// It detects tabular data and imports it into SQLite (returning an envelope),
// or falls through to the existing CompactLog path.
// For file reads, tabular data is always intercepted.
// For external tools, tabular data is only intercepted above the threshold.
func CompactOrIntercept(output string, toolName string, s *store.Store) string {
	isFileRead := compactor.IsFileReadTool(toolName)
	threshold := compactor.GetThreshold()

	td, ok := compactor.DetectTabular(output)
	if ok && compactor.ShouldIntercept(td, isFileRead, threshold) && s != nil {
		// Generate table name from content hash of first 3 rows
		tableName := generateTableName(td)

		if err := s.ImportTabular(tableName, td.Columns, td.Rows); err == nil {
			return compactor.FormatEnvelope(tableName, td, 5)
		}
		// Fall through to compact on import error
	}

	return compactor.CompactLog(output)
}

// generateTableName creates a deterministic table name from the first few rows of tabular data.
func generateTableName(td *compactor.TabularData) string {
	// Hash the columns + first 3 rows for deduplication
	sampleSize := 3
	if len(td.Rows) < sampleSize {
		sampleSize = len(td.Rows)
	}
	var parts []string
	parts = append(parts, strings.Join(td.Columns, "|"))
	for i := 0; i < sampleSize; i++ {
		parts = append(parts, strings.Join(td.Rows[i], "|"))
	}
	hash := store.ComputeHash(strings.Join(parts, "\n"))
	return "tbl_" + hash
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
