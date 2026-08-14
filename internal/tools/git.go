package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveGitRepoRoot resolves the working directory for a git operation,
// ensures git is installed, validates the directory against PathValidator,
// and verifies that the directory is inside a valid git repository.
func resolveGitRepoRoot(ctx context.Context, validator *PathValidator, targetPath string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is not installed or not found on PATH")
	}

	workDir := targetPath
	if workDir == "" {
		if tzroDir := os.Getenv("TZRO_DIR"); tzroDir != "" {
			workDir = tzroDir
		} else {
			roots := validator.resolveRoots()
			if len(roots) > 0 {
				workDir = roots[0]
			} else {
				workDir = "."
			}
		}
	}

	resolvedPath, err := validator.ValidatePath(workDir)
	if err != nil {
		return "", fmt.Errorf("path validation failed: %w", err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat path '%s': %w", workDir, err)
	}
	execDir := resolvedPath
	if !info.IsDir() {
		execDir = filepath.Dir(resolvedPath)
	}

	// Verify that execDir is inside a git repo
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = execDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("Not a git repository: '%s'", workDir)
	}

	repoRoot := strings.TrimSpace(string(out))
	if _, err := validator.ValidatePath(repoRoot); err != nil {
		return "", fmt.Errorf("git repository root '%s' is outside allowed paths: %w", repoRoot, err)
	}

	return execDir, nil
}

// gitExec runs a git command in the target directory after validating against the PathValidator.
func gitExec(ctx context.Context, validator *PathValidator, targetPath string, gitArgs ...string) (string, string, error) {
	execDir, err := resolveGitRepoRoot(ctx, validator, targetPath)
	if err != nil {
		return "", "", err
	}

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = execDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return execDir, string(output), fmt.Errorf("git command failed: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	return execDir, string(output), nil
}

// NewGitLogTool creates the git_log tool.
// Retrieves commit history with optional path scoping and count limits.
// Zero-arg default: recent 20 commits across all files.
// Capped at 50 commits with --oneline format if maxCount > 50.
func NewGitLogTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "git_log",
		description: "Retrieve git commit history. Returns commit log entries with hash, author, date, and message. Read-only.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"path":     map[string]interface{}{"type": "string", "description": "Optional file or directory path to scope history"},
			"maxCount": map[string]interface{}{"type": "integer", "description": "Maximum number of commits to return (default 20, max 50)"},
		}, []string{}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Path     string `json:"path"`
				MaxCount *int   `json:"maxCount"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			maxCount := 20
			useOneline := false
			if in.MaxCount != nil && *in.MaxCount > 0 {
				if *in.MaxCount > 50 {
					maxCount = 50
					useOneline = true
				} else {
					maxCount = *in.MaxCount
				}
			}

			args := []string{"log", fmt.Sprintf("-n%d", maxCount)}
			if useOneline {
				args = append(args, "--oneline")
			}

			// Path scoping if specific file/subdirectory requested
			var scopedRelPath string
			if in.Path != "" {
				// If in.Path is a specific file/subpath within repo
				resolved, err := validator.ValidatePath(in.Path)
				if err == nil {
					info, statErr := os.Stat(resolved)
					if statErr == nil && !info.IsDir() {
						scopedRelPath = resolved
					}
				}
			}

			if scopedRelPath != "" {
				args = append(args, "--", scopedRelPath)
			}

			execDir, output, err := gitExec(ctx, validator, in.Path, args...)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			trimmed := strings.TrimSpace(output)
			commitCount := 0
			if trimmed != "" {
				if useOneline {
					commitCount = len(strings.Split(trimmed, "\n"))
				} else {
					lines := strings.Split(trimmed, "\n")
					for _, l := range lines {
						if strings.HasPrefix(l, "commit ") {
							commitCount++
						}
					}
					if commitCount == 0 && len(lines) > 0 {
						commitCount = 1
					}
				}
			}

			result := ToolSuccess(map[string]interface{}{
				"output":      trimmed,
				"commitCount": commitCount,
				"path":        execDir,
			})
			if useOneline {
				result.Hint = "Log capped at 50 commits with --oneline format. Scope with path parameter for detailed history."
			}
			return result, nil
		},
	}
}

// NewGitDiffTool creates the git_diff tool.
// Inspects uncommitted working tree changes or diffs against a commit/branch ref.
// If output exceeds 500 lines, auto-converts to --stat summary with a scoping hint.
func NewGitDiffTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "git_diff",
		description: "View git diff of uncommitted changes or between refs. Output over 500 lines returns stat summary.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"ref":  map[string]interface{}{"type": "string", "description": "Optional git commit hash, branch, or tag to diff against"},
			"path": map[string]interface{}{"type": "string", "description": "Optional file or directory path to scope diff"},
			"stat": map[string]interface{}{"type": "boolean", "description": "Show only diffstat summary"},
		}, []string{}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Ref  string `json:"ref"`
				Path string `json:"path"`
				Stat *bool  `json:"stat"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			isStat := in.Stat != nil && *in.Stat
			args := []string{"diff"}
			if isStat {
				args = append(args, "--stat")
			}
			if in.Ref != "" {
				args = append(args, in.Ref)
			}
			if in.Path != "" {
				resolved, err := validator.ValidatePath(in.Path)
				if err == nil {
					args = append(args, "--", resolved)
				}
			}

			execDir, output, err := gitExec(ctx, validator, in.Path, args...)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			lines := strings.Split(output, "\n")
			lineCount := len(lines)
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lineCount--
			}

			// Stat-first routing if output exceeds 500 lines and wasn't already stat
			if !isStat && lineCount > 500 {
				statArgs := []string{"diff", "--stat"}
				if in.Ref != "" {
					statArgs = append(statArgs, in.Ref)
				}
				if in.Path != "" {
					resolved, _ := validator.ValidatePath(in.Path)
					if resolved != "" {
						statArgs = append(statArgs, "--", resolved)
					}
				}
				_, statOutput, statErr := gitExec(ctx, validator, in.Path, statArgs...)
				if statErr == nil {
					res := ToolSuccess(map[string]interface{}{
						"output":    strings.TrimSpace(statOutput),
						"lineCount": len(strings.Split(strings.TrimSpace(statOutput), "\n")),
						"path":      execDir,
						"ref":       in.Ref,
						"statOnly":  true,
					})
					res.Hint = fmt.Sprintf("Output was large (%d lines). Showing stat summary. Use the `path` parameter to scope to specific files for full diff.", lineCount)
					return res, nil
				}
			}

			res := ToolSuccess(map[string]interface{}{
				"output":    strings.TrimSpace(output),
				"lineCount": lineCount,
				"path":      execDir,
				"ref":       in.Ref,
			})
			return res, nil
		},
	}
}

