//go:build !windows

package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Cancel the entire shell group so descendants cannot keep work or pipes alive.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd
}
