package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/channel"
	"tzro/internal/codegen"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	internalmcp "tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/packagemanager"
	"tzro/internal/sentinel"
	"tzro/internal/task"
	"tzro/internal/tools"
	"tzro/internal/workflow"
)

// tzro_run tool definition

// TzroRunArgs defines the inputs for running a natural language task.
type TzroRunArgs struct {
	Prompt        string `json:"prompt" jsonschema:"required,The natural language task to execute"`
	Timeout       int    `json:"timeout,omitempty" jsonschema:"Execution timeout in seconds before switching to async. Default 60"`
	SelfContained bool   `json:"selfContained,omitempty" jsonschema:"Set to true when the prompt contains all required data and no external tool calls are needed. Bypasses planner and runs Direct Synthesis."`
}

func handleTzroRun(ctx context.Context, req *mcp.CallToolRequest, args TzroRunArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Prompt) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "prompt cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	taskID := uuid.New().String()

	// Start SubagentChannel for real-time event delivery to the harness.
	// Node count is unknown at this point (planning hasn't started), so pass 0.
	// Channel lifecycle is managed by the background goroutine, not this handler.
	ch := startSubagentChannel(req, mcpServer, taskID, 0)
	if ch != nil {
		// v2: Register channel for bidirectional tool dispatch.
		// ChannelToolHook will intercept client-tool nodes and dispatch via sampling.
		channel.GlobalChannelToolHook.RegisterChannel(taskID, ch)
	}

	// Execute task in a background goroutine. The handler returns immediately
	// so the MCP App UI can connect to the task via SSE using the taskId.
	go func() {
		// Manage channel lifecycle within the goroutine scope.
		if ch != nil {
			defer ch.Close()
			defer channel.GlobalChannelToolHook.UnregisterChannel(taskID)
		}

		if isDaemonRunning() {
			type RunRequest struct {
				Prompt        string `json:"prompt"`
				TaskID        string `json:"taskId"`
				SelfContained bool   `json:"selfContained,omitempty"` // ADR-0054
			}
			reqBody := RunRequest{
				Prompt:        args.Prompt,
				TaskID:        taskID,
				SelfContained: args.SelfContained,
			}
			_, err := proxyToDaemon("/api/tasks/run", "POST", reqBody)
			if err != nil {
				fmt.Printf("[tzro-mcp] daemon task run initiation failed for %s: %v\n", taskID, err)
				return
			}

			daemonURL := config.GetDaemonURL()
			url := fmt.Sprintf("%s/api/tasks/events?taskId=%s", daemonURL, taskID)

			resp, err := http.Get(url)
			if err != nil {
				fmt.Printf("[tzro-mcp] failed to connect to daemon event stream for %s: %v\n", taskID, err)
				return
			}
			defer resp.Body.Close()

			reader := bufio.NewReader(resp.Body)
			var currentEventType string

			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				if strings.HasPrefix(line, "event: ") {
					currentEventType = strings.TrimPrefix(line, "event: ")
				} else if strings.HasPrefix(line, "data: ") {
					dataStr := strings.TrimPrefix(line, "data: ")
					if dataStr == "[DONE]" {
						break
					}

					var event channel.ExecutionEvent
					if err := json.Unmarshal([]byte(dataStr), &event); err == nil {
						if event.Type == "" {
							event.Type = currentEventType
						}
						if ch != nil {
							_ = ch.EmitEvent(event)
						}

						if event.Type == channel.EventTaskCompleted || event.Type == channel.EventTaskFailed {
							break
						}
					}
				}
			}
		} else {
			execOpts := task.ExecuteOptions{
				TaskID:        taskID,
				IntentType:    "workflow",
				IsForeground:  true,
				SelfContained: args.SelfContained,
			}
			_, _, err := task.Execute(context.Background(), args.Prompt, execOpts)
			if err != nil {
				fmt.Printf("[tzro-mcp] task execution failed for %s: %v\n", taskID, err)
			}
		}
	}()

	// Return immediately with the taskId. The agent polls tzro_status for
	// completion; the MCP App UI connects via SSE to the daemon for live updates.
	daemonPort := getDaemonPort()
	setLastTask(taskID, daemonPort)

	respMap := map[string]interface{}{
		"taskId":     taskID,
		"status":     "accepted",
		"daemonPort": daemonPort,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Meta: mcp.Meta{"ui": map[string]any{"resourceUri": buildAppResourceURI(taskID, daemonPort)}},
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_code tool definition

// TzroCodeArgs defines the inputs for the tzro_code code generation tool.
type TzroCodeArgs struct {
	Spec       string `json:"spec" jsonschema:"required,The specification or JSDoc describing what to generate"`
	Filepath   string `json:"filepath" jsonschema:"required,Absolute path to the target file to create or update"`
	Language   string `json:"language,omitempty" jsonschema:"Programming language override. Auto-detected from file extension if omitted"`
	MaxLines   int    `json:"maxLines,omitempty" jsonschema:"Maximum lines for generated file. Default: 500 or config codeMaxLines"`
	Timeout    int    `json:"timeout,omitempty" jsonschema:"Execution timeout in seconds before switching to async. Default 120"`
	Mode       string `json:"mode,omitempty" jsonschema:"Code generation mode: 'full' (whole file), 'diff' (structured hunks), or '' (auto-select). Auto: new/small files use full, large files use diff"`
	Pseudocode string `json:"pseudocode,omitempty" jsonschema:"Structured pseudo-code to expand into source code. When provided, the local model expands this into compilable code instead of generating from spec alone. Use when the task exceeds T1 complexity."`
}

// maxFullRewriteLines is the hard limit for whole-file rewrite mode.
// Files exceeding this must use diff mode to prevent truncation data loss.
const maxFullRewriteLines = 500

// autoModeDiffThreshold is the line count above which auto-mode selects diff
// instead of full. Set aggressively low: files ≥20 lines use the Edit Loop
// for ~30-50× token reduction over full-file generation.
const autoModeDiffThreshold = 20

func handleTzroCode(ctx context.Context, req *mcp.CallToolRequest, args TzroCodeArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Spec) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "spec cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}
	if strings.TrimSpace(args.Filepath) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "filepath cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	taskID := uuid.New().String()
	// Note: args.Timeout is accepted for backward compatibility but ignored
	// since tzro_code now returns immediately (fully async).


	// Resolve maxLines: arg → config → default 500
	maxLines := args.MaxLines
	if maxLines <= 0 {
		cfg := config.Get()
		if cfg.CodeMaxLines > 0 {
			maxLines = cfg.CodeMaxLines
		} else {
			maxLines = 500
		}
	}

	// Detect language from extension if not provided
	language := args.Language
	if language == "" {
		language = codegen.DetectLanguage(args.Filepath)
	}

	// Pre-compute context: pure Go, no LLM (design spec: "No LLM. Pure Go logic.")
	allowedPaths := tools.GetAllowedPaths()
	if strings.Contains(args.Filepath, "tzro_benchmark_") || strings.Contains(args.Filepath, "tzro_codegen_") {
		allowedPaths = append(allowedPaths, filepath.Dir(args.Filepath))
	}
	validator := tools.NewPathValidator(allowedPaths)
	codeCtx, ctxErr := codegen.GatherContext(args.Filepath, validator)
	if ctxErr != nil {
		// Non-fatal: proceed with nil context (new file creation case)
		fmt.Fprintf(os.Stderr, "[tzro_code] GatherContext warning: %v\n", ctxErr)
		codeCtx = &codegen.CodeContext{
			Language: language,
			Siblings: make(map[string]string),
		}
	}

	// --- Mode resolution ---
	mode := args.Mode

	// Validate mode value
	if mode != "" && mode != "full" && mode != "diff" {
		errJSON := fmt.Sprintf(`{"error": "Invalid mode %q. Must be 'full', 'diff', or '' (auto-select)."}`, mode)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: errJSON},
			},
			IsError: true,
		}, nil, nil
	}

	// Diff mode requires an existing file
	if mode == "diff" && !codeCtx.Exists {
		errJSON := fmt.Sprintf(
			`{"error": "Cannot use mode \"diff\" for %s: file does not exist. Use mode \"full\" for new file creation."}`,
			args.Filepath,
		)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: errJSON},
			},
			IsError: true,
		}, nil, nil
	}

	// Auto-mode selection
	if mode == "" {
		if !codeCtx.Exists {
			mode = "full" // New file creation → whole file
		} else {
			existingLines := strings.Count(codeCtx.ExistingContent, "\n")
			if existingLines > autoModeDiffThreshold {
				mode = "diff" // Large existing file → diff mode
			} else {
				mode = "full" // Small existing file → whole file OK
			}
		}
	}

	// File size guard: files > 500 lines MUST use diff mode
	if codeCtx.Exists && mode != "diff" {
		existingLines := strings.Count(codeCtx.ExistingContent, "\n")
		if existingLines > maxFullRewriteLines {
			respMap := map[string]interface{}{
				"status": "failed",
				"taskId": "",
				"error": fmt.Sprintf(
					"File %s has %d lines (limit: %d for full rewrite). "+
						"Use mode: \"diff\" for surgical edits, or decompose the file into "+
						"smaller single-responsibility files.",
					args.Filepath, existingLines, maxFullRewriteLines,
				),
				"suggestion": "diff",
			}
			respBytes, _ := json.MarshalIndent(respMap, "", "  ")
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(respBytes)},
				},
				IsError: true,
			}, nil, nil
		}
	}

	fmt.Fprintf(os.Stderr, "[tzro_code] Mode=%s for %s\n", mode, args.Filepath)

	// Create a task record so tzro_status can report on this task immediately.
	if err := memory.DB.CreateTask(taskID, fmt.Sprintf("[codegen] %s → %s", mode, args.Filepath)); err != nil {
		fmt.Fprintf(os.Stderr, "[tzro_code] WARNING: CreateTask failed for %s: %v\n", taskID, err)
	}

	// Start SubagentChannel for real-time event delivery to the UI.
	ch := startSubagentChannel(req, mcpServer, taskID, 0)
	if ch != nil {
		channel.GlobalChannelToolHook.RegisterChannel(taskID, ch)
	}

	// ADR-0057: AllowCloudRepair is true in Direct mode (no pseudocode provided)
	// and false in Draft mode (pseudocode provided). Draft mode returns
	// the failing code + compiler errors to the caller for targeted edits.
	isDraftMode := strings.TrimSpace(args.Pseudocode) != ""

	// Execute in background goroutine. The handler returns immediately
	// so the MCP client can poll via tzro_status.
	go func() {
		// Manage channel lifecycle within the goroutine scope.
		if ch != nil {
			defer ch.Close()
			defer channel.GlobalChannelToolHook.UnregisterChannel(taskID)
		}

		if err := memory.DB.UpdateTaskStatus(taskID, "running", ""); err != nil {
			fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(running) failed for %s: %v\n", taskID, err)
		}

		// Temperature 0.65 for codegen: sharper than default 1.0, reduces noise
		// in generated code while min_p 0.1 still provides dynamic token pruning.
		// Research finding: 0.6-0.7 is optimal for unconstrained codegen on 4B models.
		codeCtxBg := context.WithValue(context.Background(), inference.TemperatureKey, 0.65)

		// Presence penalty 1.3 for codegen: penalizes any token that has already
		// appeared, preventing the self-referential comment loop pattern where the
		// model enters "// But the spec might expect..." degeneration cycles.
		// Research finding: presence_penalty (1.2-1.5) is the single most effective
		// parameter for preventing degeneration in GGUF models (★★★★★).
		codeCtxBg = context.WithValue(codeCtxBg, inference.PresencePenaltyKey, 1.3)

		// DRY (Don't Repeat Yourself) sampling for codegen: lighter than synthesis
		// (0.6 vs 0.8 multiplier) with higher AllowedLength (3 vs 2) to allow
		// repeated struct fields, test cases, and switch arms. Code-aware sequence
		// breakers prevent penalty accumulation across structural boundaries.
		codeCtxBg = context.WithValue(codeCtxBg, inference.DRYSamplingKey, inference.DRYSamplingConfig{
			Multiplier:       0.6,
			Base:             1.75,
			AllowedLength:    3,
			PenaltyLastN:     -1,
			SequenceBreakers: []string{"{", "}", ";", "(", ")", "\n"},
		})

		// Route via pseudo-code availability and complexity classification:
		//   pseudocode provided → expansion DAG (any complexity)
		//   no pseudocode, diff → edit loop (inline, no DAG)
		//   no pseudocode, full → DAG codegen
		if strings.TrimSpace(args.Pseudocode) == "" && mode == "diff" {
			// Edit Loop path: plan-then-hunk iterative codegen, no DAG needed.
			moduleContext := codegen.DiscoverModuleContext(args.Filepath, language)
			editEngine := &codegen.DefaultEditLoopEngine{}
			patchedContent, editErr := codegen.RunEditLoop(
				codeCtxBg, editEngine, args.Spec, args.Filepath,
				codeCtx.ExistingContent, language, codeCtx.Siblings, moduleContext,
			)
			if editErr != nil {
				_ = memory.DB.SetNodeState(taskID, "edit_loop", "failed", fmt.Sprintf("edit loop failed: %v", editErr))
				if err := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("edit loop failed: %v", editErr)); err != nil {
					fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
				}
				fmt.Fprintf(os.Stderr, "[tzro_code] Edit loop failed for %s: %v\n", args.Filepath, editErr)
				return
			}

			// Write patched file
			if backupErr := tools.BackupFile(args.Filepath); backupErr != nil {
				fmt.Fprintf(os.Stderr, "[codegen] Backup failed (non-fatal): %v\n", backupErr)
			}
			if writeErr := os.WriteFile(args.Filepath, []byte(patchedContent), 0644); writeErr != nil {
				_ = memory.DB.SetNodeState(taskID, "edit_loop", "failed", fmt.Sprintf("file write failed: %v", writeErr))
				if err := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("file write failed: %v", writeErr)); err != nil {
					fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
				}
				fmt.Fprintf(os.Stderr, "[tzro_code] File write failed for %s: %v\n", args.Filepath, writeErr)
				return
			}

			totalLines := strings.Count(patchedContent, "\n")

			// Post-codegen compile gate: validate the edit_loop output compiles.
			// If compilation fails, re-run edit loop with error feedback (max 2 retries).
			compResult := codegen.RunCompilationGate(language, args.Filepath)
			if !compResult.Pass {
				const maxEditLoopRetries = 2
				for retry := 0; retry < maxEditLoopRetries; retry++ {
					fmt.Fprintf(os.Stderr, "[tzro_code] Compilation failed after edit_loop (attempt %d/%d): %s\n",
						retry+1, maxEditLoopRetries, compResult.Reason)

					// Build repair spec: original spec + compiler errors
					repairSpec := fmt.Sprintf(
						"%s\n\n## COMPILER ERRORS (must fix)\n%s\n\nFix ALL compiler errors. Preserve the existing implementation intent.",
						args.Spec, compResult.Reason,
					)

					// Re-run edit loop with error-enriched spec
					repairContent, repairErr := codegen.RunEditLoop(
						codeCtxBg, editEngine, repairSpec, args.Filepath,
						patchedContent, language, codeCtx.Siblings, moduleContext,
					)
					if repairErr != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] Repair edit loop failed: %v\n", repairErr)
						break
					}

					// Write repaired file
					if writeErr := os.WriteFile(args.Filepath, []byte(repairContent), 0644); writeErr != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] Repair file write failed: %v\n", writeErr)
						break
					}

					patchedContent = repairContent
					totalLines = strings.Count(patchedContent, "\n")
					compResult = codegen.RunCompilationGate(language, args.Filepath)
					if compResult.Pass {
						fmt.Fprintf(os.Stderr, "[tzro_code] Compilation passed after repair attempt %d\n", retry+1)
						break
					}
				}

				// If still failing after all retries, report the error
				if !compResult.Pass {
					errMsg := fmt.Sprintf("compilation failed after %d repair attempts: %s", maxEditLoopRetries, compResult.Reason)
					_ = memory.DB.SetNodeState(taskID, "edit_loop", "failed", errMsg)
					if err := memory.DB.UpdateTaskStatus(taskID, "failed", errMsg); err != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
					}
					fmt.Fprintf(os.Stderr, "[tzro_code] Edit loop compilation failed for %s: %s\n", args.Filepath, compResult.Reason)
					return
				}
			}

			outputMsg := fmt.Sprintf("Updated %s via edit_loop (%d lines)", args.Filepath, totalLines)
			_ = memory.DB.SetNodeState(taskID, "edit_loop", "completed", outputMsg)
			if err := memory.DB.UpdateTaskStatus(taskID, "completed", ""); err != nil {
				fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(completed) failed for %s: %v\n", taskID, err)
			}
			fmt.Fprintf(os.Stderr, "[tzro_code] Edit loop completed for %s (%d lines)\n", args.Filepath, totalLines)
			return
		}

		// DAG path: build execution graph for full/pseudocode modes.
		var graph *compiler.ExecutionGraph
		if strings.TrimSpace(args.Pseudocode) != "" {
			fmt.Fprintf(os.Stderr, "[tzro_code] Pseudo-code expansion mode for %s\n", args.Filepath)
			graph = codegen.BuildPseudocodeExpansionDAG(taskID, args.Pseudocode, args.Spec, args.Filepath, language, maxLines, codeCtx)
		} else {
			tier := codegen.ClassifyCodeComplexity(args.Spec, codeCtx)
			fmt.Fprintf(os.Stderr, "[tzro_code] Complexity tier=%s, proceeding with direct generation for %s\n", tier, args.Filepath)
			graph = codegen.BuildCodeDAG(taskID, args.Spec, args.Filepath, language, maxLines, codeCtx)
		}

		// Register the compilation gate hook for this task's execution.
		compilationHook := &codegen.CompilationGateHook{
			FilePath:         args.Filepath,
			Language:         language,
			Spec:             args.Spec,
			AllowCloudRepair: !isDraftMode,
		}
		executor.GlobalEngine.RegisterHook(compilationHook)
		defer executor.GlobalEngine.UnregisterHook(compilationHook)

		execOpts := task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "codegen",
			IsForeground: true,
		}
		_, execErr := task.ExecuteStatic(codeCtxBg, graph, execOpts)
		_ = execErr
		nodes := memory.DB.GetAllNodeStates(taskID)

		// Check if any node failed
		var taskErr error
		for _, n := range nodes {
			if n.Status == "failed" {
				taskErr = fmt.Errorf("node %s failed: %s", n.NodeID, n.Output)
				break
			}
		}

		status := "completed"
		if taskErr != nil {
			status = "failed"
		}

		// ADR-0057: In Draft mode, enrich failure with compiler errors
		if status == "failed" && isDraftMode && compilationHook.GetLocalFailureCount() > 0 {
			errMsg := fmt.Sprintf("complexity_exceeded: %v", taskErr)
			if err := memory.DB.UpdateTaskStatus(taskID, "failed", errMsg); err != nil {
				fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
			}
			fmt.Fprintf(os.Stderr, "[tzro_code] DAG failed (complexity_exceeded) for %s\n", args.Filepath)
			return
		}

		// Post-process: extract reason_code output, write file (pure Go)
		if status == "completed" {
			var rawCode string
			for _, n := range nodes {
				if n.NodeID == "reason_code" && n.Status == "completed" {
					rawCode = n.RawOutput
					if rawCode == "" {
						rawCode = n.Output
					}
				}
			}

			if rawCode == "" {
				if err := memory.DB.UpdateTaskStatus(taskID, "failed", "reason_code produced no output"); err != nil {
					fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
				}
				fmt.Fprintf(os.Stderr, "[tzro_code] reason_code produced no output for %s\n", args.Filepath)
				return
			}

			// Quality gate: structural validation before writing
			cleanedForGate := codegen.StripMarkdownFences(rawCode)
			gateResult := codegen.RunStructuralQualityGate(cleanedForGate, language)
			if !gateResult.Pass {
				fmt.Fprintf(os.Stderr, "[tzro_code] Quality gate failed: %s\n", gateResult.Reason)
				if err := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("quality gate: %s", gateResult.Reason)); err != nil {
					fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
				}
				return
			}

			// Quality gate passed — write the file
			switch mode {
			case "diff":
				// Parse structured diff output
				var diffOutput codegen.DiffOutput
				rawJSON := rawCode
				if err := json.Unmarshal([]byte(rawJSON), &diffOutput); err != nil {
					stripped := codegen.StripMarkdownFences(rawJSON)
					if err2 := json.Unmarshal([]byte(stripped), &diffOutput); err2 != nil {
						if dbErr := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("diff output parse failed: %v", err)); dbErr != nil {
							fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, dbErr)
						}
						return
					}
				}

				if len(diffOutput.Hunks) == 0 {
					if err := memory.DB.UpdateTaskStatus(taskID, "failed", "diff output contained no hunks"); err != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
					}
					return
				}

				patched, applyErr := codegen.ApplyDiffHunks(codeCtx.ExistingContent, diffOutput.Hunks)
				if applyErr != nil {
					if err := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("diff application failed: %v", applyErr)); err != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
					}
					return
				}

				if backupErr := tools.BackupFile(args.Filepath); backupErr != nil {
					fmt.Fprintf(os.Stderr, "[codegen] Backup failed (non-fatal): %v\n", backupErr)
				}
				if writeErr := os.WriteFile(args.Filepath, []byte(patched), 0644); writeErr != nil {
					if err := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("file write failed: %v", writeErr)); err != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
					}
					return
				}

				fmt.Fprintf(os.Stderr, "[tzro_code] DAG diff completed for %s (%d hunks)\n", args.Filepath, len(diffOutput.Hunks))

			default: // "full"
				writeAction, linesWritten, writeErr := codegen.WriteCodeFile(args.Filepath, rawCode, maxLines)
				if writeErr != nil {
					if err := memory.DB.UpdateTaskStatus(taskID, "failed", fmt.Sprintf("file write failed: %v", writeErr)); err != nil {
						fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
					}
					return
				}
				_ = writeAction
				fmt.Fprintf(os.Stderr, "[tzro_code] DAG full completed for %s (%d lines written)\n", args.Filepath, linesWritten)
			}
		}

		// Final status update
		if status == "failed" {
			if err := memory.DB.UpdateTaskStatus(taskID, "failed", taskErr.Error()); err != nil {
				fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(failed) failed for %s: %v\n", taskID, err)
			}
		} else {
			if err := memory.DB.UpdateTaskStatus(taskID, "completed", ""); err != nil {
				fmt.Fprintf(os.Stderr, "[tzro_code] UpdateTaskStatus(completed) failed for %s: %v\n", taskID, err)
			}
		}
	}()

	// Return immediately with the taskId. The agent polls tzro_status for
	// completion; the MCP App UI connects via SSE for live updates.
	daemonPort := getDaemonPort()
	setLastTask(taskID, daemonPort)

	respMap := map[string]interface{}{
		"taskId":     taskID,
		"status":     "accepted",
		"filepath":   args.Filepath,
		"language":   language,
		"mode":       mode,
		"daemonPort": daemonPort,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Meta: mcp.Meta{"ui": map[string]any{"resourceUri": buildAppResourceURI(taskID, daemonPort)}},
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_status tool definition

// TzroStatusArgs defines the inputs for checking task execution status.
type TzroStatusArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to check"`
}

