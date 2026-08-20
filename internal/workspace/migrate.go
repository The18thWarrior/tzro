package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// MigrateLegacy checks if a legacy tzro.db exists at the TZRO_DIR root
// and moves it into workspaces/default/ if the workspaces/ directory doesn't exist yet.
// Returns true if migration was performed, false if no migration was needed.
func MigrateLegacy(tzroDir string) (bool, error) {
	legacyDB := filepath.Join(tzroDir, "tzro.db")
	workspacesDir := filepath.Join(tzroDir, "workspaces")

	// Check if legacy DB exists
	if _, err := os.Stat(legacyDB); os.IsNotExist(err) {
		return false, nil
	}

	// Check if workspaces/ already exists (already migrated or fresh install)
	if _, err := os.Stat(workspacesDir); err == nil {
		return false, nil
	}

	// Perform migration: create default workspace directory
	defaultDir := filepath.Join(workspacesDir, DefaultID)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		return false, fmt.Errorf("create default workspace dir: %w", err)
	}

	// Move DB and associated WAL/SHM files
	dbFiles := []string{"tzro.db", "tzro.db-wal", "tzro.db-shm"}
	for _, name := range dbFiles {
		src := filepath.Join(tzroDir, name)
		dst := filepath.Join(defaultDir, name)

		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue // WAL/SHM files may not exist
		}

		if err := os.Rename(src, dst); err != nil {
			return false, fmt.Errorf("move %s: %w", name, err)
		}
	}

	// Create workspace.json for the default workspace
	reg := NewRegistry(workspacesDir)
	if _, err := reg.Register(DefaultID, ""); err != nil {
		return true, fmt.Errorf("create workspace.json: %w", err)
	}

	return true, nil
}