// NewGitShowTool creates the git_show tool.
// Inspects a specific commit's metadata and patch.
// Defaults to HEAD. If output exceeds 500 lines, auto-converts to --stat summary.
func NewGitShowTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "git_show",
		description: "Show commit details and patch. Defaults to HEAD. Output over 500 lines returns stat summary.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"ref":  map[string]interface{}{"type": "string", "description": "Commit hash, branch, or tag (defaults to HEAD)"},
			"stat": map[string]interface{}{"type": "boolean", "description": "Show only diffstat summary"},
		}, []string{}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Ref  string `json:"ref"`
				Stat *bool  `json:"stat"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			ref := in.Ref
			if ref == "" {
				ref = "HEAD"
			}

			isStat := in.Stat != nil && *in.Stat
			args := []string{"show"}
			if isStat {
				args = append(args, "--stat")
			}
			args = append(args, ref)

			execDir, output, err := gitExec(ctx, validator, "", args...)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			lines := strings.Split(output, "\n")
			lineCount := len(lines)
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lineCount--
			}

			// Stat-first routing if output exceeds 500 lines and wasn't already stat
			if !isStat && lineCount > 500 {
				statArgs := []string{"show", "--stat", ref}
				_, statOutput, statErr := gitExec(ctx, validator, "", statArgs...)
				if statErr == nil {
					res := ToolSuccess(map[string]interface{}{
						"output":    strings.TrimSpace(statOutput),
						"lineCount": len(strings.Split(strings.TrimSpace(statOutput), "\n")),
						"path":      execDir,
						"ref":       ref,
						"statOnly":  true,
					})
					res.Hint = fmt.Sprintf("Output was large (%d lines). Showing stat summary. Use the `path` parameter to scope to specific files for full diff.", lineCount)
					return res, nil
				}
			}

			res := ToolSuccess(map[string]interface{}{
				"output":    strings.TrimSpace(output),
				"lineCount": lineCount,
				"path":      execDir,
				"ref":       ref,
			})
			return res, nil
		},
	}
}
