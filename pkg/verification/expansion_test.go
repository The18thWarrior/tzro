package verification

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditAndVerify_LargeFailureRetainsEvidence tests that a check
// with more than 500 bytes of failure output stores the full output
// in the evidence store and includes an expand reference (Slice 9).
func TestEditAndVerify_LargeFailureRetainsEvidence(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	// Create a check that produces large output.
	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: verbose_check
    argv: [sh, -c, "for i in $(seq 1 100); do echo \"ERROR: test case $i failed with a very long diagnostic message that exceeds the threshold\"; done; exit 1"]
    cwd: .
`), 0644)

	// Create a mock evidence store to capture retention calls.
	store := &mockEvidenceStore{}

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
		EvidenceStore: store,
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

	// Check failed.
	if len(summary.Checks) != 1 {
		t.Fatalf("checks count = %d, want 1", len(summary.Checks))
	}
	check := summary.Checks[0]
	if check.Status != CheckFailed {
		t.Errorf("check.status = %q, want failed", check.Status)
	}

	// Diagnostics must be capped at 10 lines.
	if len(check.Diagnostics) > 10 {
		t.Errorf("diagnostics count = %d, want <= 10", len(check.Diagnostics))
	}

	// OmittedDetail must reference expand.
	if check.OmittedDetail == "" {
		t.Error("omitted_detail should indicate full output is available via expand")
	}
	if !strings.Contains(check.OmittedDetail, "tzro expand") {
		t.Errorf("omitted_detail = %q, should mention tzro expand", check.OmittedDetail)
	}

	// Evidence must have been retained.
	if store.retainCount == 0 {
		t.Error("evidence store was not called for large failure output")
	}
	if store.lastEvidence.CheckID != "verbose_check" {
		t.Errorf("retained evidence check_id = %q, want verbose_check", store.lastEvidence.CheckID)
	}
	if len(store.lastEvidence.Output) < 500 {
		t.Errorf("retained output too small: %d bytes", len(store.lastEvidence.Output))
	}

	// Evidence ref must be on the check result.
	if check.EvidenceRef == nil {
		t.Error("check.evidence_ref should be set after retention")
	} else if !check.EvidenceRef.Retained {
		t.Error("evidence_ref.retained should be true")
	}

	// Retention status must reflect success.
	if summary.Retention != RetentionRetained {
		t.Errorf("retention = %q, want retained", summary.Retention)
	}
}

// TestEditAndVerify_SmallFailureNoRetention tests that a small failure
// output is not stored in the evidence store (not worth the overhead).
func TestEditAndVerify_SmallFailureNoRetention(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: small_check
    argv: [sh, -c, "echo 'FAIL: small error'; exit 1"]
    cwd: .
`), 0644)

	store := &mockEvidenceStore{}

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
		EvidenceStore: store,
	})

	req := Request{
		Edits: []Edit{{Kind: "replace", Path: "hello.txt", OldText: "hello", NewText: "world"}},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	// Small output should not be stored.
	if store.retainCount > 0 {
		t.Error("small output should not be retained in evidence store")
	}

	// Retention status should be not_needed.
	if summary.Retention != RetentionNotNeeded {
		t.Errorf("retention = %q, want not_needed", summary.Retention)
	}
}

// TestEditAndVerify_NoEvidenceStoreAvailable tests that missing evidence
// store reports unavailable but doesn't crash.
func TestEditAndVerify_NoEvidenceStoreAvailable(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello"), 0644)

	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: failing
    argv: [sh, -c, "for i in $(seq 1 100); do echo 'ERROR line'; done; exit 1"]
    cwd: .
`), 0644)

	fa, _ := NewWorkspaceFileAccess(ws)
	svc := NewService(ws, Dependencies{
		FileAccess:    fa,
		CommandRunner: NewExactCommandRunner(ws),
		// No EvidenceStore.
	})

	req := Request{
		Edits: []Edit{{Kind: "replace", Path: "hello.txt", OldText: "hello", NewText: "world"}},
	}

	summary := svc.ApplyAndVerify(context.Background(), req, Options{})

	if summary.Retention != RetentionUnavailable {
		t.Errorf("retention = %q, want unavailable", summary.Retention)
	}
}

// --- Mock evidence store ---

type mockEvidenceStore struct {
	retainCount  int
	lastEvidence Evidence
}

func (m *mockEvidenceStore) Retain(_ context.Context, evidence Evidence) (EvidenceRef, error) {
	m.retainCount++
	m.lastEvidence = evidence
	return EvidenceRef{
		ID:       "mock-" + evidence.CheckID,
		Retained: true,
	}, nil
}
