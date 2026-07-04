package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ValidationResult represents the structured pass/fail result of a code validation command.
type ValidationResult struct {
	Passed     bool   `json:"passed"`
	Errors     string `json:"errors,omitempty"`
	ErrorCount int    `json:"errorCount"`
	Command    string `json:"command"`
	TargetFile string `json:"targetFile"`
}

// validateCodeSchema is the JSON schema for the validate_code tool.
const validateCodeSchema = `{
	"tool": {
		"arguments": {
			"command": {
				"type": "string",
				"description": "The compilation or validation command to execute (e.g., \"go build\", \"cargo build --all\").",
				"required": true
			},
			"targetFile": {
				"type": "string",
				"description": "The path to the target source file to run the command against.",
				"required": true
			}
		}
	}
}`

// executeValidateCode is the ExecuteFn for the validate_code tool.
func executeValidateCode(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolError("failed to parse input JSON: " + err.Error()), nil
	}

	cmd, ok := args["command"].(string)
	if !ok || cmd == "" {
		return ToolError("command is required and must be a non-empty string"), nil
	}

	targetFile, ok := args["targetFile"].(string)
	if !ok || targetFile == "" {
		return ToolError("targetFile is required and must be a non-empty string"), nil
	}

	result, err := ValidateCode(cmd, targetFile)
	if err != nil {
		return ToolError(err.Error()), nil
	}

	return ToolSuccess(result), nil
}

// ValidateCode executes the given command against the target file and returns the structured result.
func ValidateCode(command, targetFile string) (*ValidationResult, error) {
	if command == "" {
		return nil, fmt.Errorf("command is empty")
	}

	// Replace {{targetFile}} placeholder in the command with the actual target file path.
	// This handles cases where the command is templated (e.g., "go build {{targetFile}}").
	processedCommand := strings.ReplaceAll(command, "{{targetFile}}", targetFile)

	dir := ""
	if idx := strings.LastIndex(targetFile, "/"); idx >= 0 {
		parentDir := targetFile[:idx]
		if info, err := os.Stat(parentDir); err == nil && info.IsDir() {
			dir = parentDir
		}
	}

	// Split the processed command into arguments.
	// We use strings.Fields to safely handle arguments with spaces.
	cmd := strings.Fields(processedCommand)
	if len(cmd) < 1 {
		return nil, fmt.Errorf("invalid command format after processing: '%s'", processedCommand)
	}
	exe := cmd[0]
	args := cmd[1:]

	// Create the exec.CommandContext with a 30-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmdObj := exec.CommandContext(ctx, exe, args...)
	cmdObj.Dir = dir

	// Capture stdout and stderr separately.
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	cmdObj.Stdout = stdout
	cmdObj.Stderr = stderr

	// Execute the command.
	err := cmdObj.Run()
	if err != nil {
		// Extract error messages from stderr, falling back to stdout or the generic error.
		errStr := stderr.String()
		if errStr == "" {
			errStr = stdout.String()
		}
		if errStr == "" {
			errStr = err.Error()
		}

		// Heuristically count error lines.
		// We look for lines containing common error indicators.
		errorLines := strings.Split(errStr, "\n")
		errorCount := 0
		for _, line := range errorLines {
			line = strings.TrimSpace(line)
			if strings.Contains(strings.ToLower(line), "error") ||
				strings.Contains(strings.ToLower(line), "cannot") ||
				strings.Contains(strings.ToLower(line), "undefined") {
				errorCount++
			}
		}
		// Ensure at least 1 error count if any errors were captured.
		if errorCount == 0 {
			errorCount = 1
		}

		return &ValidationResult{
			Passed:     false,
			Errors:     errStr,
			ErrorCount: errorCount,
			Command:    processedCommand,
			TargetFile: targetFile,
		}, nil
	}

	return &ValidationResult{
		Passed:     true,
		Errors:     "",
		ErrorCount: 0,
		Command:    processedCommand,
		TargetFile: targetFile,
	}, nil
}

// Init registers the validate_code tool into the tool registry.
func init() {
	Register(&BaseAgentTool{
		name:        "validate_code",
		description: "Run a compilation or validation command against a target file and return structured pass/fail results with error details.",
		schema:      validateCodeSchema,
		executeFn:   executeValidateCode,
	})
}