func handleTzroStatus(ctx context.Context, req *mcp.CallToolRequest, args TzroStatusArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.TaskID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	nodes := memory.DB.GetAllNodeStates(args.TaskID)
	if len(nodes) == 0 {
		// ADR-0054: Check tasks table for task-level status before returning "not found".
		// Planning failures and in-progress planning produce no node_states rows.
		taskRecord, _ := memory.DB.GetTask(args.TaskID)
		if taskRecord != nil {
			respMap := map[string]interface{}{
				"taskId": args.TaskID,
				"status": taskRecord.Status,
			}
			if taskRecord.Error != "" {
				respMap["error"] = taskRecord.Error
				respMap["instruction"] = fmt.Sprintf(
					"Task planning failed. Review the error and re-submit with adjustments. Error: %s", taskRecord.Error)
			}
			respBytes, _ := json.MarshalIndent(respMap, "", "  ")
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(respBytes)},
				},
				IsError: taskRecord.Status == "failed",
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"taskId": "%s", "error": "task not found"}`, args.TaskID)},
			},
			IsError: true,
		}, nil, nil
	}

	failedCount := 0
	runningCount := 0
	completedCount := 0
	cancelledCount := 0
	nodeCount := len(nodes)
	var completedAt int64

	for _, n := range nodes {
		if n.Status == "failed" {
			failedCount++
		} else if n.Status == "running" {
			runningCount++
		} else if n.Status == "completed" {
			completedCount++
		} else if n.Status == "cancelled" {
			cancelledCount++
		}
		if n.CompletedAt > completedAt {
			completedAt = n.CompletedAt
		}
	}

	taskStatus := "pending"
	if cancelledCount > 0 {
		taskStatus = "cancelled"
	} else if failedCount > 0 {
		taskStatus = "failed"
	} else if runningCount > 0 {
		taskStatus = "running"
	} else if completedCount == nodeCount {
		taskStatus = "completed"
	}

	// Check if there are unread client-side tool requests or human approval requests for this task
	if taskStatus == "pending" || taskStatus == "running" {
		notifs, err := memory.DB.GetNotifications("unread")
		if err == nil {
			for _, n := range notifs {
				if n.TaskID == args.TaskID {
					if n.Source == "client_tool" && n.Type == "client_tool_request" {
						taskStatus = "waiting_for_client"
						break
					} else if n.Source == "human_approval" && n.Type == "approval_request" {
						taskStatus = "waiting_for_approval"
						break
					}
				}
			}
		}
	}

	respMap := map[string]interface{}{
		"taskId":      args.TaskID,
		"status":      taskStatus,
		"nodes":       nodes,
		"completedAt": completedAt,
	}
	// ADR-0055: Hoist Execution Envelope to top-level result
	if envelope := extractEnvelopeResult(nodes); envelope != nil {
		respMap["result"] = envelope
	}

	if taskStatus == "waiting_for_approval" {
		respMap["instruction"] = fmt.Sprintf("Task is waiting for human approval. Use the 'tzro_hook_approve' tool with taskId: '%s' and nodeId: '<nodeId>' to approve, or check pending approvals via 'tzro_hook_list'.", args.TaskID)
	} else if taskStatus == "waiting_for_client" {
		respMap["instruction"] = "Task is waiting for client tool execution. Use the 'tzro_client_tool_submit' tool to submit the result."
	} else if taskStatus == "failed" {
		respMap["instruction"] = fmt.Sprintf("Task execution failed. Review the errors on the failed node(s), fix any issues, and use the 'tzro_resume' tool with taskId: '%s' to resume.", args.TaskID)
	}

	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_cancel tool definition

// TzroCancelArgs defines the inputs for cancelling a running task.
type TzroCancelArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to cancel"`
}

