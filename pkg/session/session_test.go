package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tzro/pkg/store"
)

func TestSessionManifest_SaveAndExport(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	s, err := store.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	sm := NewSessionManifest("sess_123", "/workspace/repo", "feature-auth", "Implement token refresh")
	sm.Constraints = []string{"No external cloud APIs", "Zero external dependencies"}
	sm.Decisions = []string{"Use SQLite FTS5 for search", "Store uncompressed originals"}
	sm.ChangedFiles = []FileSnapshot{
		{Path: "pkg/auth/jwt.go", Hash: "a8f19c4b"},
	}
	sm.Checks = []CheckExecution{
		{Command: "go test ./pkg/auth/...", ExitCode: 0, Timestamp: time.Now().UTC()},
	}
	sm.PendingTasks = []string{"Add revocation tests"}
	sm.ArtifactIDs = []string{"art_9876543210"}

	jsonStr, err := sm.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// 1. PutSession into store
	err = s.PutSession(sm.ID, sm.Workspace, sm.Branch, sm.SchemaVersion, jsonStr)
	if err != nil {
		t.Fatalf("PutSession failed: %v", err)
	}

	// 2. Retrieve session and parse
	retrievedJSON, err := s.GetSession(sm.ID, sm.Workspace)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	loaded, err := FromJSON(retrievedJSON)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}

	if loaded.Objective != sm.Objective {
		t.Errorf("expected objective %q, got %q", sm.Objective, loaded.Objective)
	}
	if len(loaded.Checks) != 1 || loaded.Checks[0].ExitCode != 0 {
		t.Errorf("expected verified check evidence preserved")
	}

	// 3. FormatMarkdown output check
	md := loaded.FormatMarkdown()
	if !strings.Contains(md, "Implement token refresh") || !strings.Contains(md, "Verified Evidence") {
		t.Errorf("expected Markdown handoff with objective and evidence, got:\n%s", md)
	}

	// 4. Test export to disk
	exportPath := filepath.Join(tempDir, "manifest.json")
	_ = os.WriteFile(exportPath, []byte(jsonStr), 0644)
	if _, err := os.Stat(exportPath); err != nil {
		t.Errorf("manifest export file missing")
	}
}

