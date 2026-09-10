package signaldensity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tzro/pkg/ast"
	"tzro/pkg/hooks"
	"tzro/pkg/store"
)

// PiCoderTaskResult holds the execution outcome of a Pi-Coder agent task.
type PiCoderTaskResult struct {
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	Passed           bool
	Turns            int
	FinalResponse    string
	Error            string
}

// runPiCoderAgentTask executes a mini-macro coding task using the Pi-Coder agent loop.
// In Condition R (hooked=false), tool outputs are uncompressed raw terminal/build/test outputs.
// In Condition T (hooked=true), tool outputs pass through HandlePiCoderPostTool compaction.
func runPiCoderAgentTask(ctx context.Context, client *Client, model string, task TaskCase, pricing ModelPricing, s *store.Store, hooked bool) (*PiCoderTaskResult, error) {
	tmpDir, err := os.MkdirTemp("", "tzro-sdm-picoder-*")
	if err != nil {
		return nil, fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. Scaffold workspace files
	for relPath, content := range task.Scaffold {
		fullPath := filepath.Join(tmpDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return nil, fmt.Errorf("scaffold mkdir %s: %w", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("scaffold write %s: %w", relPath, err)
		}
	}

	// 2. Define tools available to the Pi-Coder agent
	tools := []chatTool{
		{
			Type: "function",
			Function: chatToolFunction{
				Name:        "read_file",
				Description: "Read the full contents of a file in the workspace.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: chatToolFunction{
				Name:        "write_file",
				Description: "Write or update a file in the workspace with complete content.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]string{"type": "string", "description": "Relative path from workspace root."},
						"content": map[string]string{"type": "string", "description": "Complete file content to write."},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: chatToolFunction{
				Name:        "run_command",
				Description: "Run a shell command in the workspace (e.g. 'go test ./...'). Returns stdout and stderr.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]string{"type": "string", "description": "Shell command to execute."},
					},
					"required": []string{"command"},
				},
			},
		},
	}

	systemPrompt := `You are an expert Go software engineer working in a workspace.
You have access to tools: read_file, write_file, and run_command.
Your goal is to inspect the code, make any necessary implementations or bug fixes, and verify that 'go test ./...' passes.
Use only one tool at a time when needed. When all tests pass, output a brief confirmation.`

	// In Condition T (hooked), equip the agent with Tzro AST skeleton and expand tools
	if hooked {
		tools = append([]chatTool{
			{
				Type: "function",
				Function: chatToolFunction{
					Name:        "tzro_skeleton",
					Description: "Get a compressed overview of a source file (imports + signatures, function bodies elided as hash tags). 70-90% smaller than full file. USE THIS FIRST on source files!",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"file": map[string]string{"type": "string", "description": "Relative path to source file from workspace root."},
						},
						"required": []string{"file"},
					},
				},
			},
			{
				Type: "function",
				Function: chatToolFunction{
					Name:        "tzro_expand",
					Description: "Retrieve an elided function body by its hash from skeleton output (e.g. '// [body elided: #abc123]'). Returns only that function body.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"hash": map[string]string{"type": "string", "description": "Hash from skeleton elision comment."},
						},
						"required": []string{"hash"},
					},
				},
			},
		}, tools...)

		systemPrompt = `You are an expert Go software engineer working in a workspace with Tzro Token Shield optimization tools:
- tzro_skeleton: Get a compressed structural overview of a source file (signatures only, bodies elided). Prefer this on source files to conserve tokens!
- tzro_expand: Retrieve only a specific function body by its hash from a skeleton.
- read_file: Read full file contents (prefer tzro_skeleton for Go source files).
- write_file: Write or update a file in the workspace.
- run_command: Run shell commands (e.g. 'go test ./...').
Your goal is to inspect the code efficiently, make any necessary implementations or bug fixes, and verify that 'go test ./...' passes.
Use only one tool at a time when needed. When all tests pass, output a brief confirmation.`
	}

	userPrompt := task.PromptRaw
	if !strings.Contains(userPrompt, "workspace") {
		userPrompt = fmt.Sprintf("Task: %s\n%s\nPlease inspect the files in the workspace, make the necessary changes, and verify that 'go test ./...' passes.", task.Name, userPrompt)
	}

	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	const maxTurns = 6
	res := &PiCoderTaskResult{}

	for turn := 0; turn < maxTurns; turn++ {
		res.Turns = turn + 1

		resp, err := client.CompleteChat(ctx, model, messages, tools, pricing)
		if err != nil {
			res.Error = fmt.Sprintf("turn %d completion error: %v", turn+1, err)
			break
		}

		res.PromptTokens += resp.PromptTokens
		res.CompletionTokens += resp.CompletionTokens
		res.CostUSD += resp.CostUSD
		res.FinalResponse = resp.Content

		// Also check if model emitted markdown code blocks directly
		if resp.Content != "" {
			applyPatchesFromResponse(tmpDir, resp.Content)
		}

		// If no tool calls, check if tests already pass and finish
		if len(resp.ToolCalls) == 0 {
			if checkGoTestPass(tmpDir) {
				res.Passed = true
			}
			break
		}

		messages = append(messages, resp.RawMessage)

		// Execute tool calls
		for _, tc := range resp.ToolCalls {
			toolOutput := executePiCoderTool(tmpDir, tc.Function.Name, tc.Function.Arguments, s)

			// In Condition T (hooked), compact tool output via Tzro Pi-Coder post-tool hook
			if hooked {
				hookInput := hooks.PiCoderPostToolInput{
					ToolName:   tc.Function.Name,
					ToolOutput: toolOutput,
				}
				hookJSON, _ := json.Marshal(hookInput)
				var hookOut bytes.Buffer
				if err := hooks.HandlePiCoderPostTool(bytes.NewReader(hookJSON), &hookOut, s); err == nil {
					var hookResp hooks.PiCoderPostToolOutput
					if err := json.Unmarshal(hookOut.Bytes(), &hookResp); err == nil {
						if sOut, ok := hookResp.ToolOutput.(string); ok && sOut != "" {
							toolOutput = sOut
						}
					}
				}
			}

			messages = append(messages, chatMessage{
				Role:       "tool",
				Content:    toolOutput,
				ToolCallID: tc.ID,
			})
		}

		// Check if tests pass after tools executed
		if checkGoTestPass(tmpDir) {
			res.Passed = true
			break
		}
	}

	// Final verification
	if !res.Passed {
		res.Passed = checkGoTestPass(tmpDir)
	}

	return res, nil
}

