package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tzro/pkg/session"
	"tzro/pkg/store"
)

// Helper to run git in a test directory
func gitCmd(t *testing.T, dir string, args ...string) string {
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
		t.Fatalf("git %v failed in %s: %v\nOutput: %s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// 1. Two pauses within one clock tick cannot collide or overwrite an unrelated task.
func TestIssue12_CollisionResistantSessionIDs(t *testing.T) {
	const count = 1000
	idMap := make(map[string]bool)
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			id := session.GenerateSessionID()
			mu.Lock()
			defer mu.Unlock()
			if idMap[id] {
				t.Errorf("collision detected for session ID: %s", id)
			}
			idMap[id] = true
		}()
	}
	wg.Wait()

	if len(idMap) != count {
		t.Errorf("expected %d unique IDs, got %d", count, len(idMap))
	}
}

// 2. The most recently paused task wins even when it was created before another task.
func TestIssue12_MostRecentlyPausedTaskWins(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspace/project"
	branch := "main"

	// Task 1: created first (10:00)
	t1Created := time.Now().Add(-2 * time.Hour).UTC()
	sm1 := session.NewSessionManifestV3("task_1", ws, branch, "First Task Objective")
	sm1.CreatedAt = t1Created

	// Task 2: created second (10:30)
	t2Created := time.Now().Add(-1 * time.Hour).UTC()
	sm2 := session.NewSessionManifestV3("task_2", ws, branch, "Second Task Objective")
	sm2.CreatedAt = t2Created
	t2Paused := time.Now().Add(-50 * time.Minute).UTC()
	sm2.PausedAt = &t2Paused

	// Save task 1 and task 2
	json1, _ := sm1.ToJSON()
	_ = s.PutSession(sm1.ID, ws, sm1.Branch, sm1.SchemaVersion, json1)
	json2, _ := sm2.ToJSON()
	_ = s.PutSession(sm2.ID, ws, sm2.Branch, sm2.SchemaVersion, json2)

	// Now pause Task 1 LATER (at 11:00, most recent pause!)
	t1Paused := time.Now().Add(-10 * time.Minute).UTC()
	sm1.PausedAt = &t1Paused
	json1Updated, _ := sm1.ToJSON()
	_ = s.PutSession(sm1.ID, ws, sm1.Branch, sm1.SchemaVersion, json1Updated)

	// Query latest paused session: sm1 must win because it was paused most recently!
	latestJSON, err := s.GetLatestPausedSession(ws, branch)
	if err != nil {
		t.Fatalf("GetLatestPausedSession error: %v", err)
	}
	latest, err := session.FromJSON(latestJSON)
	if err != nil {
		t.Fatalf("FromJSON error: %v", err)
	}

	if latest.ID != "task_1" {
		t.Errorf("expected most recently paused task_1 to win, got %s", latest.ID)
	}
}

// 3. Another branch, worktree, or repository cannot become the implicit resume target.
func TestIssue12_BranchAndWorkspaceIsolation(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	wsA := "/workspaces/repo-a"
	wsB := "/workspaces/repo-b"

	// Save session on branch "feature-1" in wsA
	now := time.Now().UTC()
	smA1 := session.NewSessionManifestV3("sess_a1", wsA, "feature-1", "Obj A1")
	smA1.PausedAt = &now
	jA1, _ := smA1.ToJSON()
	_ = s.PutSession(smA1.ID, wsA, smA1.Branch, smA1.SchemaVersion, jA1)

	// Save session on branch "feature-2" in wsA
	smA2 := session.NewSessionManifestV3("sess_a2", wsA, "feature-2", "Obj A2")
	smA2.PausedAt = &now
	jA2, _ := smA2.ToJSON()
	_ = s.PutSession(smA2.ID, wsA, smA2.Branch, smA2.SchemaVersion, jA2)

	// Save session on branch "feature-1" in wsB
	smB1 := session.NewSessionManifestV3("sess_b1", wsB, "feature-1", "Obj B1")
	smB1.PausedAt = &now
	jB1, _ := smB1.ToJSON()
	_ = s.PutSession(smB1.ID, wsB, smB1.Branch, smB1.SchemaVersion, jB1)

	// In wsA on branch "feature-3": no session should be returned (no fallback to other branches!)
	_, err = s.GetLatestPausedSession(wsA, "feature-3")
	if err == nil {
		t.Errorf("expected error for non-existent branch, got nil")
	}

	// In wsA on branch "feature-1": only sess_a1 must be returned, never sess_b1
	resJSON, err := s.GetLatestPausedSession(wsA, "feature-1")
	if err != nil {
		t.Fatalf("GetLatestPausedSession failed: %v", err)
	}
	res, _ := session.FromJSON(resJSON)
	if res.ID != "sess_a1" {
		t.Errorf("expected sess_a1, got %s", res.ID)
	}
}