func handleTzroCancel(ctx context.Context, req *mcp.CallToolRequest, args TzroCancelArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.TaskID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// POST to daemon /api/tasks/cancel
	reqBody, _ := json.Marshal(map[string]string{"taskId": args.TaskID})
	httpResp, err := http.Post(config.GetDaemonURL()+"/api/tasks/cancel", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to reach daemon: %v"}`, err)},
			},
			IsError: true,
		}, nil, nil
	}
	defer httpResp.Body.Close()

	respBytes, _ := io.ReadAll(httpResp.Body)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_list_tasks tool definition

// TzroListTasksArgs defines the inputs for listing recent tasks.
type TzroListTasksArgs struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of tasks to return. Default 20"`
	Status string `json:"status,omitempty" jsonschema:"Filter by status: running, completed, failed. Default all"`
}

func handleTzroListTasks(ctx context.Context, req *mcp.CallToolRequest, args TzroListTasksArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	tasks, err := memory.DB.GetRecentTasks(limit, args.Status)
	if err != nil {
		return nil, nil, err
	}

	respBytes, _ := json.MarshalIndent(tasks, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_configure_tools tool definition

// TzroConfigureToolsArgs defines the inputs for configuring new daemon tools.
type TzroConfigureToolsArgs struct {
	Servers map[string]internalmcp.MCPServerConfig `json:"servers" jsonschema:"required,Map of server name to MCP server config"`
}

func handleTzroConfigureTools(ctx context.Context, req *mcp.CallToolRequest, args TzroConfigureToolsArgs) (*mcp.CallToolResult, any, error) {
	configPath := config.ResolvePath("mcp_config.json")

	// Read existing config or initialize empty
	var mcpCfg internalmcp.MCPConfig
	mcpCfg.MCPServers = make(map[string]internalmcp.MCPServerConfig)

	if _, err := os.Stat(configPath); err == nil {
		fileBytes, err := os.ReadFile(configPath)
		if err == nil {
			_ = json.Unmarshal(fileBytes, &mcpCfg)
		}
	}

	// Merge new entries
	for k, v := range args.Servers {
		mcpCfg.MCPServers[k] = v
	}

	// Write back to disk
	mergedBytes, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal merged config: %w", err)
	}

	_ = os.MkdirAll(filepath.Dir(configPath), 0755)
	if err := os.WriteFile(configPath, mergedBytes, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write mcp_config.json: %w", err)
	}

	// Reload daemon config
	if err := internalmcp.GlobalRegistry.LoadConfig(configPath); err != nil {
		return nil, nil, fmt.Errorf("failed to reload daemon config: %w", err)
	}

	// Discover new tools
	newTools, err := internalmcp.GlobalRegistry.DiscoverTools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover tools: %w", err)
	}

	// Gather newly discovered tool names
	var toolNames []string
	for name := range newTools {
		toolNames = append(toolNames, name)
	}

	respMap := map[string]interface{}{
		"status":          "success",
		"discoveredTools": toolNames,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroMemoryQueryArgs defines inputs for tzro_memory_query.
type TzroMemoryQueryArgs struct {
	Query string `json:"query" jsonschema:"required,The natural language semantic search query"`
	Limit int    `json:"limit,omitempty" jsonschema:"Max number of memories and nodes to return. Default 10"`
}

func handleTzroMemoryQuery(ctx context.Context, req *mcp.CallToolRequest, args TzroMemoryQueryArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Query) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "query cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	mems, nodes, err := memory.DB.SearchMemoriesAndNodes(args.Query, limit)
	if err != nil {
		return nil, nil, err
	}
	respMap := map[string]interface{}{
		"memories": mems,
		"nodes":    nodes,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroMemoryIngestArgs defines inputs for tzro_memory_ingest.
type TzroMemoryIngestArgs struct {
	Type       string  `json:"type" jsonschema:"required,The category of memory: fact, preference, insight, correction, anti_pattern, strategy"`
	Content    string  `json:"content" jsonschema:"required,The text content representing the memory"`
	UserID     string  `json:"userId,omitempty" jsonschema:"User ID this memory belongs to"`
	Context    string  `json:"context,omitempty" jsonschema:"Session ID or context description"`
	Confidence float64 `json:"confidence,omitempty" jsonschema:"Confidence score between 0.0 and 1.0"`
}

func handleTzroMemoryIngest(ctx context.Context, req *mcp.CallToolRequest, args TzroMemoryIngestArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Content) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "content cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}
	validTypes := map[string]bool{
		"fact":         true,
		"preference":   true,
		"insight":      true,
		"correction":   true,
		"anti_pattern": true,
		"strategy":     true,
	}
	if !validTypes[args.Type] {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "invalid memory type: must be one of fact, preference, insight, correction, anti_pattern, strategy"}`},
			},
			IsError: true,
		}, nil, nil
	}

	var embedding []float32
	if memory.DB.EmbeddingEngine != nil {
		vec, err := memory.DB.EmbeddingEngine.Embed(ctx, args.Content)
		if err == nil {
			embedding = vec
		}
	}
	conf := args.Confidence
	if conf <= 0 {
		conf = 1.0
	}
	m := memory.FactMemory{
		UserID:     args.UserID,
		Type:       args.Type,
		Content:    args.Content,
		Context:    args.Context,
		Confidence: conf,
		Source:     "mcp_ingest",
		Embedding:  embedding,
	}
	if err := memory.DB.AddMemory(m); err != nil {
		return nil, nil, err
	}
	respMap := map[string]interface{}{
		"status":  "success",
		"content": args.Content,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroKgNeighborhoodArgs defines inputs for tzro_kg_neighborhood.
type TzroKgNeighborhoodArgs struct {
	EntityID  string   `json:"entityId" jsonschema:"required,The node ID from which to start traversal"`
	MaxHops   int      `json:"maxHops,omitempty" jsonschema:"Max traverse steps. Default 2"`
	NodeTypes []string `json:"nodeTypes,omitempty" jsonschema:"Restrict traversal to these node types"`
	EdgeTypes []string `json:"edgeTypes,omitempty" jsonschema:"Restrict traversal to these edge types"`
	Direction string   `json:"direction,omitempty" jsonschema:"Traversal direction: incoming, outgoing, undirected. Default undirected"`
	Limit     int      `json:"limit,omitempty" jsonschema:"Max number of nodes to return"`
}

func handleTzroKgNeighborhood(ctx context.Context, req *mcp.CallToolRequest, args TzroKgNeighborhoodArgs) (*mcp.CallToolResult, any, error) {
	maxHops := args.MaxHops
	if maxHops <= 0 {
		maxHops = 2
	}
	var opts []memory.NeighborhoodOption
	if len(args.NodeTypes) > 0 {
		opts = append(opts, memory.WithNodeTypes(args.NodeTypes))
	}
	if len(args.EdgeTypes) > 0 {
		opts = append(opts, memory.WithEdgeTypes(args.EdgeTypes))
	}
	if args.Direction != "" {
		opts = append(opts, memory.WithDirection(args.Direction))
	}
	if args.Limit > 0 {
		opts = append(opts, memory.WithLimit(args.Limit))
	}
	subgraph := memory.DB.GetEntityNeighborhood(args.EntityID, maxHops, opts...)
	respBytes, _ := json.MarshalIndent(subgraph, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// KGNodeInput defines node properties for tzro_kg_add_entity.
type KGNodeInput struct {
	ID       string                 `json:"id" jsonschema:"required,Node unique identifier"`
	NodeType string                 `json:"nodeType" jsonschema:"required,Node type e.g. account, contact, ticket, document"`
	Name     string                 `json:"name" jsonschema:"required,Display name of entity"`
	Metadata map[string]interface{} `json:"metadata,omitempty" jsonschema:"Arbitrary node metadata properties"`
	Source   string                 `json:"source,omitempty" jsonschema:"Provenance source"`
	Weight   float64                `json:"weight,omitempty" jsonschema:"Importance weight between 0.0 and 1.0"`
}

// KGEdgeInput defines edge properties for tzro_kg_add_entity.
type KGEdgeInput struct {
	ID       string                 `json:"id" jsonschema:"required,Edge unique identifier"`
	EdgeType string                 `json:"edgeType" jsonschema:"required,Edge type e.g. belongs_to, assigned_to, references"`
	SourceID string                 `json:"sourceId" jsonschema:"required,Source node ID"`
	TargetID string                 `json:"targetId" jsonschema:"required,Target node ID"`
	Metadata map[string]interface{} `json:"metadata,omitempty" jsonschema:"Arbitrary edge metadata properties"`
	Weight   float64                `json:"weight,omitempty" jsonschema:"Edge weight between 0.0 and 1.0"`
}

// TzroKgAddEntityArgs defines inputs for tzro_kg_add_entity.
type TzroKgAddEntityArgs struct {
	Node *KGNodeInput `json:"node,omitempty" jsonschema:"Node entity to add/update"`
	Edge *KGEdgeInput `json:"edge,omitempty" jsonschema:"Edge relationship to add/update"`
}

func handleTzroKgAddEntity(ctx context.Context, req *mcp.CallToolRequest, args TzroKgAddEntityArgs) (*mcp.CallToolResult, any, error) {
	if args.Node == nil && args.Edge == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "must provide at least one of node or edge"}`},
			},
			IsError: true,
		}, nil, nil
	}

	if args.Node != nil {
		if strings.TrimSpace(args.Node.ID) == "" || strings.TrimSpace(args.Node.NodeType) == "" || strings.TrimSpace(args.Node.Name) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "node requires non-empty id, nodeType, and name"}`},
				},
				IsError: true,
			}, nil, nil
		}
		var embedding []float32
		if memory.DB.EmbeddingEngine != nil {
			vec, err := memory.DB.EmbeddingEngine.Embed(ctx, args.Node.Name+" "+args.Node.NodeType)
			if err == nil {
				embedding = vec
			}
		}
		weight := args.Node.Weight
		if weight <= 0 {
			weight = 1.0
		}
		node := memory.KGNode{
			ID:        args.Node.ID,
			NodeType:  args.Node.NodeType,
			Name:      args.Node.Name,
			Metadata:  args.Node.Metadata,
			Source:    args.Node.Source,
			Weight:    weight,
			Embedding: embedding,
		}
		if err := memory.DB.AddNode(node); err != nil {
			return nil, nil, err
		}
	}

	if args.Edge != nil {
		if strings.TrimSpace(args.Edge.ID) == "" || strings.TrimSpace(args.Edge.EdgeType) == "" || strings.TrimSpace(args.Edge.SourceID) == "" || strings.TrimSpace(args.Edge.TargetID) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "edge requires non-empty id, edgeType, sourceId, and targetId"}`},
				},
				IsError: true,
			}, nil, nil
		}
		weight := args.Edge.Weight
		if weight <= 0 {
			weight = 1.0
		}
		edge := memory.KGEdge{
			ID:       args.Edge.ID,
			EdgeType: args.Edge.EdgeType,
			SourceID: args.Edge.SourceID,
			TargetID: args.Edge.TargetID,
			Metadata: args.Edge.Metadata,
			Weight:   weight,
		}
		if err := memory.DB.AddEdge(edge); err != nil {
			return nil, nil, err
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: `{"status": "success"}`},
		},
	}, nil, nil
}

