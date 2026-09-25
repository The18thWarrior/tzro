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

// GitState captures Git repository state and identity.
type GitState struct {
	IsGit      bool   `json:"is_git"`
	Branch     string `json:"branch"`
	IsDetached bool   `json:"is_detached"`
	HeadCommit string `json:"head_commit"`
	Worktree   string `json:"worktree"`
}

// SenseGitState inspects the current directory and returns detailed Git identity.
func SenseGitState(ctx context.Context, workDir string) GitState {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	_, err := runGit(ctx, workDir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		// Non-Git workspace
		ciBranch := senseBranchFromEnv()
		if ciBranch != "" {
			return GitState{
				IsGit:  false,
				Branch: ciBranch,
			}
		}
		return GitState{
			IsGit:  false,
			Branch: "(non-git)",
		}
	}

	worktree, _ := runGit(ctx, workDir, "rev-parse", "--show-toplevel")
	headCommit, _ := runGit(ctx, workDir, "rev-parse", "HEAD")

	// Check branch / detached
	branch, symErr := runGit(ctx, workDir, "symbolic-ref", "--short", "-q", "HEAD")
	if symErr == nil && branch != "" {
		return GitState{
			IsGit:      true,
			Branch:     branch,
			IsDetached: false,
			HeadCommit: headCommit,
			Worktree:   worktree,
		}
	}

	// Detached HEAD
	sha, err := runGit(ctx, workDir, "rev-parse", "--short", "HEAD")
	detachedBranch := "HEAD (detached)"
	if err == nil && sha != "" {
		detachedBranch = fmt.Sprintf("HEAD (detached at %s)", sha)
	}

	return GitState{
		IsGit:      true,
		Branch:     detachedBranch,
		IsDetached: true,
		HeadCommit: headCommit,
		Worktree:   worktree,
	}
}

// SenseBranch detects the current git branch with graceful fallbacks.
//
// Fallback hierarchy:
//  1. git symbolic-ref or rev-parse (2s timeout)
//  2. If detached HEAD → "HEAD (detached at <sha>)"
//  3. CI environment variables: GIT_BRANCH, BRANCH_NAME, CI_COMMIT_BRANCH
//  4. "(non-git)" (distinct non-git identity, not "main")
func SenseBranch(ctx context.Context, workDir string) string {
	state := SenseGitState(ctx, workDir)
	return state.Branch
}

// senseBranchFromEnv probes CI environment variables for a branch name.
func senseBranchFromEnv() string {
	for _, key := range []string{"GIT_BRANCH", "BRANCH_NAME", "CI_COMMIT_BRANCH"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// SenseChanges detects all changed files via git status --porcelain=v1 -uall,
// capturing staged status, worktree status, index SHA-256, worktree SHA-256,
// untracked files, expected deletions, and renames.
func SenseChanges(ctx context.Context, workDir string) ([]FileSnapshot, error) {
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
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 {
			continue
		}

		xy := line[:2]
		x := string(xy[0])
		y := string(xy[1])
		path := strings.TrimSpace(line[2:])

		var oldPath string
		status := "modified"

		// Handle renames: "R  old -> new"
		if x == "R" || y == "R" {
			status = "renamed"
			if idx := strings.Index(path, " -> "); idx >= 0 {
				oldPath = unquotePath(path[:idx])
				path = path[idx+4:]
			}
		} else if xy == "??" {
			status = "untracked"
		} else if x == "D" || y == "D" {
			status = "deleted"
		} else if x == "A" {
			status = "added"
		}

		path = unquotePath(path)
		if seen[path] {
			continue
		}
		seen[path] = true

		fullPath := path
		if !strings.HasPrefix(path, "/") {
			fullPath = filepath.Join(workDir, path)
		}

		snap := FileSnapshot{
			Path:           path,
			OldPath:        oldPath,
			Status:         status,
			StagedStatus:   x,
			WorktreeStatus: y,
		}

		// Staged blob hash: git show :0:<path>
		if x != " " && x != "?" && x != "D" {
			stagedContent, stagedErr := runGit(ctx, workDir, "show", ":0:"+path)
			if stagedErr == nil {
				h := sha256.Sum256([]byte(stagedContent))
				snap.StagedHash = hex.EncodeToString(h[:])
			}
		}

		// Worktree file hash
		if y != "D" && status != "deleted" {
			if info, err := os.Stat(fullPath); err == nil && info.Size() <= maxFileSizeBytes {
				if hash, err := StreamSHA256(fullPath); err == nil {
					snap.WorktreeHash = hash
				}
			}
		}

		// Legacy / primary Hash field for backward compatibility
		if snap.WorktreeHash != "" {
			snap.Hash = snap.WorktreeHash
		} else if snap.StagedHash != "" {
			snap.Hash = snap.StagedHash
		}

		snapshots = append(snapshots, snap)
	}

	return snapshots, nil
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
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 {
			continue
		}

		xy := line[:2]
		path := strings.TrimSpace(line[2:])

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

// runGit executes a git command and returns trimmed stdout (preserving leading whitespace on line 1).
func runGit(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
