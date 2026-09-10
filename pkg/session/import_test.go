package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tzro/pkg/session"
	"tzro/pkg/store"
)

func TestSessionManifest_ImportSecurity(t *testing.T) {
	manifest := session.NewSessionManifest("sess_123", "ws-alpha", "main", "refactor engine")
	data, err := manifest.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// 1. Valid import into same workspace
	imported, err := session.ImportSession(data, "ws-alpha")
	if err != nil {
		t.Fatalf("Expected valid import, got err: %v", err)
	}
	if imported.ID != "sess_123" {
		t.Errorf("Expected sess_123, got %s", imported.ID)
	}

	// 2. Reject cross-workspace import
	_, err = session.ImportSession(data, "ws-beta")
	if err == nil {
		t.Fatalf("Expected cross-workspace rejection, got nil")
	}

	// 3. Reject incompatible schema version
	incompatibleJSON := `{"schema_version": 99, "id": "sess_future"}`
	_, err = session.ImportSession(incompatibleJSON, "ws-alpha")
	if err == nil {
		t.Fatalf("Expected schema version rejection, got nil")
	}

	// 4. Reject invalid JSON
	_, err = session.ImportSession("{bad-json}", "ws-alpha")
	if err == nil {
		t.Fatalf("Expected invalid JSON error, got nil")
	}
}

func TestSessionManifest_ValidateFreshness(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := "pkg/util.go"
	file2 := "pkg/missing.go"
	file3 := "pkg/modified.go"

	os.MkdirAll(filepath.Join(tmpDir, "pkg"), 0755)
	content1 := "package util\nfunc Hello() string { return \"hi\" }\n"
	os.WriteFile(filepath.Join(tmpDir, file1), []byte(content1), 0644)
	hash1 := store.ComputeHash(content1)

	content3Old := "package util\nfunc Foo() {}\n"
	hash3Old := store.ComputeHash(content3Old)
	content3New := "package util\nfunc Foo() { println(\"changed\") }\n"
	os.WriteFile(filepath.Join(tmpDir, file3), []byte(content3New), 0644)

	manifest := session.NewSessionManifest("sess_fresh", "ws-test", "main", "test freshness")
	manifest.ChangedFiles = []session.FileSnapshot{
		{Path: file1, Hash: hash1},
		{Path: file2, Hash: "nonexistenthash"},
		{Path: file3, Hash: hash3Old},
	}

	drifts := manifest.ValidateFreshness(tmpDir)
	if len(drifts) != 3 {
		t.Fatalf("Expected 3 drift records, got %d", len(drifts))
	}

	driftMap := make(map[string]session.FileDrift)
	for _, d := range drifts {
		driftMap[d.Path] = d
	}

	if driftMap[file1].Status != "fresh" {
		t.Errorf("Expected file1 to be fresh, got %s", driftMap[file1].Status)
	}
	if driftMap[file2].Status != "missing" {
		t.Errorf("Expected file2 to be missing, got %s", driftMap[file2].Status)
	}
	if driftMap[file3].Status != "modified" {
		t.Errorf("Expected file3 to be modified, got %s", driftMap[file3].Status)
	}
}

func TestSessionManifest_ValidateFreshness_FullSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "pkg"), 0755)

	// File with a full 64-char SHA-256 hash (new format)
	content := "package main\nfunc main() {}\n"
	filePath := "pkg/main.go"
	os.WriteFile(filepath.Join(tmpDir, filePath), []byte(content), 0644)
	fullHash := store.SHA256Full(content)

	// File with legacy 8-char prefix hash
	contentLegacy := "package legacy\n"
	legacyPath := "pkg/legacy.go"
	os.WriteFile(filepath.Join(tmpDir, legacyPath), []byte(contentLegacy), 0644)
	prefixHash := store.ComputeHash(contentLegacy)

	manifest := session.NewSessionManifest("sess_hash_compat", "ws-test", "main", "hash compat")
	manifest.ChangedFiles = []session.FileSnapshot{
		{Path: filePath, Hash: fullHash},
		{Path: legacyPath, Hash: prefixHash},
	}

	drifts := manifest.ValidateFreshness(tmpDir)
	driftMap := make(map[string]session.FileDrift)
	for _, d := range drifts {
		driftMap[d.Path] = d
	}

	// Full 64-char hash should match as fresh
	if driftMap[filePath].Status != "fresh" {
		t.Errorf("Expected full SHA-256 hash to match as fresh, got %s (expected=%s, actual=%s)",
			driftMap[filePath].Status, fullHash, driftMap[filePath].ActualHash)
	}

	// Legacy 8-char prefix should still match as fresh
	if driftMap[legacyPath].Status != "fresh" {
		t.Errorf("Expected legacy 8-char hash to match as fresh, got %s", driftMap[legacyPath].Status)
	}
}

func TestSessionManifest_PauseEditResume(t *testing.T) {
	// End-to-end pause-edit-resume test validating that prior decisions and verified checks persist accurately.
	manifest := session.NewSessionManifest("sess_pause_resume", "ws-audit", "feat/caching", "Implement prefix lock")
	manifest.Constraints = []string{"Memory budget <50MB", "Zero external dependencies"}
	manifest.Decisions = []string{"Use SQLite WAL for persistent indices", "Use FTS5 with BM25"}
	manifest.Checks = []session.CheckExecution{
		{
			Command:   "go test ./pkg/kvlock/...",
			ExitCode:  0,
			Timestamp: time.Now().UTC().Add(-10 * time.Minute),
			OutputID:  "art_check_001",
		},
	}
	manifest.PendingTasks = []string{"Complete fixture tests", "Run holdout benchmark"}

	jsonStr, err := manifest.ToJSON()
	if err != nil {
		t.Fatalf("Failed to serialize manifest: %v", err)
	}

	resumed, err := session.ImportSession(jsonStr, "ws-audit")
	if err != nil {
		t.Fatalf("Failed to resume session: %v", err)
	}

	if len(resumed.Decisions) != 2 || resumed.Decisions[0] != "Use SQLite WAL for persistent indices" {
		t.Errorf("Decisions not preserved accurately: %+v", resumed.Decisions)
	}
	if len(resumed.Checks) != 1 || resumed.Checks[0].Command != "go test ./pkg/kvlock/..." || resumed.Checks[0].OutputID != "art_check_001" {
		t.Errorf("Checks not preserved accurately: %+v", resumed.Checks)
	}
	if len(resumed.PendingTasks) != 2 {
		t.Errorf("Pending tasks count mismatch: %+v", resumed.PendingTasks)
	}

	md := resumed.FormatMarkdown()
	if md == "" || !strings.Contains(md, "Architectural Decisions") || !strings.Contains(md, "Verified Evidence") {
		t.Errorf("Markdown formatting missing key sections: %s", md)
	}
}
