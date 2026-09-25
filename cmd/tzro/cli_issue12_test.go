package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/session"
)

func runGitCLI(t *testing.T, dir string, args ...string) {
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

func TestCLI_PauseResume_Issue12(t *testing.T) {
	tempDir := t.TempDir()
	runGitCLI(t, tempDir, "init")
	runGitCLI(t, tempDir, "checkout", "-b", "feature-pause")
	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\n\nfunc RunApp() {}\n"), 0644)
	runGitCLI(t, tempDir, "add", ".")
	runGitCLI(t, tempDir, "commit", "-m", "init")

	// Create unstaged edit
	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\n\nfunc RunApp() { /* edited */ }\n"), 0644)

	// Switch working directory to tempDir for CLI test
	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer os.Chdir(origWd)

	// 1. Run tzro pause "Initial pause"
	var outBuf bytes.Buffer
	rootCmd := newRootCmd()
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&outBuf)
	rootCmd.SetArgs([]string{"pause", "Initial pause"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("tzro pause failed: %v", err)
	}

	pauseOutput := outBuf.String()
	if !strings.Contains(pauseOutput, "✓ Paused session") {
		t.Fatalf("expected '✓ Paused session' in output, got: %s", pauseOutput)
	}

	// Extract session ID
	parts := strings.Fields(pauseOutput)
	var sessionID string
	for i, p := range parts {
		if p == "session" && i+1 < len(parts) {
			sessionID = parts[i+1]
			break
		}
	}
	if sessionID == "" {
		t.Fatalf("failed to parse session ID from pause output: %s", pauseOutput)
	}

	// 2. Run tzro resume (without argument) -> should resume latest session on this branch
	outBuf.Reset()
	resumeCmd := newRootCmd()
	resumeCmd.SetOut(&outBuf)
	resumeCmd.SetErr(&outBuf)
	resumeCmd.SetArgs([]string{"resume"})

	if err := resumeCmd.Execute(); err != nil {
		t.Fatalf("tzro resume failed: %v", err)
	}

	resumeOutput := outBuf.String()
	if !strings.Contains(resumeOutput, sessionID) {
		t.Errorf("expected resume output to contain %s, got: %s", sessionID, resumeOutput)
	}
	if !strings.Contains(resumeOutput, "Initial pause") {
		t.Errorf("expected resume output to contain 'Initial pause', got: %s", resumeOutput)
	}

	// 3. Run tzro resume --json
	outBuf.Reset()
	resumeJSONCmd := newRootCmd()
	resumeJSONCmd.SetOut(&outBuf)
	resumeJSONCmd.SetErr(&outBuf)
	resumeJSONCmd.SetArgs([]string{"resume", "--json"})

	if err := resumeJSONCmd.Execute(); err != nil {
		t.Fatalf("tzro resume --json failed: %v", err)
	}

	var vm session.DashboardViewModel
	if err := json.Unmarshal(outBuf.Bytes(), &vm); err != nil {
		t.Fatalf("failed to parse JSON resume dashboard: %v\nOutput: %s", err, outBuf.String())
	}
	if vm.SessionID != sessionID {
		t.Errorf("expected session ID %s in JSON report, got %s", sessionID, vm.SessionID)
	}
	if vm.Branch != "feature-pause" {
		t.Errorf("expected Branch feature-pause, got %s", vm.Branch)
	}

	// 4. Switch branch and test explicit resume branch mismatch detection
	runGitCLI(t, tempDir, "checkout", "-b", "other-branch")

	// Implicit resume on other-branch should report NO session found (no silent fallback!)
	outBuf.Reset()
	resumeOtherCmd := newRootCmd()
	resumeOtherCmd.SetOut(&outBuf)
	resumeOtherCmd.SetErr(&outBuf)
	resumeOtherCmd.SetArgs([]string{"resume"})

	if err := resumeOtherCmd.Execute(); err != nil {
		t.Fatalf("tzro resume on other-branch failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "No paused session found") {
		t.Errorf("expected 'No paused session found' on other-branch, got: %s", outBuf.String())
	}

	// Explicit resume with sessionID should succeed and report branch mismatch
	outBuf.Reset()
	resumeExplicitCmd := newRootCmd()
	resumeExplicitCmd.SetOut(&outBuf)
	resumeExplicitCmd.SetErr(&outBuf)
	resumeExplicitCmd.SetArgs([]string{"resume", sessionID})

	if err := resumeExplicitCmd.Execute(); err != nil {
		t.Fatalf("tzro resume <id> failed: %v", err)
	}
	if !strings.Contains(strings.ToUpper(outBuf.String()), "BRANCH MISMATCH") {
		t.Errorf("expected 'Branch Mismatch' warning in explicit resume output, got: %s", outBuf.String())
	}
}
