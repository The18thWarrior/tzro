package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEditAndVerify_CorrectEditWithCheckPass verifies that a correct
// replacement edit through the service produces an applied status with
// an observed native check pass (E057 acceptance case 1).
func TestEditAndVerify_CorrectEditWithCheckPass(t *testing.T) {
	ws := setupGoProject(t)

	// Write a preset.
	writePreset(t, ws, `
version: 1
timeout: 30s
checks:
  - id: tests
    argv: [go, test, -count=1, ./...]
    cwd: .
`)

	// Create the service.
	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	runner := NewExactCommandRunner(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: runner,
	})

	// Submit a correct fix.
	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "calc.go",
				OldText: "return a - b",
				NewText: "return a + b",
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	summary := svc.ApplyAndVerify(ctx, req, Options{Timeout: 30 * time.Second})

	// Assert application status.
	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationApplied)
	}
	if summary.Schema != "tzro.verification.v1" {
		t.Errorf("schema = %q, want %q", summary.Schema, "tzro.verification.v1")
	}

	// Assert file was changed.
	if len(summary.ChangedFiles) != 1 || summary.ChangedFiles[0] != "calc.go" {
		t.Errorf("changed_files = %v, want [calc.go]", summary.ChangedFiles)
	}

	// Verify actual file contents.
	data, err := os.ReadFile(filepath.Join(ws, "calc.go"))
	if err != nil {
		t.Fatalf("cannot read calc.go: %v", err)
	}
	if got := string(data); got != correctCalcGo {
		t.Errorf("calc.go content mismatch:\ngot: %s\nwant: %s", got, correctCalcGo)
	}

	// Assert verification passed.
	if summary.Verification != VerificationPassed {
		t.Errorf("verification = %q, want %q", summary.Verification, VerificationPassed)
	}

	// Assert check outcome.
	if len(summary.Checks) != 1 {
		t.Fatalf("checks count = %d, want 1", len(summary.Checks))
	}
	check := summary.Checks[0]
	if check.ID != "tests" {
		t.Errorf("check.id = %q, want %q", check.ID, "tests")
	}
	if check.Status != CheckPassed {
		t.Errorf("check.status = %q, want %q", check.Status, CheckPassed)
	}
	if check.ExitCode == nil || *check.ExitCode != 0 {
		t.Errorf("check.exit_code = %v, want 0", check.ExitCode)
	}

	// Assert termination.
	if summary.Termination != TerminationCompleted {
		t.Errorf("termination = %q, want %q", summary.Termination, TerminationCompleted)
	}

	// Verify text formatting produces output.
	text := summary.FormatText()
	if text == "" {
		t.Error("FormatText returned empty string")
	}

	// Verify ToMap produces a valid map.
	m := summary.ToMap()
	if m["schema"] != "tzro.verification.v1" {
		t.Errorf("ToMap schema = %v, want tzro.verification.v1", m["schema"])
	}
}

// TestEditAndVerify_WrongEditRetainsEditsWithFailedCheck verifies that
// a wrong fix is applied, the check fails, and edits are NOT rolled back
// (E057 acceptance case 2).
func TestEditAndVerify_WrongEditRetainsEditsWithFailedCheck(t *testing.T) {
	ws := setupGoProject(t)

	writePreset(t, ws, `
version: 1
timeout: 30s
checks:
  - id: tests
    argv: [go, test, -count=1, ./...]
    cwd: .
`)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	// Submit a wrong fix.
	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "calc.go",
				OldText: "return a - b",
				NewText: "return a * b",
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	summary := svc.ApplyAndVerify(ctx, req, Options{Timeout: 30 * time.Second})

	// Edits must be applied (not rolled back).
	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationApplied)
	}

	// Verify the wrong fix is still on disk.
	data, err := os.ReadFile(filepath.Join(ws, "calc.go"))
	if err != nil {
		t.Fatalf("cannot read calc.go: %v", err)
	}
	if got := string(data); got == brokenCalcGo {
		t.Error("calc.go was rolled back to original; edits must be retained")
	}

	// Verification must fail.
	if summary.Verification != VerificationFailed {
		t.Errorf("verification = %q, want %q", summary.Verification, VerificationFailed)
	}

	// Check must have failed with diagnostics.
	if len(summary.Checks) != 1 {
		t.Fatalf("checks count = %d, want 1", len(summary.Checks))
	}
	check := summary.Checks[0]
	if check.Status != CheckFailed {
		t.Errorf("check.status = %q, want %q", check.Status, CheckFailed)
	}
	if check.ExitCode == nil {
		t.Error("check.exit_code should not be nil for a failed check")
	}
	if len(check.Diagnostics) == 0 {
		t.Error("check.diagnostics should contain failure evidence")
	}
}

