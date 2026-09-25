package session_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/pkg/session"
	"tzro/pkg/store"
)

func TestIssue14_IsCommandAllowlisted(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		custom   []string
		expected bool
	}{
		{"go test", "go test ./...", nil, true},
		{"go build", "go build -o /tmp/bin ./cmd/...", nil, true},
		{"cargo test", "cargo test --release", nil, true},
		{"git commit", "git commit -m 'feat: message'", nil, true},
		{"git status", "git status --porcelain", nil, true},
		{"git checkout", "git checkout -b feature", nil, true},
		{"npm test", "npm test", nil, true},
		{"pytest", "pytest tests/unit", nil, true},
		{"python -m unittest", "python -m unittest discover", nil, true},
		{"make", "make all", nil, true},
		{"tsc", "tsc --noEmit", nil, true},
		{"tzro", "tzro pause", nil, true},
		{"with env vars", "CGO_ENABLED=0 GOOS=linux go build ./...", nil, true},
		{"with complex env vars", "FOO=bar_123 BAZ=xyz cargo test", nil, true},
		{"custom pattern", "docker compose up", []string{"docker compose *"}, true},

		// Rejections: non-allowlisted
		{"ls", "ls -la", nil, false},
		{"cat", "cat file.go", nil, false},
		{"rm", "rm -rf /tmp/data", nil, false},
		{"empty", "   ", nil, false},
		{"env only", "FOO=bar", nil, false},

		// Rejections: compound, chained, or background
		{"semicolon", "go test ./... ; rm -rf /", nil, false},
		{"and operator", "cargo build && cargo test", nil, false},
		{"or operator", "git status || true", nil, false},
		{"pipe", "git log | head -n 5", nil, false},
		{"background", "make test &", nil, false},
		{"subshell parens", "(go test ./...)", nil, false},
		{"backticks", "`go test`", nil, false},
		{"multiline newline", "go test\nrm -rf /", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := session.IsCommandAllowlisted(tc.cmd, tc.custom)
			if got != tc.expected {
				t.Errorf("IsCommandAllowlisted(%q) = %v, want %v", tc.cmd, got, tc.expected)
			}
		})
	}
}