func checkGoTestPass(dir string) bool {
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	err := cmd.Run()
	return err == nil
}

func executePiCoderTool(workspaceDir, toolName, argsJSON string, s *store.Store) string {
	var args map[string]string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("error parsing arguments: %v", err)
	}

	switch toolName {
	case "read_file":
		relPath := args["path"]
		if relPath == "" {
			return "error: path required"
		}
		target := filepath.Join(workspaceDir, relPath)
		data, err := os.ReadFile(target)
		if err != nil {
			return fmt.Sprintf("error reading %s: %v", relPath, err)
		}
		return string(data)

	case "write_file":
		relPath := args["path"]
		content := args["content"]
		if relPath == "" {
			return "error: path required"
		}
		target := filepath.Join(workspaceDir, relPath)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Sprintf("error creating directory for %s: %v", relPath, err)
		}
		if err := os.WriteFile(target, []byte(content), 0644); err != nil {
			return fmt.Sprintf("error writing %s: %v", relPath, err)
		}
		return fmt.Sprintf("successfully wrote %d bytes to %s", len(content), relPath)

	case "run_command":
		cmdStr := args["command"]
		if cmdStr == "" {
			return "error: command required"
		}
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = workspaceDir
		out, err := cmd.CombinedOutput()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		return fmt.Sprintf("exit_code: %d\n%s", exitCode, string(out))

	case "tzro_expand":
		hash := strings.TrimPrefix(args["hash"], "#")
		if s != nil {
			blob, err := s.GetBlob(hash)
			if err == nil && blob != nil {
				return blob.Body
			}
		}
		return fmt.Sprintf("hash #%s not found in store", hash)

	case "tzro_skeleton":
		filePath := args["file"]
		if filePath == "" {
			return "error: file required"
		}
		target := filepath.Join(workspaceDir, filePath)
		data, err := os.ReadFile(target)
		if err != nil {
			return fmt.Sprintf("error reading %s: %v", filePath, err)
		}
		res, err := ast.Skeletonize(filePath, data, s, workspaceDir)
		if err != nil || res == nil {
			return string(data)
		}
		return res.SkeletonCode

	default:
		return fmt.Sprintf("unknown tool: %s", toolName)
	}
}
