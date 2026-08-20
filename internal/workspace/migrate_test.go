package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacy_MovesDB(t *testing.T) {
	tmp := t.TempDir()
	content := []byte("SQLite format 3")

	// Create legacy tzro.db
	if err := os.WriteFile(filepath.Join(tmp, "tzro.db"), content, 0644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacy(tmp)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if !migrated {
		t.Error("Expected migration to be performed")
	}

	// Old file should be gone
	if _, err := os.Stat(filepath.Join(tmp, "tzro.db")); !os.IsNotExist(err) {
		t.Error("Legacy tzro.db should have been removed")
	}

	// New file should exist with same content
	newPath := filepath.Join(tmp, "workspaces", DefaultID, "tzro.db")
	got, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("New DB not found: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("Content mismatch: %q vs %q", got, content)
	}

	// workspace.json should exist
	jsonPath := filepath.Join(tmp, "workspaces", DefaultID, "workspace.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("workspace.json not created: %v", err)
	}
}

func TestMigrateLegacy_NothingToMigrate(t *testing.T) {
	tmp := t.TempDir()
	// No tzro.db at all

	migrated, err := MigrateLegacy(tmp)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if migrated {
		t.Error("Should not have performed migration")
	}
}

func TestMigrateLegacy_AlreadyMigrated(t *testing.T) {
	tmp := t.TempDir()

	// Create both legacy DB and workspaces/ directory
	_ = os.WriteFile(filepath.Join(tmp, "tzro.db"), []byte("data"), 0644)
	_ = os.MkdirAll(filepath.Join(tmp, "workspaces"), 0755)

	migrated, err := MigrateLegacy(tmp)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if migrated {
		t.Error("Should not migrate when workspaces/ already exists")
	}

	// Legacy DB should still be in place (no-op)
	if _, err := os.Stat(filepath.Join(tmp, "tzro.db")); err != nil {
		t.Error("Legacy tzro.db should not have been touched")
	}
}

func TestMigrateLegacy_PreservesWALFiles(t *testing.T) {
	tmp := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmp, "tzro.db"), []byte("main"), 0644)
	_ = os.WriteFile(filepath.Join(tmp, "tzro.db-wal"), []byte("wal"), 0644)
	_ = os.WriteFile(filepath.Join(tmp, "tzro.db-shm"), []byte("shm"), 0644)

	migrated, err := MigrateLegacy(tmp)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if !migrated {
		t.Error("Expected migration")
	}

	defaultDir := filepath.Join(tmp, "workspaces", DefaultID)

	// All three files should be moved
	for _, name := range []string{"tzro.db", "tzro.db-wal", "tzro.db-shm"} {
		if _, err := os.Stat(filepath.Join(defaultDir, name)); err != nil {
			t.Errorf("%s not found in workspace dir: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(tmp, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed from legacy location", name)
		}
	}
}
