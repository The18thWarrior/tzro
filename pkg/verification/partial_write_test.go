package verification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEditAndVerify_WriteFailureReportsPartial tests that a write failure
// after one successful write reports partial application with correct
// changed and uncertain file lists (E057 Slice 5).
func TestEditAndVerify_WriteFailureReportsPartial(t *testing.T) {
	ws := setupGoProject(t)
	writePreset(t, ws, `
version: 1
timeout: 30s
checks:
  - id: tests
    argv: [go, test, -count=1, ./...]
    cwd: .
`)

	// Use a fault-injecting file access that fails on the second write.
	real, _ := NewWorkspaceFileAccess(ws)
	faultyFA := &faultingFileAccess{
		real:        real,
		failOnWrite: 1, // fail on second write (0-indexed)
		writeCount:  0,
	}

	svc := NewService(ws, Dependencies{
		FileAccess:    faultyFA,
		CommandRunner: NewExactCommandRunner(ws),
	})

	// Create a new file first (succeeds), then replace in calc.go (fails).
	req := Request{
		Edits: []Edit{
			{
				Kind:    "create",
				Path:    "new_helper.go",
				Content: "package calc\n\nfunc Helper() {}\n",
			},
			{
				Kind:    "replace",
				Path:    "calc.go",
				OldText: "return a - b",
				NewText: "return a + b",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	// Must report partial application.
	if summary.Application != ApplicationPartial {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationPartial)
	}

	// First file (new_helper.go) should be in changed.
	foundChanged := false
	for _, f := range summary.ChangedFiles {
		if f == "new_helper.go" {
			foundChanged = true
		}
	}
	if !foundChanged {
		t.Errorf("changed_files = %v, want to contain new_helper.go", summary.ChangedFiles)
	}

	// Second file (calc.go) should be uncertain.
	foundUncertain := false
	for _, f := range summary.UncertainFiles {
		if f == "calc.go" {
			foundUncertain = true
		}
	}
	if !foundUncertain {
		t.Errorf("uncertain_files = %v, want to contain calc.go", summary.UncertainFiles)
	}

	// Verification must not have run.
	if summary.Verification != VerificationUnavailable {
		t.Errorf("verification = %q, want %q", summary.Verification, VerificationUnavailable)
	}

	// Termination must report application error.
	if summary.Termination != TerminationApplicationError {
		t.Errorf("termination = %q, want %q", summary.Termination, TerminationApplicationError)
	}
}

// TestEditAndVerify_WriteFailureBeforeAnyWrite tests that a failure
// before any file is written reports not_applied, not partial.
func TestEditAndVerify_WriteFailureBeforeAnyWrite(t *testing.T) {
	ws := setupGoProject(t)

	real, _ := NewWorkspaceFileAccess(ws)
	faultyFA := &faultingFileAccess{
		real:        real,
		failOnWrite: 0, // fail on first write
	}

	svc := NewService(ws, Dependencies{
		FileAccess:    faultyFA,
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

	if summary.Application != ApplicationNotApplied {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationNotApplied)
	}
}

// TestEditAndVerify_DependencyScheduling_FailureBlocksDependent tests that
// a failed prerequisite blocks its dependent check (Slice 7).
func TestEditAndVerify_DependencyScheduling_FailureBlocksDependent(t *testing.T) {
	ws := t.TempDir()
	// Create a simple file to edit.
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	// Create three checks: A fails, B depends on A (blocked), C is independent.
	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: check_a
    argv: [sh, -c, "echo 'check A failed' >&2; exit 1"]
    cwd: .
  - id: check_b
    argv: [sh, -c, "echo 'check B ran'"]
    cwd: .
    depends_on: [check_a]
  - id: check_c
    argv: [sh, -c, "echo 'check C ran'"]
    cwd: .
`), 0644)

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "hello.txt",
				OldText: "hello",
				NewText: "world",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	// Edit must be applied.
	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want %q", summary.Application, ApplicationApplied)
	}

	// Must have 3 check results.
	if len(summary.Checks) != 3 {
		t.Fatalf("checks count = %d, want 3", len(summary.Checks))
	}

	// Check A: failed.
	if summary.Checks[0].ID != "check_a" {
		t.Errorf("check[0].id = %q, want check_a", summary.Checks[0].ID)
	}
	if summary.Checks[0].Status != CheckFailed {
		t.Errorf("check_a.status = %q, want %q", summary.Checks[0].Status, CheckFailed)
	}

	// Check B: blocked (depends on failed check_a).
	if summary.Checks[1].ID != "check_b" {
		t.Errorf("check[1].id = %q, want check_b", summary.Checks[1].ID)
	}
	if summary.Checks[1].Status != CheckBlocked {
		t.Errorf("check_b.status = %q, want %q (blocked by failed check_a)", summary.Checks[1].Status, CheckBlocked)
	}

	// Check C: passed (independent of A).
	if summary.Checks[2].ID != "check_c" {
		t.Errorf("check[2].id = %q, want check_c", summary.Checks[2].ID)
	}
	if summary.Checks[2].Status != CheckPassed {
		t.Errorf("check_c.status = %q, want %q", summary.Checks[2].Status, CheckPassed)
	}

	// Overall verification: failed (A failed).
	if summary.Verification != VerificationFailed {
		t.Errorf("verification = %q, want %q", summary.Verification, VerificationFailed)
	}
}

// TestEditAndVerify_DependencyScheduling_PassedPrerequisiteAllowsDependent
// tests that a passed prerequisite allows its dependent to run.
func TestEditAndVerify_DependencyScheduling_PassedPrerequisiteAllowsDependent(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: format
    argv: [sh, -c, "echo 'format ok'"]
    cwd: .
  - id: test
    argv: [sh, -c, "echo 'tests ok'"]
    cwd: .
    depends_on: [format]
`), 0644)

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "hello.txt",
				OldText: "hello",
				NewText: "world",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if len(summary.Checks) != 2 {
		t.Fatalf("checks count = %d, want 2", len(summary.Checks))
	}

	// Both should pass.
	for _, c := range summary.Checks {
		if c.Status != CheckPassed {
			t.Errorf("check %s: status = %q, want passed", c.ID, c.Status)
		}
	}

	if summary.Verification != VerificationPassed {
		t.Errorf("verification = %q, want passed", summary.Verification)
	}
}

// TestEditAndVerify_DeadlineStopsSecondCheck verifies that a tight deadline
// stops the second check after the first completes (Slice 6).
func TestEditAndVerify_DeadlineStopsSecondCheck(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	// First check: instant. Second check: sleeps 10s (will be killed by deadline).
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: quick_check
    argv: [sh, -c, "echo 'quick done'"]
    cwd: .
  - id: slow_check
    argv: [sh, -c, "sleep 10"]
    cwd: .
`), 0644)

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "hello.txt",
				OldText: "hello",
				NewText: "world",
			},
		},
	}

	// Use a tight 500ms deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	summary := svc.ApplyAndVerify(ctx, req, Options{Timeout: 500 * time.Millisecond})

	// Edit must be applied.
	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want applied", summary.Application)
	}

	// Must have 2 check results.
	if len(summary.Checks) < 2 {
		t.Fatalf("checks count = %d, want >= 2", len(summary.Checks))
	}

	// First check should have passed.
	if summary.Checks[0].Status != CheckPassed {
		t.Errorf("quick_check.status = %q, want passed", summary.Checks[0].Status)
	}

	// Second check should be timed_out or not_run.
	secondStatus := summary.Checks[1].Status
	if secondStatus != CheckTimedOut && secondStatus != CheckNotRun {
		t.Errorf("slow_check.status = %q, want timed_out or not_run", secondStatus)
	}

	// Termination should be deadline.
	if summary.Termination != TerminationDeadline {
		t.Errorf("termination = %q, want deadline", summary.Termination)
	}

	// Overall verification must not be passed.
	if summary.Verification == VerificationPassed {
		t.Error("verification should not be passed when a check was timed out")
	}
}

// TestEditAndVerify_MissingExecutable tests that a missing command
// executable marks the check as failed, not crashed (Slice 4 extension).
func TestEditAndVerify_MissingExecutable(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: missing_tool
    argv: [nonexistent_binary_xyz_123, --check]
    cwd: .
`), 0644)

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
	})

	req := Request{
		Edits: []Edit{
			{
				Kind:    "replace",
				Path:    "hello.txt",
				OldText: "hello",
				NewText: "world",
			},
		},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	// Edit applied.
	if summary.Application != ApplicationApplied {
		t.Errorf("application = %q, want applied", summary.Application)
	}

	// Check reports failure, not crash.
	if len(summary.Checks) != 1 {
		t.Fatalf("checks count = %d, want 1", len(summary.Checks))
	}
	if summary.Checks[0].Status != CheckFailed {
		t.Errorf("check.status = %q, want failed", summary.Checks[0].Status)
	}
	if len(summary.Checks[0].Diagnostics) == 0 {
		t.Error("missing executable should produce a diagnostic")
	}

	// Verification failed.
	if summary.Verification != VerificationFailed {
		t.Errorf("verification = %q, want failed", summary.Verification)
	}
}

// --- Fault injection helpers ---

// faultingFileAccess wraps a real FileAccess and injects write failures.
type faultingFileAccess struct {
	real        FileAccess
	failOnWrite int // which write call to fail (0-indexed)
	writeCount  int
}

func (f *faultingFileAccess) Read(ctx context.Context, path string) (FileState, error) {
	return f.real.Read(ctx, path)
}

func (f *faultingFileAccess) Write(ctx context.Context, path string, state FileState) error {
	if f.writeCount == f.failOnWrite {
		f.writeCount++
		return fmt.Errorf("injected write failure for testing")
	}
	f.writeCount++
	return f.real.Write(ctx, path, state)
}
