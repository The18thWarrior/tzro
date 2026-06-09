package pidlock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// ErrAlreadyRunning is returned when another live process holds the lock.
// The HolderPID field contains the PID of the process holding the lock.
type ErrAlreadyRunning struct {
	HolderPID int
}

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("another instance is already running (PID %d)", e.HolderPID)
}

// Acquire attempts to take an exclusive flock on the lockfile at the given path.
// On success, returns an unlock function that releases the lock and removes the file.
// On failure (lock held by a live process), returns ErrAlreadyRunning.
func Acquire(lockfilePath string) (unlock func(), err error) {
	// Open or create the lockfile
	f, err := os.OpenFile(lockfilePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("pidlock: open lockfile: %w", err)
	}

	// Try non-blocking exclusive flock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Lock is held by another process — read the PID for diagnostics
		if errors.Is(err, syscall.EWOULDBLOCK) {
			holderPID := readPIDFromFile(f)
			f.Close()
			return nil, &ErrAlreadyRunning{HolderPID: holderPID}
		}
		f.Close()
		return nil, fmt.Errorf("pidlock: flock: %w", err)
	}

	// We hold the lock — write our PID
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Sync()

	// Build the unlock function
	unlocked := false
	unlock = func() {
		if unlocked {
			return
		}
		unlocked = true
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		_ = os.Remove(lockfilePath)
	}

	return unlock, nil
}

// readPIDFromFile reads a PID integer from an open file. Returns 0 on failure.
func readPIDFromFile(f *os.File) int {
	_, _ = f.Seek(0, 0)
	buf := make([]byte, 32)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return 0
	}
	pid, err := strconv.Atoi(trimNewline(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}

// trimNewline removes trailing newlines and spaces.
func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// IsHeld checks whether the lockfile exists and is held by a live process.
// Returns the holder PID and true if held, or 0 and false otherwise.
func IsHeld(lockfilePath string) (pid int, held bool) {
	f, err := os.OpenFile(lockfilePath, os.O_RDONLY, 0)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	// Probe with non-blocking exclusive flock — EWOULDBLOCK means another
	// process holds the lock.
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Lock is held by another process
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return readPIDFromFile(f), true
		}
		return 0, false
	}

	// We got the lock — nobody held it. Release immediately.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return 0, false
}