// 4. Explicit selection reports branch mismatch without switching branches or applying saved state.
func TestIssue12_ExplicitSelectionBranchMismatch(t *testing.T) {
	tempDir := t.TempDir()
	gitCmd(t, tempDir, "init")
	gitCmd(t, tempDir, "checkout", "-b", "current-branch")
	_ = os.WriteFile(filepath.Join(tempDir, "file.txt"), []byte("initial\n"), 0644)
	gitCmd(t, tempDir, "add", ".")
	gitCmd(t, tempDir, "commit", "-m", "init")

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Session saved on "saved-branch"
	sm := session.NewSessionManifestV3("sess_saved", tempDir, "saved-branch", "Feature on another branch")
	now := time.Now().Add(-10 * time.Minute).UTC()
	sm.PausedAt = &now

	gitState := session.SenseGitState(context.Background(), tempDir)
	if gitState.Branch != "current-branch" {
		t.Fatalf("expected gitState.Branch current-branch, got %s", gitState.Branch)
	}

	report := session.GenerateResumeReport(context.Background(), sm, tempDir, gitState, s, nil)

	if !report.BranchMismatch {
		t.Errorf("expected BranchMismatch to be true")
	}
	if report.ExpectedBranch != "saved-branch" {
		t.Errorf("expected ExpectedBranch saved-branch, got %s", report.ExpectedBranch)
	}
	if report.CurrentBranch != "current-branch" {
		t.Errorf("expected CurrentBranch current-branch, got %s", report.CurrentBranch)
	}

	// Verify developer files remain unmodified
	content, _ := os.ReadFile(filepath.Join(tempDir, "file.txt"))
	if string(content) != "initial\n" {
		t.Errorf("file was unexpectedly modified: %s", string(content))
	}
	// Verify git branch was NOT changed
	currBranch := gitCmd(t, tempDir, "branch", "--show-current")
	if currBranch != "current-branch" {
		t.Errorf("git branch was unexpectedly switched to: %s", currBranch)
	}
}

// 5. A test followed by an edit and then pause appears stale or unknown, never fresh from the pause hash.
func TestIssue12_TestFollowedByEditAppearsStale(t *testing.T) {
	tempDir := t.TempDir()

	fileA := filepath.Join(tempDir, "fileA.go")
	_ = os.WriteFile(fileA, []byte("package main\n// version 1\n"), 0644)
	h1 := sha256.Sum256([]byte("package main\n// version 1\n"))
	execHash := hex.EncodeToString(h1[:])

	// 1. Check executed against version 1
	check := session.CheckExecution{
		Command:     "go test ./...",
		ExitCode:    0,
		Timestamp:   time.Now().Add(-30 * time.Minute).UTC(),
		ScopeFiles:  []string{"fileA.go"},
		ScopeHashes: map[string]string{"fileA.go": execHash},
	}

	// 2. File edited to version 2
	_ = os.WriteFile(fileA, []byte("package main\n// version 2 edited\n"), 0644)
	h2 := sha256.Sum256([]byte("package main\n// version 2 edited\n"))
	pauseHash := hex.EncodeToString(h2[:])

	// 3. Pause occurs capturing pauseHash
	sm := session.NewSessionManifestV3("sess_test_drift", tempDir, "main", "Check drift after edit")
	sm.Checks = []session.CheckExecution{check}
	sm.ChangedFiles = []session.FileSnapshot{
		{Path: "fileA.go", Hash: pauseHash, WorktreeHash: pauseHash, Status: "modified"},
	}

	drifts, checkReports := sm.ValidateCheckFreshness(tempDir)
	_ = drifts

	if len(checkReports) != 1 {
		t.Fatalf("expected 1 check report, got %d", len(checkReports))
	}
	cr := checkReports[0]
	if cr.Fresh {
		t.Errorf("expected check to be STALE, but got Fresh=true")
	}
	if cr.Status != session.FreshnessStale {
		t.Errorf("expected check status to be %q, got %q", session.FreshnessStale, cr.Status)
	}

	// Now test check WITHOUT execution-time scope hashes: must be UNKNOWN, never fresh
	legacyCheck := session.CheckExecution{
		Command:    "go test ./legacy",
		ExitCode:   0,
		Timestamp:  time.Now().UTC(),
		ScopeFiles: []string{"fileA.go"}, // no ScopeHashes!
	}
	sm.Checks = []session.CheckExecution{legacyCheck}
	_, legacyReports := sm.ValidateCheckFreshness(tempDir)
	if legacyReports[0].Status != session.FreshnessUnknown {
		t.Errorf("expected legacy check without execution hashes to be %q, got %q", session.FreshnessUnknown, legacyReports[0].Status)
	}
}

