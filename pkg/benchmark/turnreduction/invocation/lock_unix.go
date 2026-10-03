//go:build !windows

package invocation

import (
	"os"
	"syscall"
)

func lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func unlock(f *os.File)     { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
