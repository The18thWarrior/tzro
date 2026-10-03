package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/session"
	"tzro/pkg/store"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGitCLI(t, dir, "init")
	runGitCLI(t, dir, "checkout", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	runGitCLI(t, dir, "add", ".")
	runGitCLI(t, dir, "commit", "-m", "initial commit")
}

func TestIssue14_CLI_ShellInit(t *testing.T) {
	// 1. zsh init
	cmd := newRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "init", "zsh"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell init zsh failed: %v", err)
	}
	output := outBuf.String()
	if !strings.Contains(output, "add-zsh-hook preexec _tzro_preexec") {
		t.Errorf("expected zsh preexec hook, got:\n%s", output)
	}
	if !strings.Contains(output, "_TZRO_ZSH_INIT") {
		t.Errorf("expected zsh idempotency guard, got:\n%s", output)
	}

	// 2. bash init
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "init", "bash"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell init bash failed: %v", err)
	}
	output = outBuf.String()
	if !strings.Contains(output, "PROMPT_COMMAND") {
		t.Errorf("expected bash PROMPT_COMMAND hook, got:\n%s", output)
	}

	// 3. unsupported shell
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "init", "tcsh"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for unsupported shell, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported shell") {
		t.Errorf("expected unsupported shell error, got %v", err)
	}
}

func TestIssue14_CLI_ShellRecordAndBranchIsolation(t *testing.T) {
	tempDir := t.TempDir()
	tempDir, _ = filepath.EvalSymlinks(tempDir)
	initGitRepo(t, tempDir)

	dbPath := filepath.Join(tempDir, ".tzro", "state.db")
	_ = os.Setenv("TZRO_DB_PATH", dbPath)
	defer os.Unsetenv("TZRO_DB_PATH")

	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(origWd) }()

	// 1. Shell record without active task -> silent no-op
	cmd := newRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=shell_1", "--id=evt_1", "--cmd=go test ./..."})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("record preexec without active task returned error: %v", err)
	}

	s, err := store.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer s.Close()

	events, _ := s.GetCommandEvents(tempDir, "", 10)
	if len(events) != 0 {
		t.Errorf("expected 0 events without active task, got %d", len(events))
	}

	// 2. Pause a task on main branch -> establishes active task for shell_1
	_ = os.Setenv("TZRO_SHELL_ID", "shell_1")
	defer os.Unsetenv("TZRO_SHELL_ID")

	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"pause", "Implement Feature X"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pause command failed: %v", err)
	}

	// Verify active task is bound
	activeID, _ := s.GetActiveTask(tempDir, "shell_1")
	if activeID == "" {
		t.Fatalf("expected active task for shell_1")
	}

	// 3. Non-allowlisted command (e.g. ls -la) -> silent no-op
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=shell_1", "--id=evt_non_allowlist", "--cmd=ls -la"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("record non-allowlisted command failed: %v", err)
	}
	events, _ = s.GetCommandEvents(tempDir, activeID, 10)
	if len(events) != 0 {
		t.Errorf("expected 0 events for non-allowlisted command, got %d", len(events))
	}

	// 4. Allowlisted command with secret argument -> recorded and redacted
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=shell_1", "--id=evt_test_1", "--cmd=go test --token=sk-supersecret12345678901234567890 ./..."})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("record preexec failed: %v", err)
	}

	events, _ = s.GetCommandEvents(tempDir, activeID, 10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if strings.Contains(events[0].DisplayText, "supersecret") {
		t.Errorf("secret not redacted from DisplayText: %s", events[0].DisplayText)
	}
	if !strings.Contains(events[0].DisplayText, "[REDACTED]") {
		t.Errorf("expected [REDACTED] placeholder in DisplayText: %s", events[0].DisplayText)
	}
	if events[0].ExitStatus != nil {
		t.Errorf("expected nil ExitStatus before precmd, got %v", events[0].ExitStatus)
	}

	// 5. Correlate with precmd
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "record", "precmd", "--shell-id=shell_1", "--id=evt_test_1", "--status=0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("record precmd failed: %v", err)
	}

	events, _ = s.GetCommandEvents(tempDir, activeID, 10)
	if len(events) != 1 || events[0].ExitStatus == nil || *events[0].ExitStatus != 0 {
		t.Fatalf("expected correlated event with ExitStatus 0, got %+v", events)
	}

	// 6. Branch switch -> subsequent command must NOT attach to previous task
	runGitCLI(t, tempDir, "checkout", "-b", "other-branch")
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=shell_1", "--id=evt_other_branch", "--cmd=cargo test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("record on other branch returned error: %v", err)
	}

	events, _ = s.GetCommandEvents(tempDir, activeID, 10)
	if len(events) != 1 {
		t.Errorf("command on different branch should NOT attach to active task: expected 1 event, got %d", len(events))
	}

	// Switch back to original branch
	runGitCLI(t, tempDir, "checkout", "main")

	// 7. Separate terminal isolation: shell_2 does not have active task
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=shell_2", "--id=evt_shell_2", "--cmd=cargo test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("record shell_2 error: %v", err)
	}
	events, _ = s.GetCommandEvents(tempDir, activeID, 10)
	if len(events) != 1 {
		t.Errorf("command from un-bound shell_2 should NOT attach: got %d events", len(events))
	}
}