// 6. Mixed staged/unstaged, expected deletions, untracked, renames, and path boundary validation.
func TestIssue12_SenseChangesAndExpectedDeletions(t *testing.T) {
	tempDir := t.TempDir()
	gitCmd(t, tempDir, "init")
	gitCmd(t, tempDir, "checkout", "-b", "main")

	// Commit initial files
	_ = os.WriteFile(filepath.Join(tempDir, "staged_and_unstaged.txt"), []byte("v1\n"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "to_delete.txt"), []byte("will be deleted\n"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "to_rename.txt"), []byte("rename content\n"), 0644)
	gitCmd(t, tempDir, "add", ".")
	gitCmd(t, tempDir, "commit", "-m", "init")

	// 1. Staged and unstaged edits
	_ = os.WriteFile(filepath.Join(tempDir, "staged_and_unstaged.txt"), []byte("v2 staged\n"), 0644)
	gitCmd(t, tempDir, "add", "staged_and_unstaged.txt")
	_ = os.WriteFile(filepath.Join(tempDir, "staged_and_unstaged.txt"), []byte("v3 unstaged worktree\n"), 0644)

	// 2. Expected deletion
	_ = os.Remove(filepath.Join(tempDir, "to_delete.txt"))

	// 3. Rename
	gitCmd(t, tempDir, "mv", "to_rename.txt", "renamed.txt")

	// 4. Untracked file
	_ = os.WriteFile(filepath.Join(tempDir, "untracked.txt"), []byte("brand new untracked\n"), 0644)

	changes, err := session.SenseChanges(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("SenseChanges failed: %v", err)
	}

	changeMap := make(map[string]session.FileSnapshot)
	for _, c := range changes {
		changeMap[c.Path] = c
	}

	// Validate staged and unstaged
	su, ok := changeMap["staged_and_unstaged.txt"]
	if !ok {
		t.Fatalf("staged_and_unstaged.txt missing from SenseChanges")
	}
	if su.StagedStatus != "M" || su.WorktreeStatus != "M" {
		t.Errorf("expected MM for staged and unstaged, got Staged=%s Worktree=%s", su.StagedStatus, su.WorktreeStatus)
	}
	if su.StagedHash == "" || su.WorktreeHash == "" || su.StagedHash == su.WorktreeHash {
		t.Errorf("expected distinct non-empty staged (%s) and worktree (%s) hashes", su.StagedHash, su.WorktreeHash)
	}

	// Validate expected deletion
	del, ok := changeMap["to_delete.txt"]
	if !ok {
		t.Fatalf("to_delete.txt missing from SenseChanges")
	}
	if del.Status != "deleted" || del.WorktreeStatus != "D" {
		t.Errorf("expected status deleted and WorktreeStatus D, got %s / %s", del.Status, del.WorktreeStatus)
	}

	// Validate rename
	ren, ok := changeMap["renamed.txt"]
	if !ok {
		t.Fatalf("renamed.txt missing from SenseChanges")
	}
	if ren.Status != "renamed" || ren.OldPath != "to_rename.txt" {
		t.Errorf("expected renamed status with OldPath to_rename.txt, got %+v", ren)
	}

	// Validate untracked
	unt, ok := changeMap["untracked.txt"]
	if !ok {
		t.Fatalf("untracked.txt missing from SenseChanges")
	}
	if unt.Status != "untracked" {
		t.Errorf("expected untracked status, got %s", unt.Status)
	}

	// Freshness check: expected deletion should be FRESH, not missing!
	sm := session.NewSessionManifestV3("sess_changes", tempDir, "main", "Test changes")
	sm.ChangedFiles = changes
	drifts := sm.ValidateFreshness(tempDir)
	for _, d := range drifts {
		if d.Path == "to_delete.txt" {
			if d.Status != "fresh" {
				t.Errorf("expected deleted file to be fresh on disk (not missing), got %s", d.Status)
			}
		}
	}
}

