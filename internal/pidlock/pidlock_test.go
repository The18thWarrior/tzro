package pidlock_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tzro/internal/pidlock"
)

func TestAcquire_FreshPath_SucceedsAndWritesPID(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	unlock, err := pidlock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("Acquire on fresh path should succeed, got: %v", err)
	}
	defer unlock()

	// Lockfile should exist and contain our PID
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("lockfile should be readable: %v", err)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("lockfile should contain a numeric PID, got %q: %v", pidStr, err)
	}

	if pid != os.Getpid() {
		t.Errorf("lockfile PID = %d, want %d", pid, os.Getpid())
	}
}

func TestAcquire_AlreadyHeld_ReturnsErrAlreadyRunning(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	unlock1, err := pidlock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("first Acquire should succeed: %v", err)
	}
	defer unlock1()

	// Second acquire on the same path should fail
	_, err = pidlock.Acquire(lockPath)
	if err == nil {
		t.Fatal("second Acquire should fail, got nil error")
	}

	var alreadyRunning *pidlock.ErrAlreadyRunning
	if !errors.As(err, &alreadyRunning) {
		t.Fatalf("error should be *ErrAlreadyRunning, got %T: %v", err, err)
	}

	if alreadyRunning.HolderPID != os.Getpid() {
		t.Errorf("HolderPID = %d, want %d", alreadyRunning.HolderPID, os.Getpid())
	}
}

func TestAcquire_StaleLockfile_ReclaimsLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	// Create a lockfile with a PID that doesn't exist.
	// Use a very high PID that is almost certainly not in use.
	deadPID := 99999999
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(deadPID)+"\n"), 0644); err != nil {
		t.Fatalf("failed to create stale lockfile: %v", err)
	}

	// Acquire should succeed because the flock is not held (no process has it)
	unlock, err := pidlock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("Acquire should reclaim stale lockfile, got: %v", err)
	}
	defer unlock()

	// Lockfile should now contain our PID
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("lockfile should be readable: %v", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("lockfile should contain a numeric PID, got %q: %v", pidStr, err)
	}
	if pid != os.Getpid() {
		t.Errorf("lockfile PID = %d, want %d (should be overwritten)", pid, os.Getpid())
	}
}

func TestUnlock_CalledTwice_NoPanic(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	unlock, err := pidlock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("Acquire should succeed: %v", err)
	}

	// First unlock — should clean up
	unlock()

	// Lockfile should be removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lockfile should be removed after unlock, stat err = %v", err)
	}

	// Second unlock — should not panic
	unlock()
}

func TestIsHeld_WhenHeld_ReturnsPIDAndTrue(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	unlock, err := pidlock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("Acquire should succeed: %v", err)
	}
	defer unlock()

	pid, held := pidlock.IsHeld(lockPath)
	if !held {
		t.Fatal("IsHeld should return true when lock is held")
	}
	if pid != os.Getpid() {
		t.Errorf("IsHeld PID = %d, want %d", pid, os.Getpid())
	}
}

func TestIsHeld_WhenNotHeld_ReturnsZeroAndFalse(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	// No lock acquired — file doesn't exist
	pid, held := pidlock.IsHeld(lockPath)
	if held {
		t.Error("IsHeld should return false when no lock exists")
	}
	if pid != 0 {
		t.Errorf("IsHeld PID = %d, want 0", pid)
	}
}

func TestIsHeld_AfterUnlock_ReturnsZeroAndFalse(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "mcp.lock")

	unlock, err := pidlock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("Acquire should succeed: %v", err)
	}

	// Release the lock
	unlock()

	pid, held := pidlock.IsHeld(lockPath)
	if held {
		t.Error("IsHeld should return false after unlock")
	}
	if pid != 0 {
		t.Errorf("IsHeld PID = %d, want 0", pid)
	}
}
