package session_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tzro/pkg/session"
	"tzro/pkg/store"
)

func TestSessionManifest_SchemaV2_ScopeFilesFreshness(t *testing.T) {
	tempDir := t.TempDir()

	fileA := filepath.Join(tempDir, "fileA.go")
	fileB := filepath.Join(tempDir, "fileB.go")
	_ = os.WriteFile(fileA, []byte("package main\nfunc A() {}\n"), 0644)
	_ = os.WriteFile(fileB, []byte("package main\nfunc B() {}\n"), 0644)

	hashA, _ := session.StreamSHA256(fileA)
	hashB, _ := session.StreamSHA256(fileB)

	manifest := session.NewSessionManifest("sess_v2", tempDir, "main", "Implement feature X")
	manifest.SchemaVersion = 2
	manifest.ChangedFiles = []session.FileSnapshot{
		{Path: "fileA.go", Hash: hashA},
		{Path: "fileB.go", Hash: hashB},
	}

	// Check 1 only touches fileA
	manifest.Checks = []session.CheckExecution{
		{
			Command:    "go test ./fileA",
			ExitCode:   0,
			Timestamp:  time.Now().UTC(),
			ScopeFiles: []string{"fileA.go"},
		},
		{
			Command:    "go test ./fileB",
			ExitCode:   0,
			Timestamp:  time.Now().UTC(),
			ScopeFiles: []string{"fileB.go"},
		},
	}

	// 1. Initial state: everything fresh
	drifts, checkReports := manifest.ValidateCheckFreshness(tempDir)
	for _, d := range drifts {
		if d.Status != "fresh" {
			t.Errorf("expected file %s to be fresh, got %s", d.Path, d.Status)
		}
	}
	for _, cr := range checkReports {
		if !cr.Fresh {
			t.Errorf("expected check %s to be fresh initially", cr.Check.Command)
		}
	}

	// 2. Mutate fileB only. Check 1 (touching fileA) MUST remain fresh! Check 2 must become stale!
	_ = os.WriteFile(fileB, []byte("package main\nfunc B() { /* mutated */ }\n"), 0644)

	drifts, checkReports = manifest.ValidateCheckFreshness(tempDir)
	var check1Fresh, check2Fresh bool
	var check2DriftedFiles []string
	for _, cr := range checkReports {
		if cr.Check.Command == "go test ./fileA" {
			check1Fresh = cr.Fresh
		}
		if cr.Check.Command == "go test ./fileB" {
			check2Fresh = cr.Fresh
			check2DriftedFiles = cr.DriftedFiles
		}
	}

	if !check1Fresh {
		t.Errorf("expected check 1 (fileA only) to remain fresh when fileB mutated")
	}
	if check2Fresh {
		t.Errorf("expected check 2 (fileB) to be stale when fileB mutated")
	}
	if len(check2DriftedFiles) == 0 || check2DriftedFiles[0] != "fileB.go" {
		t.Errorf("expected check 2 to report fileB.go drifted, got: %v", check2DriftedFiles)
	}
}

func TestSessionManifest_TaskSelectionOrder(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspaces/my-repo"

	// 1. Clean start when nothing saved
	sess, err := session.ResolveSession(s, ws, "feature/foo", "")
	if err != nil {
		t.Fatalf("unexpected error on clean start: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session on clean start, got %+v", sess)
	}

	// Save session 1 on "main"
	m1 := session.NewSessionManifest("sess_1", ws, "main", "Obj 1")
	json1, _ := m1.ToJSON()
	_ = s.PutSession(m1.ID, ws, m1.Branch, m1.SchemaVersion, json1)

	// Save session 2 on "feature/foo"
	m2 := session.NewSessionManifest("sess_2", ws, "feature/foo", "Obj 2")
	json2, _ := m2.ToJSON()
	_ = s.PutSession(m2.ID, ws, m2.Branch, m2.SchemaVersion, json2)

	// 2. Branch-keyed lookup: should find sess_2
	resolved, err := session.ResolveSession(s, ws, "feature/foo", "")
	if err != nil || resolved == nil || resolved.ID != "sess_2" {
		t.Errorf("expected branch-keyed match to find sess_2, got %+v (err: %v)", resolved, err)
	}

	// 3. Fallback to most recent for workspace when branch doesn't match
	resolvedFallback, err := session.ResolveSession(s, ws, "non-existent-branch", "")
	if err != nil || resolvedFallback == nil || resolvedFallback.ID != "sess_2" {
		t.Errorf("expected workspace fallback to find sess_2, got %+v (err: %v)", resolvedFallback, err)
	}

	// 4. Explicit override finds specific session
	resolvedExplicit, err := session.ResolveSession(s, ws, "other-branch", "sess_1")
	if err != nil || resolvedExplicit == nil || resolvedExplicit.ID != "sess_1" {
		t.Errorf("expected explicit override to find sess_1, got %+v", resolvedExplicit)
	}
}

func TestSessionManifest_MissingArtifactsAndGracefulDegradation(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspaces/my-repo"

	// Put an artifact
	artID, err := s.PutArtifact(&store.Artifact{
		Type:      "log",
		Workspace: ws,
		Body:      "log content",
	})
	if err != nil {
		t.Fatalf("PutArtifact failed: %v", err)
	}

	manifest := session.NewSessionManifest("sess_test", ws, "main", "Testing degradation")
	manifest.ArtifactIDs = []string{artID, "art_missing_12345"}

	missing := manifest.CheckMissingArtifacts(s)
	if len(missing) != 1 || missing[0] != "art_missing_12345" {
		t.Errorf("expected art_missing_12345 to be reported missing, got: %v", missing)
	}
}