func TestIssue14_CLI_ShellStatusAndClear(t *testing.T) {
	tempDir := t.TempDir()
	initGitRepo(t, tempDir)

	dbPath := filepath.Join(tempDir, ".tzro", "state.db")
	_ = os.Setenv("TZRO_DB_PATH", dbPath)
	defer os.Unsetenv("TZRO_DB_PATH")

	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(origWd) }()

	// Status plain
	cmd := newRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell status failed: %v", err)
	}
	outStr := outBuf.String()
	if !strings.Contains(outStr, "tzro Shell Integration Status") {
		t.Errorf("expected header in shell status, got:\n%s", outStr)
	}

	// Status json
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "status", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell status --json failed: %v", err)
	}
	var metrics session.ShellMetrics
	if err := json.Unmarshal(outBuf.Bytes(), &metrics); err != nil {
		t.Fatalf("failed to parse status JSON: %v, raw:\n%s", err, outBuf.String())
	}
	if metrics.QueueMaxCount != 1000 {
		t.Errorf("expected QueueMaxCount 1000, got %d", metrics.QueueMaxCount)
	}

	// Clear
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"shell", "clear"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell clear failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Cleared command history") {
		t.Errorf("expected clear confirmation, got:\n%s", outBuf.String())
	}
}

func TestIssue14_CLI_PauseResumeCommandHistoryDashboard(t *testing.T) {
	tempDir := t.TempDir()
	initGitRepo(t, tempDir)

	dbPath := filepath.Join(tempDir, ".tzro", "state.db")
	_ = os.Setenv("TZRO_DB_PATH", dbPath)
	defer os.Unsetenv("TZRO_DB_PATH")

	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(origWd) }()

	shellID := "shell_dash_test"
	_ = os.Setenv("TZRO_SHELL_ID", shellID)
	defer os.Unsetenv("TZRO_SHELL_ID")

	// 1. Initial pause to create and bind active task
	cmd := newRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"pause", "Command History Dashboard Verification"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pause failed: %v", err)
	}

	// 2. Simulate running two commands in the shell:
	// Command 1: go test ./... (status 0)
	cmd = newRootCmd()
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=" + shellID, "--id=evt_dash_1", "--cmd=go test ./..."})
	_ = cmd.Execute()
	cmd = newRootCmd()
	cmd.SetArgs([]string{"shell", "record", "precmd", "--shell-id=" + shellID, "--id=evt_dash_1", "--status=0"})
	_ = cmd.Execute()

	// Command 2: cargo test (uncompleted / unknown)
	cmd = newRootCmd()
	cmd.SetArgs([]string{"shell", "record", "preexec", "--shell-id=" + shellID, "--id=evt_dash_2", "--cmd=cargo test"})
	_ = cmd.Execute()

	// 3. Pause again to snapshot recent commands
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"pause", "Command History Dashboard Verification"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("second pause failed: %v", err)
	}

	// 4. Resume task and verify commands appear in dashboard
	cmd = newRootCmd()
	outBuf.Reset()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"resume", "--format=plain"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	resumeOut := outBuf.String()
	if !strings.Contains(resumeOut, "Recent Commands") {
		t.Errorf("expected 'Recent Commands' section in dashboard, got:\n%s", resumeOut)
	}
	if !strings.Contains(resumeOut, "go test ./...") {
		t.Errorf("expected 'go test ./...' in dashboard, got:\n%s", resumeOut)
	}
	if !strings.Contains(resumeOut, "(exit 0)") {
		t.Errorf("expected exit status '(exit 0)' in dashboard, got:\n%s", resumeOut)
	}
	if !strings.Contains(resumeOut, "cargo test") {
		t.Errorf("expected 'cargo test' in dashboard, got:\n%s", resumeOut)
	}
	if !strings.Contains(resumeOut, "(exit unknown)") {
		t.Errorf("expected exit status '(exit unknown)' for uncompleted command in dashboard, got:\n%s", resumeOut)
	}
}
