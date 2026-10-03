//go:build windows

package turnreduction

import "os/exec"

// Native evaluation readiness rejects Windows until descendant cleanup is supported.
func ownProcessGroup(cmd *exec.Cmd) func() { return func() {} }