// TzroRagContextArgs defines inputs for tzro_rag_context.
type TzroRagContextArgs struct {
	Query    string `json:"query" jsonschema:"required,The user query or prompt to retrieve context for"`
	MaxChars int    `json:"maxChars,omitempty" jsonschema:"Max character limit of the returned context. Default 2000"`
}

func handleTzroRagContext(ctx context.Context, req *mcp.CallToolRequest, args TzroRagContextArgs) (*mcp.CallToolResult, any, error) {
	maxChars := args.MaxChars
	if maxChars <= 0 {
		maxChars = 2000
	}
	ragStr := memory.DB.GetGraphRAGContext(args.Query, maxChars)
	respMap := map[string]interface{}{
		"context": ragStr,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsListArgs defines inputs for tzro_skills_list.
type TzroSkillsListArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max number of skills to return"`
}

func handleTzroSkillsList(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsListArgs) (*mcp.CallToolResult, any, error) {
	skills := memory.DB.GetSkills()
	if args.Limit > 0 && len(skills) > args.Limit {
		skills = skills[:args.Limit]
	}
	respBytes, _ := json.MarshalIndent(skills, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsGetArgs defines inputs for tzro_skills_get.
type TzroSkillsGetArgs struct {
	ID string `json:"id" jsonschema:"required,The skill ID to retrieve"`
}

func handleTzroSkillsGet(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsGetArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.ID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "id cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	s, err := memory.DB.GetSkill(args.ID)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "%s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}
	respBytes, _ := json.MarshalIndent(s, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsRelevantArgs defines inputs for tzro_skills_relevant.
type TzroSkillsRelevantArgs struct {
	Prompt string `json:"prompt" jsonschema:"required,The user prompt or query to match skills against"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of relevant skills to return. Default 5"`
}

func handleTzroSkillsRelevant(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsRelevantArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Prompt) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "prompt cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	skills := memory.DB.GetRelevantSkills(args.Prompt, limit)
	respBytes, _ := json.MarshalIndent(skills, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsAddArgs defines inputs for tzro_skills_add.
type TzroSkillsAddArgs struct {
	Name               string `json:"name" jsonschema:"required,The name of the micro-skill/SOP"`
	TriggerDescription string `json:"triggerDescription" jsonschema:"required,Description of scenarios that trigger this SOP"`
	SOPContent         string `json:"sopContent" jsonschema:"required,The step-by-step Standard Operating Procedure content in Markdown"`
}

func handleTzroSkillsAdd(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsAddArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Name) == "" || strings.TrimSpace(args.TriggerDescription) == "" || strings.TrimSpace(args.SOPContent) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "name, triggerDescription, and sopContent are required and cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	s := &memory.Skill{
		Name:               args.Name,
		TriggerDescription: args.TriggerDescription,
		SOPContent:         args.SOPContent,
	}
	if err := memory.DB.AddSkill(s); err != nil {
		return nil, nil, err
	}
	respMap := map[string]interface{}{
		"status": "success",
		"skill":  s,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroHookArgs defines the inputs for the merged tzro_hook tool.
type TzroHookArgs struct {
	Action string `json:"action" jsonschema:"required,Action to perform: list or approve"`
	TaskID string `json:"taskId,omitempty" jsonschema:"The task ID to approve (required for approve action)"`
	NodeID string `json:"nodeId,omitempty" jsonschema:"The node ID to approve (required for approve action)"`
}

func handleTzroHook(ctx context.Context, req *mcp.CallToolRequest, args TzroHookArgs) (*mcp.CallToolResult, any, error) {
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list":
		return handleTzroHookList(ctx, req, TzroHookListArgs{})
	case "approve":
		return handleTzroHookApprove(ctx, req, TzroHookApproveArgs{
			TaskID: args.TaskID,
			NodeID: args.NodeID,
		})
	default:
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "unknown action '%s'. Valid actions: list, approve"}`, args.Action)},
			},
			IsError: true,
		}, nil, nil
	}
}

// TzroHookListArgs defines inputs for tzro_hook_list.
type TzroHookListArgs struct{}

func handleTzroHookList(ctx context.Context, req *mcp.CallToolRequest, args TzroHookListArgs) (*mcp.CallToolResult, any, error) {
	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var list []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "human_approval" && n.Type == "approval_request" {
			list = append(list, n)
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroHookApproveArgs defines inputs for tzro_hook_approve.
type TzroHookApproveArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to approve"`
	NodeID string `json:"nodeId" jsonschema:"required,The node ID to approve"`
}

func handleTzroHookApprove(ctx context.Context, req *mcp.CallToolRequest, args TzroHookApproveArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.TaskID) == "" || strings.TrimSpace(args.NodeID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId and nodeId are required"}`},
			},
			IsError: true,
		}, nil, nil
	}

	if isDaemonRunning() {
		type ApproveRequest struct {
			TaskID string `json:"taskId"`
			NodeID string `json:"nodeId"`
		}
		reqBody := ApproveRequest{
			TaskID: args.TaskID,
			NodeID: args.NodeID,
		}
		respBytes, err := proxyToDaemon("/api/tasks/approve", "POST", reqBody)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "daemon proxy failed: %v"}`, err)},
				},
				IsError: true,
			}, nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil
	}

	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var target *memory.DurableNotification
	for _, n := range notifs {
		if n.TaskID == args.TaskID && n.TargetID == args.NodeID && n.Source == "human_approval" && n.Type == "approval_request" {
			target = &n
			break
		}
	}
	if target == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "unread approval request for task '%s' node '%s' not found"}`, args.TaskID, args.NodeID)},
			},
			IsError: true,
		}, nil, nil
	}

	// Update notification status to approved
	if err := memory.DB.UpdateNotificationStatus(target.ID, "approved"); err != nil {
		return nil, nil, err
	}

	// Trigger task resume in the background
	go func() {
		_ = runResumeTask(context.Background(), args.TaskID)
	}()

	respMap := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Node %s approved and task resume triggered.", args.NodeID),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroResumeArgs defines inputs for tzro_resume.
type TzroResumeArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to resume"`
}

func handleTzroResume(ctx context.Context, req *mcp.CallToolRequest, args TzroResumeArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.TaskID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	if isDaemonRunning() {
		type ResumeRequest struct {
			TaskID string `json:"taskId"`
		}
		reqBody := ResumeRequest{
			TaskID: args.TaskID,
		}
		respBytes, err := proxyToDaemon("/api/tasks/resume", "POST", reqBody)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "daemon proxy failed: %v"}`, err)},
				},
				IsError: true,
			}, nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil
	}

	go func() {
		_ = runResumeTask(context.Background(), args.TaskID)
	}()

	respMap := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Task %s resume triggered in background.", args.TaskID),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

func runResumeTask(ctx context.Context, taskID string) error {
	db := memory.DB.RawDB()
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	var graphBytes string
	err := db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", "graph_"+taskID).Scan(&graphBytes)
	if err != nil {
		return fmt.Errorf("failed to load cached graph for task %s: %w", taskID, err)
	}

	var graph compiler.ExecutionGraph
	if err := json.Unmarshal([]byte(graphBytes), &graph); err != nil {
		return fmt.Errorf("failed to unmarshal cached graph: %w", err)
	}

	levels, err := compiler.CompileAndSort(&graph)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}

	// Run graph execution
	return executor.GlobalEngine.ExecuteGraph(ctx, &graph, levels)
}

// TzroObserverEventsArgs defines inputs for tzro_observer_events.
type TzroObserverEventsArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max number of events to return. Default 10"`
}

