// Package sentinel implements the Sentinel Agent — a proactive Background Agent
// that reasons over accumulated context to surface emergent insights (ADR-0023).
package sentinel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// WorkspaceScanner detects recently changed files in the workspace.
type WorkspaceScanner interface {
	// ScanChanges returns a list of recently changed file paths relative to the workspace root.
	ScanChanges() ([]string, error)
}

// sensitiveNamePattern matches filenames that likely contain secrets or credentials.
// Used by MtimeScanner to filter out sensitive files from workspace scanning.
var sensitiveNamePattern = regexp.MustCompile(`(?i)(password|secret|credential|token|\.env|\.pem|\.key|id_rsa|\.pfx|\.p12|private|\.keystore|\.jks)`)

// structuralIgnores are directory/file patterns that should always be skipped during scanning.
var structuralIgnores = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	"build":        true,
	"dist":         true,
	".next":        true,
	".cache":       true,
	"vendor":       true,
	"target":       true,
	".idea":        true,
	".vscode":      true,
}

// binaryExtensions are file extensions that should be skipped during scanning.
var binaryExtensions = map[string]bool{
	".o": true, ".so": true, ".dylib": true, ".a": true,
	".exe": true, ".dll": true, ".bin": true,
	".wasm": true, ".pyc": true, ".class": true,
	".zip": true, ".tar": true, ".gz": true, ".rar": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".mp3": true, ".mp4": true, ".avi": true,
	".pdf": true, ".doc": true, ".docx": true,
	".gguf": true, ".onnx": true, ".pt": true, ".safetensors": true,
}

// NewWorkspaceScanner creates the appropriate scanner for the workspace.
// Uses GitScanner if the workspace is a git repository, otherwise falls back to MtimeScanner.
func NewWorkspaceScanner(workspaceDir string, since time.Duration) WorkspaceScanner {
	// Check if workspace is a git repository
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = workspaceDir
	if err := cmd.Run(); err == nil {
		return &GitScanner{Dir: workspaceDir}
	}
	return &MtimeScanner{Dir: workspaceDir, Since: since}
}

// GitScanner detects changes using git status and git diff.
type GitScanner struct {
	Dir string
}

// ScanChanges returns files changed in the working tree (staged + unstaged + untracked).
func (g *GitScanner) ScanChanges() ([]string, error) {
	seen := make(map[string]bool)
	var result []string

	// 1. Modified and staged files
	diffCmd := exec.Command("git", "diff", "--name-only", "HEAD")
	diffCmd.Dir = g.Dir
	diffOut, err := diffCmd.Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(diffOut)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
				result = append(result, line)
			}
		}
	}

	// 2. Untracked files
	statusCmd := exec.Command("git", "status", "--porcelain", "-uall")
	statusCmd.Dir = g.Dir
	statusOut, err := statusCmd.Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(statusOut)), "\n") {
			line = strings.TrimSpace(line)
			if len(line) < 4 {
				continue
			}
			// Status lines are "XY filename" — skip the 3-char prefix
			file := strings.TrimSpace(line[2:])
			// Handle renamed files: "R  old -> new"
			if idx := strings.Index(file, " -> "); idx >= 0 {
				file = file[idx+4:]
			}
			if file != "" && !seen[file] {
				seen[file] = true
				result = append(result, file)
			}
		}
	}

	return result, nil
}

// MtimeScanner detects changes by walking the filesystem and checking modification times.
type MtimeScanner struct {
	Dir   string
	Since time.Duration
}

// ScanChanges returns files modified within the configured time window.
func (m *MtimeScanner) ScanChanges() ([]string, error) {
	cutoff := time.Now().Add(-m.Since)
	var result []string

	err := filepath.Walk(m.Dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable entries
		}

		name := info.Name()

		// Skip structural ignores
		if info.IsDir() {
			if structuralIgnores[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary extensions
		ext := strings.ToLower(filepath.Ext(name))
		if binaryExtensions[ext] {
			return nil
		}

		// Skip sensitive filenames
		if sensitiveNamePattern.MatchString(name) {
			return nil
		}

		// Check modification time
		if info.ModTime().After(cutoff) {
			rel, relErr := filepath.Rel(m.Dir, path)
			if relErr != nil {
				rel = path
			}
			result = append(result, rel)
		}

		return nil
	})

	if err != nil {
		return result, fmt.Errorf("workspace scan error: %w", err)
	}

	return result, nil
}