// 7. Legacy v1/v2 records, missing artifacts, future schemas, and path boundary rejection.
func TestIssue12_SchemaMigrationAndSecurity(t *testing.T) {
	ws := "/workspace/test"

	// v1 / v2 manifest deserialization
	v1JSON := `{"schema_version": 1, "id": "v1_sess", "workspace": "/workspace/test", "branch": "main", "created_at": "2026-09-01T00:00:00Z", "objective": "v1 obj", "changed_files": []}`
	sm1, err := session.FromJSON(v1JSON)
	if err != nil {
		t.Fatalf("FromJSON for v1 failed: %v", err)
	}
	if sm1.SchemaVersion != 1 {
		t.Errorf("expected schema version 1, got %d", sm1.SchemaVersion)
	}

	// v2 manifest
	v2JSON := `{"schema_version": 2, "id": "v2_sess", "workspace": "/workspace/test", "branch": "main", "created_at": "2026-09-01T00:00:00Z", "objective": "v2 obj", "changed_files": []}`
	sm2, err := session.FromJSON(v2JSON)
	if err != nil {
		t.Fatalf("FromJSON for v2 failed: %v", err)
	}
	if sm2.SchemaVersion != 2 {
		t.Errorf("expected schema version 2, got %d", sm2.SchemaVersion)
	}

	// Future schema version 4 must be rejected
	v4JSON := `{"schema_version": 4, "id": "v4_sess", "workspace": "/workspace/test", "branch": "main", "created_at": "2026-09-01T00:00:00Z", "objective": "v4 obj"}`
	_, err = session.FromJSON(v4JSON)
	if err == nil {
		t.Errorf("expected error for future schema version 4, got nil")
	}

	// Path escape rejection in ImportSession
	escapeJSON := `{"schema_version": 3, "id": "escape_sess", "workspace": "/workspace/test", "branch": "main", "created_at": "2026-09-01T00:00:00Z", "objective": "escape", "changed_files": [{"path": "../../etc/passwd", "hash": "abc"}]}`
	_, err = session.ImportSession(escapeJSON, ws)
	if err == nil {
		t.Errorf("expected error for path traversal escape, got nil")
	}

	// Cross-workspace rejection
	diffWSJSON := `{"schema_version": 3, "id": "ws_sess", "workspace": "/other/workspace", "branch": "main", "created_at": "2026-09-01T00:00:00Z", "objective": "diff ws", "changed_files": []}`
	_, err = session.ImportSession(diffWSJSON, ws)
	if err == nil {
		t.Errorf("expected cross-workspace error, got nil")
	}
}

// 8. Hash or hydration failure preserves the available summary with an explicit limitation.
func TestIssue12_HydrationFailurePreservesSavedIntent(t *testing.T) {
	ws := "/workspace/test"
	sm := session.NewSessionManifestV3("sess_limitation", ws, "main", "Explore complicated refactor")
	sm.Decisions = []string{"Use SQLite FTS5 for search"}
	sm.Constraints = []string{"Offline only"}
	sm.PendingTasks = []string{"Run benchmarks"}

	gitState := session.GitState{IsGit: false, Branch: "(non-git)"}

	failingHydrator := func(ctx context.Context, wsRoot, query string, budget int) (string, error) {
		return "", os.ErrNotExist
	}

	report := session.GenerateResumeReport(context.Background(), sm, ws, gitState, nil, failingHydrator)

	if report.HydrationError == "" {
		t.Errorf("expected HydrationError to be recorded")
	}
	// Saved intent is fully intact
	if report.Session.Objective != "Explore complicated refactor" {
		t.Errorf("objective corrupted: %s", report.Session.Objective)
	}
	if len(report.Session.Decisions) != 1 || report.Session.Decisions[0] != "Use SQLite FTS5 for search" {
		t.Errorf("decisions corrupted: %v", report.Session.Decisions)
	}
	if len(report.Session.Constraints) != 1 || report.Session.Constraints[0] != "Offline only" {
		t.Errorf("constraints corrupted: %v", report.Session.Constraints)
	}
	if len(report.Session.PendingTasks) != 1 || report.Session.PendingTasks[0] != "Run benchmarks" {
		t.Errorf("pending tasks corrupted: %v", report.Session.PendingTasks)
	}

	formatted := report.Format()
	if !strings.Contains(formatted, "Use SQLite FTS5 for search") {
		t.Errorf("formatted report did not contain saved decisions: %s", formatted)
	}
	if !strings.Contains(formatted, "Context hydration unavailable") {
		t.Errorf("formatted report did not state hydration limitation: %s", formatted)
	}
}