func handleTzroObserverEvents(ctx context.Context, req *mcp.CallToolRequest, args TzroObserverEventsArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	notifs, err := memory.DB.GetNotifications("")
	if err != nil {
		return nil, nil, err
	}
	var list []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "observer" {
			list = append(list, n)
			if len(list) >= limit {
				break
			}
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroObserverMemoriesArgs defines inputs for tzro_observer_memories.
type TzroObserverMemoriesArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max number of memories to return. Default 10"`
}

func handleTzroObserverMemories(ctx context.Context, req *mcp.CallToolRequest, args TzroObserverMemoriesArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	mems := memory.DB.GetMemories()
	var list []memory.FactMemory
	for _, m := range mems {
		if m.Source == "auto_reflection" {
			list = append(list, m)
			if len(list) >= limit {
				break
			}
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroCompletionArgs defines inputs for tzro_completion.
type TzroCompletionArgs struct {
	SystemPrompt string  `json:"systemPrompt" jsonschema:"required,System prompt to guide the local model behavior"`
	UserPrompt   string  `json:"userPrompt" jsonschema:"required,The user-facing prompt or content to process"`
	JsonSchema   string  `json:"jsonSchema,omitempty" jsonschema:"Optional JSON schema to constrain output via GBNF grammar. When provided the model output is guaranteed valid JSON matching this schema."`
	MaxTokens    int     `json:"maxTokens,omitempty" jsonschema:"Maximum tokens to generate. Default 2048"`
	Temperature  float64 `json:"temperature,omitempty" jsonschema:"Sampling temperature. Default 1.0"`
}

func handleTzroCompletion(ctx context.Context, req *mcp.CallToolRequest, args TzroCompletionArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.UserPrompt) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "userPrompt cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Resolve the active inference backend
	backend := inference.ActiveBackend
	if backend == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "no inference backend configured"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Auto-start if stopped
	if strings.ToLower(backend.Status()) == "stopped" {
		if err := backend.Start(ctx); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "local model failed to start: %s"}`, err.Error())},
				},
				IsError: true,
			}, nil, nil
		}
	}

	result, err := backend.CallModel(ctx, []inference.InferenceMessage{{Role: "system", Content: args.SystemPrompt}, {Role: "user", Content: args.UserPrompt}}, args.JsonSchema)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "local model inference failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	respMap := map[string]interface{}{
		"content":          result.Content,
		"promptTokens":     result.PromptTokens,
		"completionTokens": result.CompletionTokens,
		"durationSeconds":  result.DurationSeconds,
		"tokensPerSecond":  result.TokensPerSecond,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroClassificationArgs defines inputs for tzro_classification.
type TzroClassificationArgs struct {
	Input      string   `json:"input" jsonschema:"required,The text content to classify"`
	Categories []string `json:"categories" jsonschema:"required,The set of valid classification labels"`
	Context    string   `json:"context,omitempty" jsonschema:"Optional context or instructions to guide classification"`
}

func handleTzroClassification(ctx context.Context, req *mcp.CallToolRequest, args TzroClassificationArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Input) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "input cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	if len(args.Categories) < 2 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "at least 2 categories are required"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Build classification system prompt
	systemPrompt := "You are a classification agent. Classify the input into exactly one of the provided categories. Respond with ONLY valid JSON matching the schema."
	if args.Context != "" {
		systemPrompt += "\n\nAdditional context: " + args.Context
	}

	// Build user prompt with categories listed
	userPrompt := fmt.Sprintf("Classify this input:\n\n%s\n\nValid categories: %s", args.Input, strings.Join(args.Categories, ", "))

	// Build JSON schema with enum constraint — GBNF guarantees the output is one of the valid labels
	categoriesJSON, _ := json.Marshal(args.Categories)
	jsonSchema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"category": {
				"type": "string",
				"enum": %s
			},
			"confidence": {
				"type": "number",
				"minimum": 0.0,
				"maximum": 1.0
			},
			"reasoning": {
				"type": "string"
			}
		},
		"required": ["category", "confidence", "reasoning"]
	}`, string(categoriesJSON))

	// Route classification to the router sidecar — GBNF-constrained, fast output
	result, err := inference.CallRouter(ctx, []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}, jsonSchema)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "classification inference failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// Parse and re-envelope the result with metrics
	var classResult map[string]interface{}
	if json.Unmarshal([]byte(result.Content), &classResult) != nil {
		// If parsing fails, return raw content
		classResult = map[string]interface{}{"raw": result.Content}
	}

	respMap := map[string]interface{}{
		"classification":   classResult,
		"promptTokens":     result.PromptTokens,
		"completionTokens": result.CompletionTokens,
		"durationSeconds":  result.DurationSeconds,
		"tokensPerSecond":  result.TokensPerSecond,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// delegationHint returns a description suffix for completion/classification tools
// based on the configured delegation mode.
func delegationHint() string {
	switch config.GetDelegationMode() {
	case "conservative":
		return " Use sparingly — prefer handling tasks directly unless the task is purely mechanical."
	case "aggressive":
		return " Prefer this tool over direct processing for ANY task that does not require cutting-edge reasoning, real-time data, or multi-modal understanding."
	default: // balanced
		return ""
	}
}

// runDelegationHint returns a description suffix for the tzro_run tool
// based on the configured delegation mode. In aggressive mode it steers
// the cloud model to delegate multi-step work (research, exploration,
// data gathering, automation) through DAG execution rather than
// executing tool calls manually.
func runDelegationHint() string {
	switch config.GetDelegationMode() {
	case "conservative":
		return " Only use for explicitly requested multi-tool workflows."
	case "aggressive":
		return " STRONGLY PREFERRED: Delegate ANY multi-step task to this tool rather than executing steps manually. This includes research, codebase exploration, data gathering, environment inspection, and automation. If the task involves more than 2 sequential actions, use this tool."
	default: // balanced
		return ""
	}
}

// getDaemonPort returns the daemon port string for inclusion in tool results,
// allowing the MCP App UI to connect to the correct daemon instance.
func getDaemonPort() string {
	daemonURL := config.GetDaemonURL()
	// Extract port from URL like "http://127.0.0.1:8080"
	if idx := strings.LastIndex(daemonURL, ":"); idx != -1 {
		return daemonURL[idx+1:]
	}
	return "8080"
}

// registerTools registers all tools with the MCP server.
// tzro_activity_report tool definition

// TzroActivityReportArgs defines the inputs for reporting agent activity to the Sentinel.
type TzroActivityReportArgs struct {
	Activity     string   `json:"activity" jsonschema:"required,Brief description of current work"`
	FilesTouched []string `json:"filesTouched,omitempty" jsonschema:"File paths read or modified"`
	ToolsUsed    []string `json:"toolsUsed,omitempty" jsonschema:"Tools called since last report"`
}

func handleTzroActivityReport(ctx context.Context, req *mcp.CallToolRequest, args TzroActivityReportArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Activity) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "activity cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	report := sentinel.ActivityReport{
		Activity:     args.Activity,
		FilesTouched: args.FilesTouched,
		ToolsUsed:    args.ToolsUsed,
		Timestamp:    time.Now().Unix(),
	}

	sentinel.DefaultAgent.IngestActivityReport(report)

	result, _ := json.Marshal(map[string]string{"status": "acknowledged"})
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(result)},
		},
	}, nil, nil
}

// tzro_sentinel_alerts tool definition

// TzroSentinelAlertsArgs defines the inputs for querying Sentinel alerts.
type TzroSentinelAlertsArgs struct {
	Status string `json:"status,omitempty" jsonschema:"Filter by status: unread, read, dismissed. Default unread"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max alerts to return. Default 10"`
}

func handleTzroSentinelAlerts(ctx context.Context, req *mcp.CallToolRequest, args TzroSentinelAlertsArgs) (*mcp.CallToolResult, any, error) {
	status := args.Status
	if status == "" {
		status = "unread"
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	notifs, err := memory.DB.GetNotifications(status)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to query alerts: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// Filter to sentinel-sourced notifications only
	var sentinelAlerts []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "sentinel" {
			sentinelAlerts = append(sentinelAlerts, n)
			if len(sentinelAlerts) >= limit {
				break
			}
		}
	}

	// Mark returned unread alerts as read
	if status == "unread" {
		for _, n := range sentinelAlerts {
			_ = memory.DB.UpdateNotificationStatus(n.ID, "read")
		}
	}

	if sentinelAlerts == nil {
		sentinelAlerts = []memory.DurableNotification{}
	}

	result, _ := json.Marshal(map[string]interface{}{
		"alerts": sentinelAlerts,
		"count":  len(sentinelAlerts),
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(result)},
		},
	}, nil, nil
}

// tzro_sentinel_wake tool definition

// TzroSentinelWakeArgs defines the inputs for manually waking the Sentinel Agent.
type TzroSentinelWakeArgs struct {
	ContextHint string `json:"contextHint,omitempty" jsonschema:"Optional hint to bias Sentinel analysis toward a specific topic, e.g. 'check for security issues in auth module'"`
}

func handleTzroSentinelWake(ctx context.Context, req *mcp.CallToolRequest, args TzroSentinelWakeArgs) (*mcp.CallToolResult, any, error) {
	alerted := sentinel.DefaultAgent.Wake(ctx, args.ContextHint)

	respMap := map[string]interface{}{
		"status":        "completed",
		"alertProduced": alerted,
	}
	if args.ContextHint != "" {
		respMap["contextHint"] = args.ContextHint
	}

	result, _ := json.Marshal(respMap)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(result)},
		},
	}, nil, nil
}

// tzro_schedule tool definition

// ScheduleTaskInput defines a single task step within a scheduled workflow.
type ScheduleTaskInput struct {
	ID           string `json:"id" jsonschema:"required,Unique task step identifier"`
	Name         string `json:"name,omitempty" jsonschema:"Human-readable task name"`
	Instructions string `json:"instructions" jsonschema:"required,Natural language instructions for this task step. Use {{tasks.<id>.output}} to reference upstream task outputs."`
	Dependencies string `json:"dependencies,omitempty" jsonschema:"Comma-separated IDs of prerequisite task steps that must complete first"`
}

// TzroScheduleArgs defines inputs for the tzro_schedule tool.
type TzroScheduleArgs struct {
	Action      string              `json:"action" jsonschema:"required,Action to perform: create, list, toggle, delete, trigger"`
	Name        string              `json:"name,omitempty" jsonschema:"Workflow name (required for create)"`
	Description string              `json:"description,omitempty" jsonschema:"Human-readable workflow description"`
	Cron        string              `json:"cron,omitempty" jsonschema:"Standard 5-field cron expression e.g. '0 8 * * *' for daily at 8am (required for create)"`
	Tasks       []ScheduleTaskInput `json:"tasks,omitempty" jsonschema:"Array of task steps to execute on each trigger (required for create)"`
	WorkflowID  string              `json:"workflowId,omitempty" jsonschema:"Workflow ID (required for toggle, delete, trigger)"`
	Status      string              `json:"status,omitempty" jsonschema:"Set to active or paused (used with toggle action)"`
}

