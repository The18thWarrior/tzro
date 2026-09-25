package hooks

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	TzroHookBeginMarker = "# --- BEGIN TZRO MANAGED HOOK ---"
	TzroHookEndMarker   = "# --- END TZRO MANAGED HOOK ---"
)

// GitHookResult records the outcome of a Git hook operation.
type GitHookResult struct {
	HookName     string `json:"hook_name"`
	HookPath     string `json:"hook_path"`
	Status       string `json:"status"` // installed | updated | removed | restored | skipped
	Message      string `json:"message,omitempty"`
	BackupPath   string `json:"backup_path,omitempty"`
	ManualAdvice string `json:"manual_advice,omitempty"`
}

// ResolveGitHooksDir locates the effective Git hooks directory for workspaceRoot,
// respecting core.hooksPath, linked worktrees, and submodules.
func ResolveGitHooksDir(workspaceRoot string) (string, error) {
	// 1. Try git rev-parse --git-path hooks
	cmd := exec.Command("git", "rev-parse", "--git-path", "hooks")
	cmd.Dir = workspaceRoot
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil {
		out := strings.TrimSpace(stdout.String())
		if out != "" {
			if filepath.IsAbs(out) {
				return filepath.Clean(out), nil
			}
			return filepath.Clean(filepath.Join(workspaceRoot, out)), nil
		}
	}

	// 2. Fallback: inspect .git entry in workspaceRoot
	gitEntry := filepath.Join(workspaceRoot, ".git")
	fi, err := os.Stat(gitEntry)
	if err != nil {
		return "", fmt.Errorf("not a git repository (or .git not accessible): %w", err)
	}

	if fi.IsDir() {
		return filepath.Join(gitEntry, "hooks"), nil
	}

	// .git is a file (linked worktree or submodule, e.g. "gitdir: /path/to/commondir/worktrees/name")
	content, err := os.ReadFile(gitEntry)
	if err != nil {
		return "", fmt.Errorf("failed to read .git file: %w", err)
	}

	trimmed := strings.TrimSpace(string(content))
	if strings.HasPrefix(trimmed, "gitdir:") {
		dirTarget := strings.TrimSpace(strings.TrimPrefix(trimmed, "gitdir:"))
		if !filepath.IsAbs(dirTarget) {
			dirTarget = filepath.Join(workspaceRoot, dirTarget)
		}
		// In worktrees, hooks directory is typically in common dir or gitdir/hooks
		commondirPath := filepath.Join(dirTarget, "commondir")
		if cBytes, cErr := os.ReadFile(commondirPath); cErr == nil {
			commonDir := strings.TrimSpace(string(cBytes))
			if !filepath.IsAbs(commonDir) {
				commonDir = filepath.Join(dirTarget, commonDir)
			}
			return filepath.Join(commonDir, "hooks"), nil
		}
		return filepath.Join(dirTarget, "hooks"), nil
	}

	return "", fmt.Errorf("malformed .git file: %s", trimmed)
}