// 9. Existing session commands remain compatible and do not silently drop v3 fields during round-trips.
func TestIssue12_RoundTripPreservesSchemaV3Fields(t *testing.T) {
	now := time.Now().UTC()
	exitStatus := 0
	sm := session.NewSessionManifestV3("sess_v3_roundtrip", "/workspace/repo", "main", "Roundtrip verification")
	sm.PausedAt = &now
	sm.Worktree = "/workspace/repo/worktree1"
	sm.IsDetached = false
	sm.HeadCommit = "commit123456"
	sm.ActiveSymbols = []session.ActiveSymbol{
		{Name: "CalculateImpact", Kind: "function", FilePath: "impact.go", Line: 42, Precision: "precise"},
	}
	sm.RecentCommands = []session.CommandEvent{
		{ID: "cmd_1", DisplayText: "go test ./...", Cwd: "/workspace/repo", Workspace: "/workspace/repo", SessionID: sm.ID, StartedAt: now, ExitStatus: &exitStatus},
	}
	sm.Checks = []session.CheckExecution{
		{Command: "go test", ExitCode: 0, Timestamp: now, ScopeHashes: map[string]string{"foo.go": "abc"}},
	}
	sm.ChangedFiles = []session.FileSnapshot{
		{Path: "foo.go", StagedHash: "s1", WorktreeHash: "w1", StagedStatus: "M", WorktreeStatus: "M", Status: "modified"},
	}

	jsonStr, err := sm.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// Round-trip deserialize
	loaded, err := session.FromJSON(jsonStr)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}

	if loaded.PausedAt == nil || loaded.PausedAt.Unix() != sm.PausedAt.Unix() {
		t.Errorf("PausedAt not preserved: got %v, want %v", loaded.PausedAt, sm.PausedAt)
	}
	if loaded.Worktree != sm.Worktree {
		t.Errorf("Worktree not preserved: %s", loaded.Worktree)
	}
	if loaded.HeadCommit != sm.HeadCommit {
		t.Errorf("HeadCommit not preserved: %s", loaded.HeadCommit)
	}
	if len(loaded.ActiveSymbols) != 1 || loaded.ActiveSymbols[0].Name != "CalculateImpact" {
		t.Errorf("ActiveSymbols not preserved: %+v", loaded.ActiveSymbols)
	}
	if len(loaded.RecentCommands) != 1 || loaded.RecentCommands[0].DisplayText != "go test ./..." {
		t.Errorf("RecentCommands not preserved: %+v", loaded.RecentCommands)
	}
	if len(loaded.Checks) != 1 || loaded.Checks[0].ScopeHashes["foo.go"] != "abc" {
		t.Errorf("Check ScopeHashes not preserved: %+v", loaded.Checks)
	}
	if len(loaded.ChangedFiles) != 1 || loaded.ChangedFiles[0].StagedHash != "s1" || loaded.ChangedFiles[0].WorktreeHash != "w1" {
		t.Errorf("ChangedFiles extended fields not preserved: %+v", loaded.ChangedFiles)
	}
}

// 10. Re-resolve active symbols on resume: intact, moved, or missing.
func TestIssue12_ReResolveActiveSymbols(t *testing.T) {
	tempDir := t.TempDir()

	file := filepath.Join(tempDir, "calc.go")
	content := "package main\n\n// Line 3 comment\nfunc CalculateTotal() int {\n    return 42\n}\n"
	_ = os.WriteFile(file, []byte(content), 0644)

	sm := session.NewSessionManifestV3("sess_sym", tempDir, "main", "Symbol check")
	sm.ActiveSymbols = []session.ActiveSymbol{
		// Intact: CalculateTotal is on line 4
		{Name: "CalculateTotal", FilePath: "calc.go", Line: 4},
		// Moved: was line 1, but now at line 4
		{Name: "CalculateTotal", FilePath: "calc.go", Line: 1},
		// Missing: DoesNotExistFunc
		{Name: "DoesNotExistFunc", FilePath: "calc.go", Line: 10},
		// Missing file
		{Name: "SomeFunc", FilePath: "missing_file.go", Line: 5},
	}

	drifts := sm.ReResolveSymbols(tempDir)
	if len(drifts) != 4 {
		t.Fatalf("expected 4 symbol drifts, got %d", len(drifts))
	}

	if drifts[0].Status != "intact" || drifts[0].CurrentLine != 4 {
		t.Errorf("drifts[0] expected intact at line 4, got %+v", drifts[0])
	}
	if drifts[1].Status != "moved" || drifts[1].CurrentLine != 4 {
		t.Errorf("drifts[1] expected moved to line 4, got %+v", drifts[1])
	}
	if drifts[2].Status != "missing" {
		t.Errorf("drifts[2] expected missing, got %+v", drifts[2])
	}
	if drifts[3].Status != "missing" {
		t.Errorf("drifts[3] expected missing for nonexistent file, got %+v", drifts[3])
	}
}