func handleTzroSchedule(ctx context.Context, req *mcp.CallToolRequest, args TzroScheduleArgs) (*mcp.CallToolResult, any, error) {
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "create":
		return handleScheduleCreate(ctx, args)
	case "list":
		return handleScheduleList(ctx)
	case "toggle":
		return handleScheduleToggle(ctx, args)
	case "delete":
		return handleScheduleDelete(ctx, args)
	case "trigger":
		return handleScheduleTrigger(ctx, args)
	default:
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "unknown action '%s'. Valid actions: create, list, toggle, delete, trigger"}`, args.Action)},
			},
			IsError: true,
		}, nil, nil
	}
}

func handleScheduleCreate(ctx context.Context, args TzroScheduleArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Name) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "name is required for create action"}`}},
			IsError: true,
		}, nil, nil
	}
	if strings.TrimSpace(args.Cron) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "cron expression is required for create action"}`}},
			IsError: true,
		}, nil, nil
	}
	if len(args.Tasks) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "at least one task is required for create action"}`}},
			IsError: true,
		}, nil, nil
	}

	// Validate cron expression by computing next run
	now := time.Now()
	nextRun := workflow.ParseCronNext(args.Cron, now)
	if nextRun.IsZero() {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "invalid cron expression: %s"}`, args.Cron)}},
			IsError: true,
		}, nil, nil
	}

	// Validate task IDs are unique
	taskIDs := make(map[string]bool, len(args.Tasks))
	for _, t := range args.Tasks {
		if strings.TrimSpace(t.ID) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "all tasks must have a non-empty id"}`}},
				IsError: true,
			}, nil, nil
		}
		if strings.TrimSpace(t.Instructions) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "task '%s' must have non-empty instructions"}`, t.ID)}},
				IsError: true,
			}, nil, nil
		}
		if taskIDs[t.ID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "duplicate task id: %s"}`, t.ID)}},
				IsError: true,
			}, nil, nil
		}
		taskIDs[t.ID] = true
	}

	wfID := fmt.Sprintf("wf_%d", time.Now().UnixNano())
	nowUnix := now.Unix()

	wf := memory.WorkflowDefinition{
		ID:            wfID,
		Name:          args.Name,
		Description:   args.Description,
		TriggerType:   "cron",
		TriggerConfig: args.Cron,
		Status:        "active",
		NextRunAt:     nextRun.Unix(),
		CreatedAt:     nowUnix,
		UpdatedAt:     nowUnix,
	}

	var wfTasks []memory.WorkflowTask
	for _, t := range args.Tasks {
		wfTasks = append(wfTasks, memory.WorkflowTask{
			WorkflowID:     wfID,
			TaskTemplateID: t.ID,
			Name:           t.Name,
			Instructions:   t.Instructions,
			Dependencies:   t.Dependencies,
		})
	}

	if err := memory.DB.SaveWorkflow(wf, wfTasks); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to save workflow: %s"}`, err.Error())}},
			IsError: true,
		}, nil, nil
	}

	respMap := map[string]interface{}{
		"status":     "created",
		"workflowId": wfID,
		"name":       args.Name,
		"cron":       args.Cron,
		"nextRunAt":  nextRun.Format(time.RFC3339),
		"taskCount":  len(wfTasks),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

func handleScheduleList(ctx context.Context) (*mcp.CallToolResult, any, error) {
	defs, err := memory.DB.GetWorkflows()
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to list workflows: %s"}`, err.Error())}},
			IsError: true,
		}, nil, nil
	}

	type WorkflowSummary struct {
		ID            string                `json:"id"`
		Name          string                `json:"name"`
		Description   string                `json:"description"`
		TriggerType   string                `json:"triggerType"`
		TriggerConfig string                `json:"triggerConfig"`
		Status        string                `json:"status"`
		NextRunAt     string                `json:"nextRunAt,omitempty"`
		Tasks         []memory.WorkflowTask `json:"tasks"`
	}

	var result []WorkflowSummary
	for _, d := range defs {
		tasks, _ := memory.DB.GetWorkflowTasks(d.ID)
		var nextRunStr string
		if d.NextRunAt > 0 {
			nextRunStr = time.Unix(d.NextRunAt, 0).Format(time.RFC3339)
		}
		result = append(result, WorkflowSummary{
			ID:            d.ID,
			Name:          d.Name,
			Description:   d.Description,
			TriggerType:   d.TriggerType,
			TriggerConfig: d.TriggerConfig,
			Status:        d.Status,
			NextRunAt:     nextRunStr,
			Tasks:         tasks,
		})
	}

	if result == nil {
		result = []WorkflowSummary{}
	}

	respBytes, _ := json.MarshalIndent(map[string]interface{}{
		"workflows": result,
		"count":     len(result),
	}, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

func handleScheduleToggle(ctx context.Context, args TzroScheduleArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "workflowId is required for toggle action"}`}},
			IsError: true,
		}, nil, nil
	}

	status := strings.ToLower(strings.TrimSpace(args.Status))
	if status != "active" && status != "paused" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "status must be 'active' or 'paused' for toggle action"}`}},
			IsError: true,
		}, nil, nil
	}

	// Update next run time when activating
	if status == "active" {
		defs, _ := memory.DB.GetWorkflows()
		for _, d := range defs {
			if d.ID == args.WorkflowID {
				if d.TriggerType == "cron" && d.TriggerConfig != "" {
					next := workflow.ParseCronNext(d.TriggerConfig, time.Now())
					if !next.IsZero() {
						_ = memory.DB.UpdateWorkflowNextRun(args.WorkflowID, next.Unix())
					}
				}
				break
			}
		}
	} else {
		_ = memory.DB.UpdateWorkflowNextRun(args.WorkflowID, 0)
	}

	if err := memory.DB.ToggleWorkflow(args.WorkflowID, status); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to toggle workflow: %s"}`, err.Error())}},
			IsError: true,
		}, nil, nil
	}

	respBytes, _ := json.MarshalIndent(map[string]string{
		"status":     "success",
		"workflowId": args.WorkflowID,
		"newStatus":  status,
	}, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

func handleScheduleDelete(ctx context.Context, args TzroScheduleArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "workflowId is required for delete action"}`}},
			IsError: true,
		}, nil, nil
	}

	if err := memory.DB.DeleteWorkflow(args.WorkflowID); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to delete workflow: %s"}`, err.Error())}},
			IsError: true,
		}, nil, nil
	}

	respBytes, _ := json.MarshalIndent(map[string]string{
		"status":     "deleted",
		"workflowId": args.WorkflowID,
	}, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

func handleScheduleTrigger(ctx context.Context, args TzroScheduleArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"error": "workflowId is required for trigger action"}`}},
			IsError: true,
		}, nil, nil
	}

	go func(id string) {
		if err := workflow.ExecuteWorkflow(context.Background(), id); err != nil {
			fmt.Fprintf(os.Stderr, "[tzro_schedule] Manual trigger failed for workflow %s: %v\n", id, err)
		}
	}(args.WorkflowID)

	respBytes, _ := json.MarshalIndent(map[string]string{
		"status":     "triggered",
		"workflowId": args.WorkflowID,
	}, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

// tzro_workflow tool definition

// WorkflowNodeInput defines a single node in a user-specified DAG workflow.
type WorkflowNodeInput struct {
	ID                  string              `json:"id" jsonschema:"required,Unique node identifier"`
	Type                string              `json:"type" jsonschema:"required,Node type: action, probe, deterministic, branch, merge, synthesis, hypothesis"`
	Action              string              `json:"action,omitempty" jsonschema:"Target tool name for action/deterministic nodes"`
	Instructions        string              `json:"instructions" jsonschema:"required,Natural language step instructions. Use double-braces variable binding to reference upstream node outputs."`
	AllowedTools        []string            `json:"allowedTools,omitempty" jsonschema:"Whitelist of permitted tools for this node"`
	StaticArgs          string              `json:"staticArgs,omitempty" jsonschema:"Pre-known arguments as a JSON string"`
	RequireApproval     bool                `json:"requireApproval,omitempty" jsonschema:"Pause and wait for human approval before executing"`
	ActivationThreshold float64             `json:"activationThreshold,omitempty" jsonschema:"Sufficiency gate 0.0-1.0. 0.0 disables Edge Thoughts."`
	ProbeConfig         *WorkflowProbeInput `json:"probeConfig,omitempty" jsonschema:"Configuration for probe nodes. Required when type is probe."`
}

// WorkflowEdgeInput defines a directed edge between two nodes.
type WorkflowEdgeInput struct {
	SourceID string `json:"sourceId" jsonschema:"required,Source node ID"`
	TargetID string `json:"targetId" jsonschema:"required,Target node ID"`
}

// WorkflowProbeInput configures a Probe Node's Thought Chain execution loop.
type WorkflowProbeInput struct {
	Goal         string   `json:"goal" jsonschema:"required,The exploration objective"`
	AllowedTools []string `json:"allowedTools" jsonschema:"required,Tools the probe may use"`
	StepBudget   int      `json:"stepBudget,omitempty" jsonschema:"Maximum number of Thought Chain steps before forced synthesis. Default 20"`
	CompactEvery int      `json:"compactEvery,omitempty" jsonschema:"Rolling compaction frequency in steps. Default 3"`
}

// TzroWorkflowArgs defines the inputs for creating and executing a DAG workflow directly.
type TzroWorkflowArgs struct {
	Nodes          []WorkflowNodeInput `json:"nodes" jsonschema:"required,Array of DAG nodes defining the workflow steps"`
	Edges          []WorkflowEdgeInput `json:"edges,omitempty" jsonschema:"Array of directed edges defining node dependencies"`
	MutationBudget int                 `json:"mutationBudget,omitempty" jsonschema:"Max dynamic node spawns for activation thresholds. Default 0 (disabled)"`
	MaxCycles      int                 `json:"maxCycles,omitempty" jsonschema:"Max execution cycles. Default 5"`
	Timeout        int                 `json:"timeout,omitempty" jsonschema:"Execution timeout in seconds before switching to async. Default 60"`
	DryRun         bool                `json:"dryRun,omitempty" jsonschema:"If true validates and compiles the graph without executing. Returns execution levels."`
}

func handleTzroWorkflow(ctx context.Context, req *mcp.CallToolRequest, args TzroWorkflowArgs) (*mcp.CallToolResult, any, error) {
	// --- Validation ---
	if len(args.Nodes) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "nodes array cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Check for unique node IDs and build lookup set
	nodeIDs := make(map[string]bool, len(args.Nodes))
	for _, n := range args.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "all nodes must have a non-empty id"}`},
				},
				IsError: true,
			}, nil, nil
		}
		if nodeIDs[n.ID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "duplicate node id: %s"}`, n.ID)},
				},
				IsError: true,
			}, nil, nil
		}
		nodeIDs[n.ID] = true

		// Validate probe nodes have probeConfig
		if n.Type == "probe" && n.ProbeConfig == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "probe node '%s' requires a probeConfig"}`, n.ID)},
				},
				IsError: true,
			}, nil, nil
		}
	}

	// Validate edge references
	for _, e := range args.Edges {
		if !nodeIDs[e.SourceID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "edge references non-existent source node: %s"}`, e.SourceID)},
				},
				IsError: true,
			}, nil, nil
		}
		if !nodeIDs[e.TargetID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "edge references non-existent target node: %s"}`, e.TargetID)},
				},
				IsError: true,
			}, nil, nil
		}
	}

	// --- Map to compiler types ---
	graphNodes := make([]compiler.GraphNode, 0, len(args.Nodes))
	for _, n := range args.Nodes {
		gn := compiler.GraphNode{
			ID:                  n.ID,
			Type:                n.Type,
			Action:              n.Action,
			Instructions:        n.Instructions,
			AllowedTools:        n.AllowedTools,
			StaticArgs:          n.StaticArgs,
			RequireApproval:     n.RequireApproval,
			ActivationThreshold: n.ActivationThreshold,
			Status:              "pending",
		}
		if n.ProbeConfig != nil {
			stepBudget := n.ProbeConfig.StepBudget
			if stepBudget <= 0 {
				stepBudget = 20
			}
			compactEvery := n.ProbeConfig.CompactEvery
			if compactEvery <= 0 {
				compactEvery = 3
			}
			gn.ProbeConfig = &compiler.ProbeConfig{
				Goal:         n.ProbeConfig.Goal,
				AllowedTools: n.ProbeConfig.AllowedTools,
				StepBudget:   stepBudget,
				CompactEvery: compactEvery,
			}
		}
		graphNodes = append(graphNodes, gn)
	}

	graphEdges := make([]compiler.GraphEdge, 0, len(args.Edges))
	for _, e := range args.Edges {
		graphEdges = append(graphEdges, compiler.GraphEdge{
			SourceID: e.SourceID,
			TargetID: e.TargetID,
		})
	}

	taskID := uuid.New().String()
	maxCycles := args.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 5
	}

	graph := &compiler.ExecutionGraph{
		TaskID:    taskID,
		Nodes:     graphNodes,
		Edges:     graphEdges,
		MaxCycles: maxCycles,
		CreatedAt: time.Now().Unix(),
	}

	// Set mutation budget if provided
	if args.MutationBudget > 0 {
		graph.MutationBudget = &compiler.MutationBudget{
			MaxSpawns:       args.MutationBudget,
			RemainingSpawns: args.MutationBudget,
		}
	}

	// --- SCT Expansion ---
	expanded, err := compiler.ExpandToSCTGraph(graph, tools.GetSchema)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "SCT expansion failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// --- Kahn Compile ---
	levels, err := compiler.CompileAndSort(expanded)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "%s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// --- Dry Run ---
	if args.DryRun {
		respMap := map[string]interface{}{
			"taskId":          taskID,
			"status":          "dry_run",
			"executionLevels": levels,
			"nodeCount":       len(expanded.Nodes),
			"edgeCount":       len(expanded.Edges),
		}
		respBytes, _ := json.MarshalIndent(respMap, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil
	}

	// --- Execute with timeout/async fallback (same pattern as tzro_run) ---
	timeoutSec := args.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	// Start SubagentChannel for real-time event delivery to the harness.
	// Node count is known from the compiled graph.
	wfCh := startSubagentChannel(req, mcpServer, taskID, float64(len(expanded.Nodes)))
	if wfCh != nil {
		defer wfCh.Close()
		channel.GlobalChannelToolHook.RegisterChannel(taskID, wfCh)
		defer channel.GlobalChannelToolHook.UnregisterChannel(taskID)
	}

	type execResult struct {
		nodes []memory.NodeState
		err   error
	}

	doneChan := make(chan execResult, 1)

	go func() {
		execErr := executor.GlobalEngine.ExecuteGraph(context.Background(), expanded, levels)
		nodes := memory.DB.GetAllNodeStates(taskID)
		doneChan <- execResult{nodes: nodes, err: execErr}
	}()

	select {
	case res := <-doneChan:
		status := "completed"
		var errMsg string
		if res.err != nil {
			status = "failed"
			errMsg = res.err.Error()
		}

		respMap := map[string]interface{}{
			"taskId": taskID,
			"status": status,
			"nodes":  res.nodes,
		}
		if errMsg != "" {
			respMap["error"] = errMsg
		}

		respBytes, _ := json.MarshalIndent(respMap, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil

	case <-time.After(time.Duration(timeoutSec) * time.Second):
		// Timeout hit: check if nodes were persisted
		nodes := memory.DB.GetAllNodeStates(taskID)
		if len(nodes) == 0 {
			// Brief grace: wait up to 5 more seconds
			graceTimer := time.After(5 * time.Second)
			graceTicker := time.NewTicker(500 * time.Millisecond)
			defer graceTicker.Stop()

		graceLoop:
			for {
				select {
				case res := <-doneChan:
					status := "completed"
					var errMsg string
					if res.err != nil {
						status = "failed"
						errMsg = res.err.Error()
					}
					respMap := map[string]interface{}{
						"taskId": taskID,
						"status": status,
						"nodes":  res.nodes,
					}
					if errMsg != "" {
						respMap["error"] = errMsg
					}
					respBytes, _ := json.MarshalIndent(respMap, "", "  ")
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: string(respBytes)},
						},
					}, nil, nil

				case <-graceTicker.C:
					nodes = memory.DB.GetAllNodeStates(taskID)
					if len(nodes) > 0 {
						break graceLoop
					}

				case <-graceTimer:
					respMap := map[string]interface{}{
						"taskId":  taskID,
						"status":  "compiling",
						"message": "Workflow graph is still being compiled. Check tzro_status after a delay.",
					}
					respBytes, _ := json.MarshalIndent(respMap, "", "  ")
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: string(respBytes)},
						},
					}, nil, nil
				}
			}
		}

		// Nodes exist — execution is in progress
		respMap := map[string]interface{}{
			"taskId": taskID,
			"status": "running",
		}
		respBytes, _ := json.MarshalIndent(respMap, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil
	}
}

