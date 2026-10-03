package executor

import (
	"context"
	"os/exec"
	"time"
)

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.WaitDelay = time.Second
	return cmd
}
