package workflow

import (
	"bytes"
	"context"
	"os/exec"
	"syscall"
	"time"
)

// Retain diagnostics without letting an unsuccessful child fill runner memory.
type outputBuffer struct{ buffer bytes.Buffer }

func (b *outputBuffer) String() string { return b.buffer.String() }

func (b *outputBuffer) Write(data []byte) (int, error) {
	n := len(data)
	remaining := 32*1024 - b.buffer.Len()
	if remaining > n {
		remaining = n
	}
	if remaining > 0 {
		_, _ = b.buffer.Write(data[:remaining])
	}
	return n, nil
}

func command(ctx context.Context, env []string, dir, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env, cmd.Dir = env, dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}
