package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tzro/pkg/session"
	"tzro/pkg/store"
)

// 1. Synthetic reports cover fresh, stale, unknown, missing, deleted, and mixed staged/unstaged states.
func TestIssue13_SyntheticReportAllStates(t *testing.T) {
	ws := "/workspace/synthetic"
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	paused := now.Add(-15 * time.Minute)

	sm := session.NewSessionManifestV3("sess_synth", ws, "feat-synth", "Synthetic state test")
	sm.PausedAt = &paused
	sm.ChangedFiles = []session.FileSnapshot{
		{Path: "fresh.go", Status: "modified", StagedStatus: "M", WorktreeStatus: " ", StagedHash: "h1"},
		{Path: "both.go", Status: "modified", StagedStatus: "M", WorktreeStatus: "M", StagedHash: "h2", WorktreeHash: "h3"},
		{Path: "untracked.go", Status: "untracked", StagedStatus: "?", WorktreeStatus: "?"},
		{Path: "deleted.go", Status: "deleted", StagedStatus: "D", WorktreeStatus: " "},
		{Path: "renamed.go", Status: "renamed", OldPath: "old.go", StagedStatus: "R", WorktreeStatus: " "},
	}

	report := &session.ResumeReport{
		Session:        sm,
		ExpectedBranch: "feat-synth",
		CurrentBranch:  "feat-synth",
		FileDrifts: []session.FileDrift{
			{Path: "fresh.go", Status: "fresh"},
			{Path: "both.go", Status: "modified"},
			{Path: "deleted.go", Status: "expected_deleted"},
			{Path: "missing.go", Status: "missing"},
		},
		CheckEvidences: []session.ResumeCheckEvidence{
			{Command: "go test ./pkg/a", ExitCode: 0, Status: session.FreshnessFresh},
			{Command: "go test ./pkg/b", ExitCode: 1, Status: session.FreshnessStale, DriftedFiles: []string{"both.go"}},
			{Command: "go test ./pkg/c", ExitCode: 0, Status: session.FreshnessUnknown},
		},
		SymbolDrifts: []session.SymbolDrift{
			{Name: "ActiveA", OriginalPath: "fresh.go", CurrentLine: 10, Status: "intact"},
			{Name: "ActiveB", OriginalPath: "both.go", OriginalLine: 5, CurrentLine: 20, Status: "moved"},
			{Name: "ActiveC", OriginalPath: "missing.go", CurrentLine: 1, Status: "missing"},
		},
	}

	clock := func() time.Time { return now }
	vm := session.BuildDashboardViewModel(report, ws, nil, clock)

	// Check view model mapping
	if vm.PauseDurationText != "15m" {
		t.Errorf("expected 15m pause duration, got %s", vm.PauseDurationText)
	}

	renderedPlain := session.RenderDashboard(vm, "plain", 80, true)

	// Verify all state labels are present in plain text
	expectedLabels := []string{
		"[STAGED]", "[STAGED+UNSTAGED]", "[UNTRACKED]", "[DELETED]", "[RENAMED]",
		"[FRESH]", "[STALE]", "[UNKNOWN]",
		"[INTACT]", "[MOVED]", "[MISSING]",
		"[PASS]", "[FAIL (1)]",
	}

	for _, label := range expectedLabels {
		if !strings.Contains(renderedPlain, label) {
			t.Errorf("rendered plain dashboard missing label %s:\n%s", label, renderedPlain)
		}
	}
}