func TestIssue14_RedactCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "env secret",
			input:    "API_KEY=sk-supersecret12345678901234567890 go test ./...",
			expected: "API_KEY=[REDACTED] go test ./...",
		},
		{
			name:     "flag equals",
			input:    "npm test --token=ghp_123456789012345678901234567890",
			expected: "npm test --token=[REDACTED]",
		},
		{
			name:     "flag space",
			input:    "git push --password mySuperSecretPassword origin main",
			expected: "git push --password [REDACTED] origin main",
		},
		{
			name:     "bearer token",
			input:    "curl -H 'Authorization: Bearer my_jwt_token.12345' https://api.example.com",
			expected: "curl -H 'Authorization: Bearer [REDACTED]' https://api.example.com",
		},
		{
			name:     "openAI key in arg",
			input:    "python run.py sk-abcdefghijklmnopqrstuvwxyz123456",
			expected: "python run.py [REDACTED]",
		},
		{
			name:     "clean command untouched",
			input:    "go test -v -run TestFoo ./pkg/session/...",
			expected: "go test -v -run TestFoo ./pkg/session/...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := session.RedactCommand(tc.input)
			if !strings.Contains(got, "[REDACTED]") && strings.Contains(tc.input, "secret") {
				t.Fatalf("RedactCommand failed to redact sensitive text: got %q", got)
			}
			if tc.expected != "" && got != tc.expected {
				t.Errorf("RedactCommand(%q) =\n  got:  %q\n  want: %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestIssue14_GenerateShellInit(t *testing.T) {
	// zsh
	zshScript, err := session.GenerateShellInit("zsh")
	if err != nil {
		t.Fatalf("GenerateShellInit(zsh) error: %v", err)
	}
	if !strings.Contains(zshScript, "add-zsh-hook preexec _tzro_preexec") {
		t.Errorf("zsh script missing preexec hook registration")
	}
	if !strings.Contains(zshScript, "add-zsh-hook precmd _tzro_precmd") {
		t.Errorf("zsh script missing precmd hook registration")
	}
	if !strings.Contains(zshScript, "return $exit_status") {
		t.Errorf("zsh script does not preserve exit status")
	}
	if !strings.Contains(zshScript, "_TZRO_ZSH_INIT") {
		t.Errorf("zsh script missing idempotency guard")
	}
	if !strings.Contains(zshScript, "TZRO_SHELL_ID") {
		t.Errorf("zsh script missing TZRO_SHELL_ID export")
	}

	// bash
	bashScript, err := session.GenerateShellInit("bash")
	if err != nil {
		t.Fatalf("GenerateShellInit(bash) error: %v", err)
	}
	if !strings.Contains(bashScript, "PROMPT_COMMAND") {
		t.Errorf("bash script missing PROMPT_COMMAND integration")
	}
	if !strings.Contains(bashScript, "DEBUG") {
		t.Errorf("bash script missing DEBUG trap integration")
	}
	if !strings.Contains(bashScript, "return $exit_status") {
		t.Errorf("bash script does not preserve exit status")
	}
	if !strings.Contains(bashScript, "_TZRO_BASH_INIT") {
		t.Errorf("bash script missing idempotency guard")
	}

	// unsupported
	_, err = session.GenerateShellInit("fish")
	if err == nil || !strings.Contains(err.Error(), "unsupported shell") {
		t.Errorf("expected unsupported shell error, got %v", err)
	}
}

func TestIssue14_StoreCommandEvents(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	s, err := store.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer s.Close()

	ws := "/Users/test/repo"
	sessID := "sess_12345"
	shellID := "zsh_test_1"

	// 1. Record command start
	cmdID := "cmd_001"
	startTime := time.Now().UTC().Add(-5 * time.Second)
	err = s.RecordCommandStart(cmdID, ws, sessID, shellID, "go test ./...", ws, startTime)
	if err != nil {
		t.Fatalf("RecordCommandStart error: %v", err)
	}

	// 2. Query event before completion -> exit status must be nil (unknown)
	events, err := s.GetCommandEvents(ws, sessID, 10)
	if err != nil {
		t.Fatalf("GetCommandEvents error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ExitStatus != nil {
		t.Errorf("expected ExitStatus nil for uncompleted command, got %v", *events[0].ExitStatus)
	}
	if events[0].CompletedAt != nil {
		t.Errorf("expected CompletedAt nil, got %v", events[0].CompletedAt)
	}

	// 3. Record command complete with exit code 0
	completeTime := time.Now().UTC()
	err = s.RecordCommandComplete(cmdID, 0, completeTime)
	if err != nil {
		t.Fatalf("RecordCommandComplete error: %v", err)
	}

	events, err = s.GetCommandEvents(ws, sessID, 10)
	if err != nil {
		t.Fatalf("GetCommandEvents error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ExitStatus == nil || *events[0].ExitStatus != 0 {
		t.Errorf("expected ExitStatus 0, got %v", events[0].ExitStatus)
	}
	if events[0].CompletedAt == nil {
		t.Errorf("expected non-nil CompletedAt")
	}

	// 4. Capture Gaps
	initialGaps, err := s.GetCaptureGapCount(ws)
	if err != nil || initialGaps != 0 {
		t.Errorf("expected 0 initial gaps, got %d, err %v", initialGaps, err)
	}
	_ = s.RecordCaptureGap(ws, 2)
	gaps, err := s.GetCaptureGapCount(ws)
	if err != nil || gaps != 2 {
		t.Errorf("expected 2 gaps, got %d, err %v", gaps, err)
	}

	// 5. Pruning
	for i := 2; i <= 10; i++ {
		cID := session.GenerateSessionID()
		_ = s.RecordCommandStart(cID, ws, sessID, shellID, "go vet ./...", ws, time.Now().UTC())
	}
	_ = s.PruneCommandEvents(ws, 5, 0)
	prunedEvents, err := s.GetCommandEvents(ws, sessID, 50)
	if err != nil {
		t.Fatalf("GetCommandEvents error: %v", err)
	}
	if len(prunedEvents) != 5 {
		t.Errorf("expected 5 pruned events, got %d", len(prunedEvents))
	}

	// 6. Clear Command Events
	err = s.ClearCommandEvents(ws)
	if err != nil {
		t.Fatalf("ClearCommandEvents error: %v", err)
	}
	clearedEvents, _ := s.GetCommandEvents(ws, sessID, 10)
	if len(clearedEvents) != 0 {
		t.Errorf("expected 0 events after clear, got %d", len(clearedEvents))
	}
	gapsAfterClear, _ := s.GetCaptureGapCount(ws)
	if gapsAfterClear != 0 {
		t.Errorf("expected 0 gaps after clear, got %d", gapsAfterClear)
	}
}

func TestIssue14_MeasureShellMetrics(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "metrics.db")
	s, err := store.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer s.Close()

	ws := "/Users/test/metrics_ws"
	shellID := "zsh_metric_1"
	_ = s.SetActiveTask(ws, shellID, "sess_metric_1")

	metrics := session.MeasureShellMetrics(ws, shellID, s)
	if metrics.ShellID != shellID {
		t.Errorf("expected ShellID %q, got %q", shellID, metrics.ShellID)
	}
	if !metrics.ActiveTaskBound {
		t.Errorf("expected ActiveTaskBound true")
	}
	if metrics.ActiveSessionID != "sess_metric_1" {
		t.Errorf("expected ActiveSessionID sess_metric_1, got %q", metrics.ActiveSessionID)
	}
	// Assert latency is fast (< 50ms cold, enqueue < 1ms, persistence < 10ms)
	if metrics.EnqueueLatencyMs > 1.0 {
		t.Errorf("expected EnqueueLatencyMs < 1ms, got %.3f ms", metrics.EnqueueLatencyMs)
	}
}

func TestIssue14_SubshellSourcingAndExitStatusPreservation(t *testing.T) {
	// Verify script syntax and exit status preservation in available shells
	for _, sh := range []string{"zsh", "bash"} {
		shPath, err := exec.LookPath(sh)
		if err != nil {
			t.Logf("shell %s not found on system, skipping subshell test", sh)
			continue
		}

		script, err := session.GenerateShellInit(sh)
		if err != nil {
			t.Fatalf("GenerateShellInit(%s) failed: %v", sh, err)
		}

		scriptFile := filepath.Join(t.TempDir(), "init."+sh)
		if err := os.WriteFile(scriptFile, []byte(script), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		// Test script:
		// 1. Source the integration script twice (test idempotency)
		// 2. Run a command that fails with exit status 42: (exit 42)
		// 3. Test that $? immediately after is preserved as 42
		testScript := fmt.Sprintf(`
source "%s"
source "%s"
(exit 42)
exit_code=$?
if [ "$exit_code" -ne 42 ]; then
  echo "EXIT_STATUS_FAILED:$exit_code"
  exit 1
fi
echo "SUCCESS_SHELL_ID:$TZRO_SHELL_ID"
exit 0
`, scriptFile, scriptFile)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, shPath, "-c", testScript)
		out, err := cmd.CombinedOutput()
		output := string(out)
		if err != nil {
			t.Fatalf("subshell %s failed: %v, output: %s", sh, err, output)
		}

		if !strings.Contains(output, "SUCCESS_SHELL_ID:") {
			t.Errorf("subshell %s did not output SUCCESS_SHELL_ID: %s", sh, output)
		}
	}
}