// TestEditAndVerify_MultiFileConflictRejectsBatch verifies that a conflicting
// batch is rejected before any writes (E057 acceptance case 3).
func TestEditAndVerify_MultiFileConflictRejectsBatch(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	// First edit is valid, second has a match count conflict.
	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "calc.go",
				OldText: "return a - b",
				NewText: "return a + b",
			},
			{
				Kind:            "replace",
				Path:            "calc_test.go",
				OldText:         "THIS TEXT DOES NOT EXIST",
				NewText:         "something",
				ExpectedMatches: 1,
			},
		},
	}

	ctx := context.Background()
	summary := svc.ApplyAndVerify(ctx, req, Options{})

	// Entire batch must be rejected.
	if summary.Application != ApplicationRejected {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationRejected)
	}

	// Verify the first file was NOT changed.
	data, err := os.ReadFile(filepath.Join(ws, "calc.go"))
	if err != nil {
		t.Fatalf("cannot read calc.go: %v", err)
	}
	if string(data) != brokenCalcGo {
		t.Error("calc.go was modified despite batch rejection; preflight must prevent writes")
	}

	// Verification should be unavailable.
	if summary.Verification != VerificationUnavailable {
		t.Errorf("verification = %q, want %q", summary.Verification, VerificationUnavailable)
	}
}

// TestEditAndVerify_MissingPresetReportsNotConfigured verifies that
// missing verification configuration applies edits but reports
// verification as not_configured (E057 acceptance case 4).
func TestEditAndVerify_MissingPresetReportsNotConfigured(t *testing.T) {
	ws := setupGoProject(t) // No preset written.

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "calc.go",
				OldText: "return a - b",
				NewText: "return a + b",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	// Edits must be applied.
	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationApplied)
	}

	// Verification not configured.
	if summary.Verification != VerificationNotConfigured {
		t.Errorf("verification = %q, want %q", summary.Verification, VerificationNotConfigured)
	}
}

// CLI adapter outcomes are covered through the real process in cmd/tzro/cli_edit_verify_test.go.

// TestEditAndVerify_MultiFileCreation verifies create edits for new files
// (E057 acceptance case 6).
func TestEditAndVerify_MultiFileCreation(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "create",
				Path:    "new_file.go",
				Content: "package calc\n\nfunc Multiply(a, b int) int { return a * b }\n",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationApplied)
	}

	// Verify the file was created.
	data, err := os.ReadFile(filepath.Join(ws, "new_file.go"))
	if err != nil {
		t.Fatalf("new_file.go not created: %v", err)
	}
	if string(data) != "package calc\n\nfunc Multiply(a, b int) int { return a * b }\n" {
		t.Errorf("new_file.go content mismatch: %s", string(data))
	}
}

// TestEditAndVerify_CreateExistingFileRejects verifies that creating
// a file that already exists is rejected (E057 acceptance case 6b).
func TestEditAndVerify_CreateExistingFileRejects(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "create",
				Path:    "calc.go",
				Content: "overwrite attempt",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Application != ApplicationRejected {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationRejected)
	}
}

// TestEditAndVerify_AmbiguousMatchRejects verifies that an edit with
// wrong match count is rejected (E057 acceptance case 7).
func TestEditAndVerify_AmbiguousMatchRejects(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	// This old_text appears once, but we expect 2 matches.
	req := Request{
		Edits: []Edit{
			{
				Kind:            "replace",
				Path:            "calc.go",
				OldText:         "return a - b",
				NewText:         "return a + b",
				ExpectedMatches: 2,
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Application != ApplicationRejected {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationRejected)
	}
}

// TestEditAndVerify_EmptyBatchRejects verifies that an empty edit batch
// is rejected (E057 acceptance case 9).
func TestEditAndVerify_EmptyBatchRejects(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{Edits: []Edit{}}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Application != ApplicationRejected {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationRejected)
	}
	if summary.RejectionReason != "empty edit batch" {
		t.Errorf("rejection_reason = %q, want %q", summary.RejectionReason, "empty edit batch")
	}
}

// TestEditAndVerify_PathEscapeRejected verifies path traversal is blocked.
func TestEditAndVerify_PathEscapeRejected(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "../../../etc/passwd",
				OldText: "root",
				NewText: "hacked",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Application != ApplicationRejected {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationRejected)
	}
}

// TestEditAndVerify_AbsolutePathRejected verifies absolute paths are blocked.
func TestEditAndVerify_AbsolutePathRejected(t *testing.T) {
	ws := setupGoProject(t)

	fa, err := NewWorkspaceFileAccess(ws)
	if err != nil {
		t.Fatalf("NewWorkspaceFileAccess: %v", err)
	}
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "/tmp/evil.go",
				OldText: "x",
				NewText: "y",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Application != ApplicationRejected {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationRejected)
	}
}

// --- Test fixtures ---

const brokenCalcGo = `package calc

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a - b
}
`

const correctCalcGo = `package calc

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a + b
}
`

const calcTestGo = `package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Errorf("Add(2, 3) = %d, want 5", got)
	}
}
`

const goModContent = `module calc

go 1.26
`

// setupGoProject creates a temporary Go project with a deliberately broken
// calculation and a test that will fail until corrected.
func setupGoProject(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()

	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "calc.go"), []byte(brokenCalcGo), 0644); err != nil {
		t.Fatalf("write calc.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "calc_test.go"), []byte(calcTestGo), 0644); err != nil {
		t.Fatalf("write calc_test.go: %v", err)
	}

	return ws
}

// writePreset creates a .tzro/verification.yaml in the workspace.
func writePreset(t *testing.T, ws string, content string) {
	t.Helper()
	dir := filepath.Join(ws, ".tzro")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir .tzro: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verification.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
}