// tzro_dashboard tool definition

type TzroDashboardArgs struct{}

func handleTzroDashboard(ctx context.Context, req *mcp.CallToolRequest, args TzroDashboardArgs) (*mcp.CallToolResult, any, error) {
	// Check for a running dashboard via lock file
	lock := config.ReadDashboardLock()

	spec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		respBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
			IsError: true,
		}, nil, nil
	}

	url := config.GetDaemonURL() + "/dashboard/"

	if spec == nil {
		// Trigger initial generation in the background
		taskID := uuid.New().String()
		go func() {
			_, _, _ = task.Execute(context.Background(), "Generate system dashboard spec", task.ExecuteOptions{
				TaskID:       taskID,
				IntentType:   "workflow",
				IsForeground: false,
			})
		}()

		resp := map[string]interface{}{
			"url":              url,
			"status":           "generating",
			"message":          "No dashboard spec found. Triggered initial generation. Please check back shortly.",
			"taskId":           taskID,
			"dashboardRunning": lock != nil,
		}
		if lock != nil {
			resp["dashboardPid"] = lock.PID
			resp["dashboardPort"] = lock.Port
		}
		respBytes, _ := json.Marshal(resp)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
		}, nil, nil
	}

	ageSeconds := time.Now().Unix() - spec.GeneratedAt
	resp := map[string]interface{}{
		"url":              url,
		"status":           "active",
		"specId":           spec.ID,
		"generatedAt":      spec.GeneratedAt,
		"ageSeconds":       ageSeconds,
		"generatorTaskId":  spec.GeneratorTaskID,
		"ttlSeconds":       spec.TTLSeconds,
		"dashboardRunning": lock != nil,
	}
	if lock != nil {
		resp["dashboardPid"] = lock.PID
		resp["dashboardPort"] = lock.Port
	}
	respBytes, _ := json.Marshal(resp)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

// tzro_dashboard_regenerate tool definition

type TzroDashboardRegenerateArgs struct {
	Wait bool `json:"wait,omitempty" jsonschema:"Whether to block and wait for the generation to complete"`
}

func handleTzroDashboardRegenerate(ctx context.Context, req *mcp.CallToolRequest, args TzroDashboardRegenerateArgs) (*mcp.CallToolResult, any, error) {
	taskID := uuid.New().String()
	prompt := "Generate system dashboard spec"

	if args.Wait {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		graph, _, err := task.Execute(timeoutCtx, prompt, task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "workflow",
			IsForeground: true,
		})
		if err != nil {
			if timeoutCtx.Err() == context.DeadlineExceeded {
				respBytes, _ := json.Marshal(map[string]interface{}{
					"status": "generating",
					"taskId": taskID,
				})
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
				}, nil, nil
			}
			respBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
				IsError: true,
			}, nil, nil
		}

		nodeSucceeded := false
		// Find the terminal node (no outgoing edges) and check its status
		hasOutgoing := make(map[string]bool)
		for _, edge := range graph.Edges {
			hasOutgoing[edge.SourceID] = true
		}
		for _, node := range graph.Nodes {
			if !hasOutgoing[node.ID] {
				state, ok := memory.DB.GetNodeState(taskID, node.ID)
				if ok && state.Status == "completed" {
					nodeSucceeded = true
				}
			}
		}

		status := "failed"
		if nodeSucceeded {
			status = "completed"
		}

		respBytes, _ := json.Marshal(map[string]interface{}{
			"status": status,
			"taskId": taskID,
		})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
		}, nil, nil
	}

	go func() {
		_, _, _ = task.Execute(context.Background(), prompt, task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "workflow",
			IsForeground: false,
		})
	}()

	respBytes, _ := json.Marshal(map[string]interface{}{
		"status": "generating",
		"taskId": taskID,
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

// tzro_dashboard_spec tool definition

type TzroDashboardSpecArgs struct{}

func handleTzroDashboardSpec(ctx context.Context, req *mcp.CallToolRequest, args TzroDashboardSpecArgs) (*mcp.CallToolResult, any, error) {
	spec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		respBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
			IsError: true,
		}, nil, nil
	}

	if spec == nil {
		respBytes, _ := json.Marshal(map[string]string{"error": "no dashboard spec found"})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: spec.Spec}},
	}, nil, nil
}