// GeneratePreCommitHookScript returns the portable advisory hook shell script.
func GeneratePreCommitHookScript() string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString(TzroHookBeginMarker + "\n")
	sb.WriteString("# tzro advisory pre-commit hook\n")
	sb.WriteString("# Shows staged blast radius before commit. Does not block commits on references.\n")
	sb.WriteString("# Bypass using: git commit --no-verify\n\n")
	sb.WriteString("# Ensure tzro is available on PATH\n")
	sb.WriteString("if ! command -v tzro >/dev/null 2>&1; then\n")
	sb.WriteString("    echo \"[tzro] Warning: tzro binary not found on PATH. Skipping staged impact analysis.\"\n")
	sb.WriteString("    exit 0\n")
	sb.WriteString("fi\n\n")
	sb.WriteString("# Run advisory staged impact analysis with TTY detection\n")
	sb.WriteString("if [ -t 1 ]; then\n")
	sb.WriteString("    tzro impact --staged --format tree 2>/dev/null || {\n")
	sb.WriteString("        echo \"[tzro] Advisory impact check skipped.\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("else\n")
	sb.WriteString("    tzro impact --staged --format markdown 2>/dev/null || {\n")
	sb.WriteString("        echo \"[tzro] Advisory impact check skipped.\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("fi\n\n")
	sb.WriteString("# If an existing hook was backed up by tzro, execute it and preserve its exit code\n")
	sb.WriteString("BACKUP_HOOK=\"$0.tzro.backup\"\n")
	sb.WriteString("if [ -f \"$BACKUP_HOOK\" ] && [ -x \"$BACKUP_HOOK\" ]; then\n")
	sb.WriteString("    \"$BACKUP_HOOK\" \"$@\"\n")
	sb.WriteString("    exit $?\n")
	sb.WriteString("fi\n\n")
	sb.WriteString("exit 0\n")
	sb.WriteString(TzroHookEndMarker + "\n")
	return sb.String()
}

// InstallGitHook installs or updates a Git hook (e.g. "pre-commit") in workspaceRoot.
func InstallGitHook(workspaceRoot, hookName string, force bool) (*GitHookResult, error) {
	if hookName != "pre-commit" {
		return nil, fmt.Errorf("unsupported hook name %q (currently supported: pre-commit)", hookName)
	}

	hooksDir, err := ResolveGitHooksDir(workspaceRoot)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create hooks directory %s: %w", hooksDir, err)
	}

	targetPath := filepath.Join(hooksDir, hookName)
	backupPath := targetPath + ".tzro.backup"
	scriptContent := GeneratePreCommitHookScript()

	// Check if target file already exists
	if _, err := os.Stat(targetPath); err == nil {
		existingBytes, rErr := os.ReadFile(targetPath)
		if rErr == nil {
			existingStr := string(existingBytes)
			// If already contains managed hook, update idempotently
			if strings.Contains(existingStr, TzroHookBeginMarker) {
				if err := os.WriteFile(targetPath, []byte(scriptContent), 0755); err != nil {
					return nil, fmt.Errorf("failed to update hook: %w", err)
				}
				return &GitHookResult{
					HookName: hookName,
					HookPath: targetPath,
					Status:   "updated",
					Message:  "Idempotently updated existing tzro managed hook.",
				}, nil
			}

			// Non-tzro hook exists
			if !force {
				return &GitHookResult{
					HookName:     hookName,
					HookPath:     targetPath,
					Status:       "skipped",
					Message:      "Existing non-tzro hook found. Left intact.",
					ManualAdvice: "To manually integrate tzro, add 'tzro impact --staged' to your pre-commit script, or pass --force to backup and replace.",
				}, nil
			}

			// Force replacement: backup existing hook first
			if err := os.WriteFile(backupPath, existingBytes, 0755); err != nil {
				return nil, fmt.Errorf("failed to create backup at %s: %w", backupPath, err)
			}
			if err := os.WriteFile(targetPath, []byte(scriptContent), 0755); err != nil {
				return nil, fmt.Errorf("failed to write hook: %w", err)
			}
			return &GitHookResult{
				HookName:   hookName,
				HookPath:   targetPath,
				Status:     "installed",
				BackupPath: backupPath,
				Message:    fmt.Sprintf("Existing hook backed up to %s and replaced with tzro managed hook.", backupPath),
			}, nil
		}
	}

	// Target does not exist, install cleanly
	if err := os.WriteFile(targetPath, []byte(scriptContent), 0755); err != nil {
		return nil, fmt.Errorf("failed to write hook to %s: %w", targetPath, err)
	}

	return &GitHookResult{
		HookName: hookName,
		HookPath: targetPath,
		Status:   "installed",
		Message:  "Tzro advisory pre-commit hook successfully installed.",
	}, nil
}

// UninstallGitHook removes the tzro managed hook or restores its backup.
func UninstallGitHook(workspaceRoot, hookName string) (*GitHookResult, error) {
	hooksDir, err := ResolveGitHooksDir(workspaceRoot)
	if err != nil {
		return nil, err
	}

	targetPath := filepath.Join(hooksDir, hookName)
	backupPath := targetPath + ".tzro.backup"

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return &GitHookResult{
			HookName: hookName,
			HookPath: targetPath,
			Status:   "skipped",
			Message:  "Hook does not exist.",
		}, nil
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read hook: %w", err)
	}

	if !strings.Contains(string(content), TzroHookBeginMarker) {
		return &GitHookResult{
			HookName: hookName,
			HookPath: targetPath,
			Status:   "skipped",
			Message:  "Hook is not managed by tzro; left intact.",
		}, nil
	}

	// If backup exists, restore it
	if _, bErr := os.Stat(backupPath); bErr == nil {
		backupData, rErr := os.ReadFile(backupPath)
		if rErr == nil {
			_ = os.WriteFile(targetPath, backupData, 0755)
			_ = os.Remove(backupPath)
			return &GitHookResult{
				HookName:   hookName,
				HookPath:   targetPath,
				Status:     "restored",
				BackupPath: backupPath,
				Message:    "Tzro managed hook removed and original backup restored.",
			}, nil
		}
	}

	// Remove target hook
	if err := os.Remove(targetPath); err != nil {
		return nil, fmt.Errorf("failed to remove hook: %w", err)
	}

	return &GitHookResult{
		HookName: hookName,
		HookPath: targetPath,
		Status:   "removed",
		Message:  "Tzro managed hook removed.",
	}, nil
}
