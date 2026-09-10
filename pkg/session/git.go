package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// gitTimeout is the maximum time allowed for a single git command.
	gitTimeout = 2 * time.Second

	// maxFileSizeBytes is the threshold above which files are skipped (50 MB).
	maxFileSizeBytes int64 = 50 * 1024 * 1024
)

// StreamSHA256 computes the full 64-character hex SHA-256 hash of a file
// using streaming io.Copy to avoid loading the entire file into memory.
func StreamSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", filePath, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SenseBranch detects the current git branch with graceful fallbacks.
//
// Fallback hierarchy:
//  1. git rev-parse --abbrev-ref HEAD (2s timeout)
//  2. If "HEAD" (detached) → git rev-parse --short HEAD → "HEAD (detached at <sha>)"
//  3. CI environment variables: GIT_BRANCH, BRANCH_NAME, CI_COMMIT_BRANCH
//  4. "main"
func SenseBranch(ctx context.Context, workDir string) string {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	branch, err := runGit(ctx, workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return senseBranchFromEnv()
	}

	if branch == "HEAD" {
		// Detached HEAD — get the short SHA
		sha, err := runGit(ctx, workDir, "rev-parse", "--short", "HEAD")
		if err != nil {
			return "HEAD (detached)"
		}
		return fmt.Sprintf("HEAD (detached at %s)", sha)
	}

	return branch
}

// senseBranchFromEnv probes CI environment variables for a branch name.
func senseBranchFromEnv() string {
	for _, key := range []string{"GIT_BRANCH", "BRANCH_NAME", "CI_COMMIT_BRANCH"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return "main"
}

// SenseChangedFiles detects changed files via git status --porcelain=v1 -uall,
// computes streaming SHA-256 hashes, and returns FileSnapshots.
//
// Skips deleted files and files larger than maxFileSizeBytes (50 MB).
func SenseChangedFiles(ctx context.Context, workDir string) ([]FileSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	output, err := runGit(ctx, workDir, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	if strings.TrimSpace(output) == "" {
		return nil, nil
	}

	var snapshots []FileSnapshot
	seen := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}

		xy := line[:2]
		path := strings.TrimSpace(line[3:])

		// Skip deleted files
		if isDeletedStatus(xy) {
			continue
		}

		// Handle renames: "R  old -> new" — extract new path
		if xy[0] == 'R' || xy[1] == 'R' {
			if idx := strings.Index(path, " -> "); idx >= 0 {
				path = path[idx+4:]
			}
		}

		// Unquote C-style quoted paths
		path = unquotePath(path)

		if seen[path] {
			continue
		}
		seen[path] = true

		fullPath := path
		if !strings.HasPrefix(path, "/") {
			fullPath = filepath.Join(workDir, path)
		}

		// Stat and skip files > 50 MB
		info, err := os.Stat(fullPath)
		if err != nil {
			continue // file may have been deleted between status and stat
		}
		if info.Size() > maxFileSizeBytes {
			continue
		}

		hash, err := StreamSHA256(fullPath)
		if err != nil {
			continue
		}

		snapshots = append(snapshots, FileSnapshot{
			Path: path,
			Hash: hash,
		})
	}

	return snapshots, nil
}

// isDeletedStatus returns true for porcelain status codes indicating deletion.
func isDeletedStatus(xy string) bool {
	return xy == " D" || xy == "D " || xy == "DD"
}

// unquotePath handles C-style quoted paths from git status.
func unquotePath(path string) string {
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		if unquoted, err := strconv.Unquote(path); err == nil {
			return unquoted
		}
	}
	return path
}

// runGit executes a git command and returns trimmed stdout.
func runGit(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