func registerTools(server *mcp.Server) {
	// --- Tier 1: First-class tools (high frequency, core execution) ---

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_run",
		Description: "Plan, compile, and execute a durable DAG workflow from a natural language prompt." + runDelegationHint(),
		Meta:        mcp.Meta{"ui": map[string]any{"resourceUri": appResourceURIBase}},
	}, handleTzroRun)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_code",
		Description: "Generate or update a single file via local LLM codegen. Supports two modes: 'full' (whole-file rewrite, default for new/small files) and 'diff' (structured hunk edits, default for files >200 lines). Files >500 lines MUST use diff mode. Pass a spec/JSDoc and filepath.",
		Meta:        mcp.Meta{"ui": map[string]any{"resourceUri": appResourceURIBase}},
	}, handleTzroCode)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_status",
		Description: "Check the execution status, node states, and outcomes of a specific tzro task by its ID.",
	}, handleTzroStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_cancel",
		Description: "Cancel a running task. Terminates the active execution goroutine, marks pending/running nodes " +
			"as cancelled in the database, and emits a task_cancelled event. Also works on zombie tasks " +
			"(stuck in 'running' status with no active goroutine) for SQLite-only cleanup. " +
			"Proactivity Level: L3 (Reversible Action).",
	}, handleTzroCancel)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_list_tasks",
		Description: "List recent planning and execution tasks, optionally filtered by status.",
	}, handleTzroListTasks)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_resume",
		Description: "Manually resume execution of a paused/interrupted workflow task by its ID.",
	}, handleTzroResume)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_workflow",
		Description: "Create and execute a tzro DAG workflow by directly specifying nodes, edges, " +
			"and execution parameters. Bypasses the LLM Strategic Planner — use when you have a " +
			"pre-defined workflow structure. The graph is SCT-expanded (action nodes decomposed into " +
			"bridge/exec pairs) and Kahn-sorted before execution. Supports dry-run validation, " +
			"probe nodes, activation thresholds, mutation budgets, and human-in-the-loop approval gates.",
	}, handleTzroWorkflow)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_restart",
		Description: "Restart the tzro daemon (tzrod) in-place using process re-exec. " +
			"The daemon replaces itself with a fresh copy of the same binary, preserving the PID and pidlock. " +
			"In-flight tasks are interrupted and recovered automatically on boot. " +
			"The inference sidecar survives via process adoption. " +
			"Returns the restart status and previous uptime. Proactivity Level: L3 (Reversible Action).",
	}, handleTzroRestart)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_dashboard",
		Description: "Check spec status and return the HTTP dashboard URL, age, and status. Triggers initial generation if no spec exists.",
	}, handleTzroDashboard)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_schedule",
		Description: "Create, list, toggle, delete, or manually trigger scheduled workflows. " +
			"Scheduled workflows use standard 5-field cron expressions and run durably inside the tzro daemon — " +
			"they persist across restarts and do not depend on any conversation or agent session. " +
			"Actions: create (requires name, cron, tasks), list, toggle (requires workflowId, status), " +
			"delete (requires workflowId), trigger (requires workflowId for manual immediate execution).",
	}, handleTzroSchedule)

	// --- Tier 2: Merged action-dispatch tools ---

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_hook",
		Description: "Manage human-in-the-loop workflow approval hooks. " +
			"Actions: list (show pending approval requests), approve (approve a paused step by taskId and nodeId).",
	}, handleTzroHook)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_model",
		Description: "Manage local LLM models. " +
			"Actions: list (show available GGUF models with download status and active indicator), " +
			"set (change active model via modelId, ggufModelPath, or downloadUrl).",
	}, handleTzroModel)

	// --- Tier 3: Generic API escape hatch ---

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_api",
		Description: "Generic API tool for less-frequently-used operations. Call named functions directly " +
			"(completion, classification, compact, web_search, memory_query, memory_ingest, " +
			"kg_neighborhood, kg_add_entity, rag_context, skills_list, skills_get, skills_relevant, " +
			"skills_add, observer_events, observer_memories, activity_report, sentinel_alerts, " +
			"sentinel_wake, configure_tools, schedule, apps_list, apps_install, apps_uninstall, " +
			"dashboard_regenerate, dashboard_spec) or proxy to daemon HTTP endpoints (paths starting with /).",
	}, handleTzroApi)

	// --- Infrastructure: Client tool dispatch (MCP protocol-level, not user-facing) ---

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_register_client_tools",
		Description: "Register dynamic client-side tool definitions that the tzro planning engine can leverage.",
	}, handleTzroRegisterClientTools)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_client_tool_list",
		Description: "List pending client-side tool execution requests awaiting outcomes.",
	}, handleTzroClientToolList)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_client_tool_submit",
		Description: "Submit execution outcomes for a client-side tool to resume the paused workflow.",
	}, handleTzroClientToolSubmit)
}

// --- Conversation Compaction MCP Tool ---

// TzroCompactArgs defines the inputs for the tzro_compact tool.
type TzroCompactArgs struct {
	Messages  []CompactMessage `json:"messages" jsonschema:"required,Conversation messages to compact"`
	FocusHint string           `json:"focusHint,omitempty" jsonschema:"Optional guidance on what to prioritize preserving during compaction"`
}

// CompactMessage represents a single message in the conversation to compact.
type CompactMessage struct {
	Role    string `json:"role"` // "user" | "assistant" | "system"
	Content string `json:"content"`
}

func handleTzroCompact(ctx context.Context, req *mcp.CallToolRequest, args TzroCompactArgs) (*mcp.CallToolResult, any, error) {
	if len(args.Messages) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "messages array cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Build the conversation text for compaction
	var conversationText strings.Builder
	inputTokens := 0
	for _, msg := range args.Messages {
		conversationText.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
		inputTokens += len(msg.Content) / 4 // rough token estimate
	}

	// Build the compaction system prompt with conversation-aware heuristics
	systemPrompt := `You are a conversation compactor. Compress the following conversation into a focused summary.

Apply these rules:
- PRESERVE verbatim: User corrections ("actually, I meant..."), explicit requirements, constraints, final decisions, confirmed choices
- PRESERVE in compressed form: Technical details referenced in decisions
- COMPRESS to key conclusions: Assistant reasoning and explanations
- COMPRESS to outcome only: Exploratory back-and-forth
- DROP: Pleasantries, acknowledgments, repeated explanations
- DEDUPLICATE: Repeated explanations to single instance

Output only the compacted summary text, no preamble or labels.`

	if args.FocusHint != "" {
		systemPrompt += fmt.Sprintf("\n\nFocus hint: Prioritize preserving content related to: %s", args.FocusHint)
	}

	// Call the local model for compaction
	backend := inference.ActiveBackend
	if backend == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "no active inference backend available for compaction"}`},
			},
			IsError: true,
		}, nil, nil
	}

	result, err := backend.CallModel(ctx, []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: conversationText.String()},
	}, "")
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "compaction failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	outputTokens := result.CompletionTokens
	if outputTokens == 0 {
		outputTokens = len(result.Content) / 4 // fallback estimate
	}

	compressionRatio := 0.0
	if outputTokens > 0 {
		compressionRatio = float64(inputTokens) / float64(outputTokens)
	}

	response := map[string]interface{}{
		"summary": strings.TrimSpace(result.Content),
		"stats": map[string]interface{}{
			"inputTokens":      inputTokens,
			"outputTokens":     outputTokens,
			"compressionRatio": compressionRatio,
		},
	}

	respBytes, _ := json.MarshalIndent(response, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// --- Agent App Package Manager MCP tool handlers ---

// getOrInitPackageManager creates a packagemanager.Manager using the shared memory DB.
func getOrInitPackageManager() (*packagemanager.Manager, error) {
	db := memory.DB.RawDB()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	appsDir := config.ResolvePath("apps")
	mgr := packagemanager.NewManager(db, internalmcp.GlobalRegistry, appsDir)
	if err := mgr.InitSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize package manager schema: %w", err)
	}
	return mgr, nil
}

// TzroAppsListArgs defines the inputs for listing installed Agent Apps.
type TzroAppsListArgs struct{}

func handleTzroAppsList(ctx context.Context, req *mcp.CallToolRequest, args TzroAppsListArgs) (*mcp.CallToolResult, any, error) {
	mgr, err := getOrInitPackageManager()
	if err != nil {
		return nil, nil, err
	}

	apps, err := mgr.List()
	if err != nil {
		return nil, nil, err
	}

	respBytes, _ := json.MarshalIndent(apps, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroAppsInstallArgs defines the inputs for installing an Agent App.
type TzroAppsInstallArgs struct {
	ArchivePath string `json:"archivePath" jsonschema:"required,Absolute path to the .tzroapp archive file"`
}

func handleTzroAppsInstall(ctx context.Context, req *mcp.CallToolRequest, args TzroAppsInstallArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.ArchivePath) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "archivePath cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	mgr, err := getOrInitPackageManager()
	if err != nil {
		return nil, nil, err
	}

	app, err := mgr.Install(args.ArchivePath)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": %q}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	respBytes, _ := json.MarshalIndent(app, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroAppsUninstallArgs defines the inputs for uninstalling an Agent App.
type TzroAppsUninstallArgs struct {
	AppID string `json:"appId" jsonschema:"required,The Agent App ID to uninstall"`
	Purge bool   `json:"purge,omitempty" jsonschema:"If true permanently removes all data and tables. Default false (soft-disable)."`
}

func handleTzroAppsUninstall(ctx context.Context, req *mcp.CallToolRequest, args TzroAppsUninstallArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.AppID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "appId cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	mgr, err := getOrInitPackageManager()
	if err != nil {
		return nil, nil, err
	}

	if args.Purge {
		err = mgr.Purge(args.AppID)
	} else {
		err = mgr.Uninstall(args.AppID)
	}
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": %q}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	action := "uninstalled"
	if args.Purge {
		action = "purged"
	}
	respBytes, _ := json.MarshalIndent(map[string]string{
		"status": "success",
		"appId":  args.AppID,
		"action": action,
	}, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// --- Daemon Restart MCP tool handler ---

// TzroRestartArgs defines inputs for tzro_restart.
type TzroRestartArgs struct {
	Reason string `json:"reason,omitempty" jsonschema:"Optional reason for the restart (logged for audit). Example: config change, binary upgrade, stuck sidecar"`
}

func handleTzroRestart(ctx context.Context, req *mcp.CallToolRequest, args TzroRestartArgs) (*mcp.CallToolResult, any, error) {
	// Build request body
	body := map[string]string{}
	if args.Reason != "" {
		body["reason"] = args.Reason
	}

	// POST to daemon — this triggers the re-exec after responding
	respBytes, err := proxyToDaemon("/api/restart", http.MethodPost, body)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"status":"error","message":"failed to send restart request: %v"}`, err)},
			},
			IsError: true,
		}, nil, nil
	}

	// Parse the pre-restart response to capture uptime
	var restartResp struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
		Uptime string `json:"uptime"`
	}
	_ = json.Unmarshal(respBytes, &restartResp)

	// Fire-and-verify: wait for the daemon to come back up
	// The re-exec happens ~100ms after the response, so wait a bit first
	time.Sleep(300 * time.Millisecond)

	daemonURL := config.GetDaemonURL()
	healthClient := &http.Client{Timeout: 2 * time.Second}
	verified := false

	for attempt := range 5 {
		healthReq, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+"/health", nil)
		if err != nil {
			break
		}
		resp, err := healthClient.Do(healthReq)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			verified = true
			break
		}
		if attempt < 4 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if !verified {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"status":"error","message":"daemon did not respond after restart","previousUptime":%q}`, restartResp.Uptime)},
			},
			IsError: true,
		}, nil, nil
	}

	result := map[string]string{
		"status":         "restarted",
		"previousUptime": restartResp.Uptime,
		"reason":         restartResp.Reason,
	}
	resultBytes, _ := json.MarshalIndent(result, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(resultBytes)},
		},
	}, nil, nil
}

func proxyToDaemon(path string, method string, reqBody interface{}) ([]byte, error) {
	daemonURL := config.GetDaemonURL()
	url := fmt.Sprintf("%s%s", daemonURL, path)

	var bodyReader io.Reader
	if reqBody != nil {
		reqBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(reqBytes)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBytes, fmt.Errorf("daemon returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}