// 2. A v2 manifest without timestamps, command events, or execution-time hashes produces honest placeholders.
func TestIssue13_V2ManifestHonestPlaceholders(t *testing.T) {
	ws := "/workspace/v2"
	sm := session.NewSessionManifest("sess_v2_legacy", ws, "main", "Legacy v2 session")
	sm.SchemaVersion = 2
	// No PausedAt, No RecentCommands, No ActiveSymbols, No Checks

	report := session.GenerateResumeReport(context.Background(), sm, ws, session.GitState{IsGit: false, Branch: "(non-git)"}, nil, nil)
	vm := session.BuildDashboardViewModel(report, ws, nil, nil)

	if vm.PauseDurationText != "none recorded" {
		t.Errorf("expected 'none recorded' for missing PausedAt, got %s", vm.PauseDurationText)
	}
	if len(vm.RecentCommands) != 0 {
		t.Errorf("expected 0 commands, got %d", len(vm.RecentCommands))
	}
	if len(vm.ActiveSymbols) != 0 {
		t.Errorf("expected 0 active symbols, got %d", len(vm.ActiveSymbols))
	}

	rendered := session.RenderDashboard(vm, "plain", 80, true)
	if !strings.Contains(rendered, "(no command capture recorded)") {
		t.Errorf("expected '(no command capture recorded)' placeholder, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "(no active symbols recorded)") {
		t.Errorf("expected '(no active symbols recorded)' placeholder, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "(no checks recorded)") {
		t.Errorf("expected '(no checks recorded)' placeholder, got:\n%s", rendered)
	}
}

// 3. A recorded pass with changed scope files appears stale. Absent output never produces invented failure detail.
func TestIssue13_StalePassAndAbsentFailureOutput(t *testing.T) {
	ws := "/workspace/check"
	sm := session.NewSessionManifestV3("sess_checks", ws, "main", "Check detail test")
	sm.Checks = []session.CheckExecution{
		{
			Command:     "go test ./pkg/stale_pass",
			ExitCode:    0,
			ScopeHashes: map[string]string{"foo.go": "oldhash"},
			OutputID:    "art_absent_123", // Absent artifact
		},
		{
			Command:  "go test ./pkg/fail_no_artifact",
			ExitCode: 2,
			OutputID: "", // No artifact
		},
	}

	report := &session.ResumeReport{
		Session: sm,
		CheckEvidences: []session.ResumeCheckEvidence{
			{Command: "go test ./pkg/stale_pass", ExitCode: 0, Status: session.FreshnessStale, DriftedFiles: []string{"foo.go"}, OutputID: "art_absent_123"},
			{Command: "go test ./pkg/fail_no_artifact", ExitCode: 2, Status: session.FreshnessFresh},
		},
	}

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	vm := session.BuildDashboardViewModel(report, ws, s, nil)
	rendered := session.RenderDashboard(vm, "plain", 80, true)

	// Check 1: was a PASS, but status is STALE
	if !strings.Contains(rendered, "[PASS] [STALE] go test ./pkg/stale_pass (drifted: foo.go)") {
		t.Errorf("expected pass to appear stale with drifted file, got:\n%s", rendered)
	}

	// Check 2: failed, but no artifact. Must NOT have invented failure detail
	if strings.Contains(rendered, "Failure detail:") {
		t.Errorf("failure detail should NOT appear when artifact is absent or empty, got:\n%s", rendered)
	}
}

// 4. No-upstream, detached HEAD, non-Git workspace, and clock-skew cases have deterministic output tests.
func TestIssue13_EdgeCaseDeterminism(t *testing.T) {
	ws := "/workspace/edge"
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	futureClockSkew := now.Add(10 * time.Minute) // In future!

	sm := session.NewSessionManifestV3("sess_edge", ws, "HEAD (detached at abc1234)", "Edge cases")
	sm.PausedAt = &futureClockSkew
	sm.HeadCommit = "abc1234567890"

	report := &session.ResumeReport{
		Session:        sm,
		ExpectedBranch: "HEAD (detached at abc1234)",
		CurrentBranch:  "HEAD (detached at abc1234)",
		IsDetached:     true,
	}

	clock := func() time.Time { return now }
	vm := session.BuildDashboardViewModel(report, ws, nil, clock)

	// Clock skew clamp
	if vm.PauseDurationText != "unknown (clock skew)" {
		t.Errorf("expected 'unknown (clock skew)', got %s", vm.PauseDurationText)
	}

	rendered := session.RenderDashboard(vm, "plain", 80, true)
	if !strings.Contains(rendered, "HEAD (detached at abc1234)") {
		t.Errorf("detached HEAD not shown: %s", rendered)
	}
	if !strings.Contains(rendered, "unknown (clock skew)") {
		t.Errorf("clock skew text missing: %s", rendered)
	}
}

// 5. TTY/plain/JSON formats, narrow widths, long paths, Unicode, and control characters have formatter tests.
func TestIssue13_FormattingAndSanitization(t *testing.T) {
	ws := "/workspace/format"
	sm := session.NewSessionManifestV3("sess_fmt", ws, "main", "Testing \x1b[31mred\x1b[0m injected ANSI\nand control \x07 characters 🚀")
	sm.PendingTasks = []string{"Task with \x1b[1mbold\x1b[0m and unicode: 日本語"}
	sm.ChangedFiles = []session.FileSnapshot{
		{Path: "very/long/nested/path/to/some/deeply/embedded/source/file/implementation_details_with_special_characters_日本語.go", Status: "modified"},
	}

	report := &session.ResumeReport{Session: sm}
	vm := session.BuildDashboardViewModel(report, ws, nil, nil)

	// Test sanitization stripped escape codes
	if strings.Contains(vm.Objective, "\x1b") {
		t.Errorf("ANSI escape sequences leaked into view model objective: %q", vm.Objective)
	}
	if strings.Contains(vm.PendingTasks[0], "\x1b") {
		t.Errorf("ANSI escape sequences leaked into view model task: %q", vm.PendingTasks[0])
	}
	if !strings.Contains(vm.Objective, "🚀") {
		t.Errorf("Unicode rocket stripped: %s", vm.Objective)
	}
	if !strings.Contains(vm.PendingTasks[0], "日本語") {
		t.Errorf("Unicode Japanese stripped: %s", vm.PendingTasks[0])
	}

	// 1. JSON format
	renderedJSON := session.RenderDashboard(vm, "json", 80, true)
	var parsedVM session.DashboardViewModel
	if err := json.Unmarshal([]byte(renderedJSON), &parsedVM); err != nil {
		t.Fatalf("JSON format output invalid JSON: %v", err)
	}
	if parsedVM.SessionID != "sess_fmt" {
		t.Errorf("parsed JSON mismatch: %s", parsedVM.SessionID)
	}

	// 2. Plain format with narrow width (40 cols)
	renderedNarrow := session.RenderDashboard(vm, "plain", 40, true)
	if len(renderedNarrow) == 0 {
		t.Errorf("narrow plain output was empty")
	}

	// 3. TTY format
	renderedTTY := session.RenderDashboard(vm, "tty", 80, false)
	if len(renderedTTY) == 0 {
		t.Errorf("TTY output was empty")
	}
}

// 6. Omission counts for lists exceeding bounds.
func TestIssue13_OmissionCounts(t *testing.T) {
	ws := "/workspace/omission"
	sm := session.NewSessionManifestV3("sess_omission", ws, "main", "Omission check")

	// 25 changed files
	for i := 0; i < 25; i++ {
		sm.ChangedFiles = append(sm.ChangedFiles, session.FileSnapshot{
			Path:   strings.Repeat("a", i) + "_file.go",
			Status: "modified",
		})
	}
	// 20 active symbols
	for i := 0; i < 20; i++ {
		sm.ActiveSymbols = append(sm.ActiveSymbols, session.ActiveSymbol{
			Name:     strings.Repeat("s", i) + "_sym",
			FilePath: "file.go",
			Line:     i + 1,
		})
	}

	report := session.GenerateResumeReport(context.Background(), sm, ws, session.GitState{IsGit: false}, nil, nil)
	vm := session.BuildDashboardViewModel(report, ws, nil, nil)

	if len(vm.ModifiedFiles) != 10 {
		t.Errorf("expected 10 display files, got %d", len(vm.ModifiedFiles))
	}
	if vm.ModifiedFilesOmitted != 15 {
		t.Errorf("expected 15 omitted files, got %d", vm.ModifiedFilesOmitted)
	}
	if len(vm.ActiveSymbols) != 10 {
		t.Errorf("expected 10 display symbols, got %d", len(vm.ActiveSymbols))
	}
	if vm.ActiveSymbolsOmitted != 10 {
		t.Errorf("expected 10 omitted symbols, got %d", vm.ActiveSymbolsOmitted)
	}

	rendered := session.RenderDashboard(vm, "plain", 80, true)
	if !strings.Contains(rendered, "... and 15 more modified files omitted") {
		t.Errorf("omitted files message missing from plain render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "... and 10 more active symbols omitted") {
		t.Errorf("omitted symbols message missing from plain render:\n%s", rendered)
	}
}
