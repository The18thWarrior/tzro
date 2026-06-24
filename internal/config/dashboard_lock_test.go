package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardLockRoundTrip(t *testing.T) {
	// Use a temp directory so tests don't pollute the real .tzro
	tmpDir := t.TempDir()
	t.Setenv("TZRO_DIR", tmpDir)

	// Ensure .tzro subdir exists for ResolvePath
	_ = os.MkdirAll(filepath.Join(tmpDir, ".tzro"), 0755)

	// Initially no lock
	lock := ReadDashboardLock()
	if lock != nil {
		t.Fatalf("expected nil lock before writing, got %+v", lock)
	}

	// Write lock with our own PID (guaranteed alive)
	pid := os.Getpid()
	port := 9999
	if err := WriteDashboardLock(pid, port); err != nil {
		t.Fatalf("WriteDashboardLock failed: %v", err)
	}

	// Read it back — should succeed since our PID is alive
	lock = ReadDashboardLock()
	if lock == nil {
		t.Fatal("expected non-nil lock after writing")
	}
	if lock.PID != pid {
		t.Errorf("expected PID %d, got %d", pid, lock.PID)
	}
	if lock.Port != port {
		t.Errorf("expected port %d, got %d", port, lock.Port)
	}

	// Remove and confirm it's gone
	RemoveDashboardLock()
	lock = ReadDashboardLock()
	if lock != nil {
		t.Fatalf("expected nil lock after removal, got %+v", lock)
	}
}

func TestDashboardLockStalePID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TZRO_DIR", tmpDir)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".tzro"), 0755)

	// Write a lock with a PID that almost certainly doesn't exist
	// (PID 2147483647 is the max on most systems and very unlikely to be in use)
	stalePID := 2147483647
	if err := WriteDashboardLock(stalePID, 8080); err != nil {
		t.Fatalf("WriteDashboardLock failed: %v", err)
	}

	// ReadDashboardLock should detect the stale PID and return nil
	lock := ReadDashboardLock()
	if lock != nil {
		t.Fatalf("expected nil for stale PID, got %+v", lock)
	}

	// Lock file should have been cleaned up
	lockFile := ResolvePath(".dashboard.lock")
	if _, err := os.Stat(lockFile); err == nil {
		t.Error("stale lock file should have been removed")
	}
}

func TestDashboardLockMalformedFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TZRO_DIR", tmpDir)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".tzro"), 0755)

	// Write garbage to the lock file
	lockFile := ResolvePath(".dashboard.lock")
	if err := os.WriteFile(lockFile, []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to write garbage lock: %v", err)
	}

	lock := ReadDashboardLock()
	if lock != nil {
		t.Fatalf("expected nil for malformed lock, got %+v", lock)
	}

	// Should have been cleaned up
	if _, err := os.Stat(lockFile); err == nil {
		t.Error("malformed lock file should have been removed")
	}
}
