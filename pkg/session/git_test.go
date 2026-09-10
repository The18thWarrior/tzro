package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStreamSHA256_ProducesCorrect64CharHex(t *testing.T) {
	tmpDir := t.TempDir()
	content := "package main\nfunc main() {}\n"
	filePath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := StreamSHA256(filePath)
	if err != nil {
		t.Fatalf("StreamSHA256 returned error: %v", err)
	}

	// Compute expected hash independently
	sum := sha256.Sum256([]byte(content))
	want := hex.EncodeToString(sum[:])

	if len(got) != 64 {
		t.Errorf("expected 64-char hex, got %d chars: %s", len(got), got)
	}
	if got != want {
		t.Errorf("hash mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestStreamSHA256_ErrorOnMissingFile(t *testing.T) {
	_, err := StreamSHA256("/nonexistent/path/to/file.go")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestSenseBranch_FallbackToMainInNonGitDir(t *testing.T) {
	tmpDir := t.TempDir() // not a git repo
	// Clear any CI env vars that would interfere
	for _, key := range []string{"GIT_BRANCH", "BRANCH_NAME", "CI_COMMIT_BRANCH"} {
		t.Setenv(key, "")
	}

	branch := SenseBranch(context.Background(), tmpDir)
	if branch != "main" {
		t.Errorf("expected fallback to \"main\" in non-git dir, got %q", branch)
	}
}

func TestSenseBranch_ReturnsRealBranch(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize a real git repo with a commit so HEAD is valid
	runTestGit(t, tmpDir, "init")
	runTestGit(t, tmpDir, "checkout", "-b", "feat/test-branch")
	os.WriteFile(tmpDir+"/README.md", []byte("# test"), 0644)
	runTestGit(t, tmpDir, "add", ".")
	runTestGit(t, tmpDir, "commit", "-m", "init", "--allow-empty")

	branch := SenseBranch(context.Background(), tmpDir)
	if branch != "feat/test-branch" {
		t.Errorf("expected \"feat/test-branch\", got %q", branch)
	}
}

func TestSenseBranch_DetachedHead(t *testing.T) {
	tmpDir := t.TempDir()

	runTestGit(t, tmpDir, "init")
	os.WriteFile(tmpDir+"/file.txt", []byte("v1"), 0644)
	runTestGit(t, tmpDir, "add", ".")
	runTestGit(t, tmpDir, "commit", "-m", "first")
	os.WriteFile(tmpDir+"/file.txt", []byte("v2"), 0644)
	runTestGit(t, tmpDir, "add", ".")
	runTestGit(t, tmpDir, "commit", "-m", "second")

	// Detach HEAD at first commit
	runTestGit(t, tmpDir, "checkout", "HEAD~1")

	branch := SenseBranch(context.Background(), tmpDir)
	if !isDetachedBranch(branch) {
		t.Errorf("expected detached HEAD format, got %q", branch)
	}
}

func TestSenseBranch_CIEnvFallback(t *testing.T) {
	tmpDir := t.TempDir() // not a git repo
	t.Setenv("GIT_BRANCH", "ci/deploy-prod")
	t.Setenv("BRANCH_NAME", "")
	t.Setenv("CI_COMMIT_BRANCH", "")

	branch := SenseBranch(context.Background(), tmpDir)
	if branch != "ci/deploy-prod" {
		t.Errorf("expected CI env branch \"ci/deploy-prod\", got %q", branch)
	}
}

// isDetachedBranch checks if the branch string matches "HEAD (detached at ...)" format.
func isDetachedBranch(branch string) bool {
	return len(branch) > len("HEAD (detached at )") &&
		branch[:18] == "HEAD (detached at " &&
		branch[len(branch)-1] == ')'
}

// runTestGit runs a git command in a test, failing on error.
func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestSenseChangedFiles_SkipsDeletesAndTracksModified(t *testing.T) {
	tmpDir := t.TempDir()

	runTestGit(t, tmpDir, "init")

	// Create initial files and commit
	os.WriteFile(tmpDir+"/keep.go", []byte("package keep\n"), 0644)
	os.WriteFile(tmpDir+"/delete-me.go", []byte("package del\n"), 0644)
	os.WriteFile(tmpDir+"/modify.go", []byte("package mod // v1\n"), 0644)
	runTestGit(t, tmpDir, "add", ".")
	runTestGit(t, tmpDir, "commit", "-m", "init")

	// Delete one, modify one, add one new untracked
	os.Remove(tmpDir + "/delete-me.go")
	os.WriteFile(tmpDir+"/modify.go", []byte("package mod // v2\n"), 0644)
	os.WriteFile(tmpDir+"/new-untracked.go", []byte("package fresh\n"), 0644)

	snapshots, err := SenseChangedFiles(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("SenseChangedFiles error: %v", err)
	}

	pathSet := make(map[string]bool)
	for _, s := range snapshots {
		pathSet[s.Path] = true
		if len(s.Hash) != 64 {
			t.Errorf("expected 64-char hash for %s, got %d chars", s.Path, len(s.Hash))
		}
	}

	// delete-me.go should NOT appear (deleted)
	if pathSet["delete-me.go"] {
		t.Error("deleted file should be excluded from snapshots")
	}
	// modify.go SHOULD appear
	if !pathSet["modify.go"] {
		t.Error("modified file should appear in snapshots")
	}
	// new-untracked.go SHOULD appear (untracked, -uall)
	if !pathSet["new-untracked.go"] {
		t.Error("untracked file should appear in snapshots")
	}
	// keep.go should NOT appear (unchanged)
	if pathSet["keep.go"] {
		t.Error("unchanged file should not appear in snapshots")
	}
}

func TestSenseChangedFiles_NoChangesReturnsNil(t *testing.T) {
	tmpDir := t.TempDir()

	runTestGit(t, tmpDir, "init")
	os.WriteFile(tmpDir+"/file.go", []byte("clean"), 0644)
	runTestGit(t, tmpDir, "add", ".")
	runTestGit(t, tmpDir, "commit", "-m", "init")

	snapshots, err := SenseChangedFiles(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("SenseChangedFiles error: %v", err)
	}
	if snapshots != nil {
		t.Errorf("expected nil for clean repo, got %d snapshots", len(snapshots))
	}
}

func TestSenseChangedFiles_NonGitDirReturnsError(t *testing.T) {
	tmpDir := t.TempDir() // not a git repo

	_, err := SenseChangedFiles(context.Background(), tmpDir)
	if err == nil {
		t.Fatal("expected error for non-git dir, got nil")
	}
}

func TestUnquotePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`"path/with spaces.go"`, "path/with spaces.go"},
		{`"path/with\ttab.go"`, "path/with\ttab.go"},
		{"normal/path.go", "normal/path.go"},
		{`""`, ""},
	}
	for _, tt := range tests {
		got := unquotePath(tt.input)
		if got != tt.want {
			t.Errorf("unquotePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsDeletedStatus(t *testing.T) {
	deleted := []string{" D", "D ", "DD"}
	notDeleted := []string{" M", "M ", "A ", "??", "R "}

	for _, s := range deleted {
		if !isDeletedStatus(s) {
			t.Errorf("expected %q to be deleted status", s)
		}
	}
	for _, s := range notDeleted {
		if isDeletedStatus(s) {
			t.Errorf("expected %q to NOT be deleted status", s)
		}
	}
}
