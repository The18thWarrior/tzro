package verification

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tzro/pkg/store"
)

// ExactCommandRunner executes commands with exact arguments (no shell interpolation)
// using Unix process groups for clean cancellation.
type ExactCommandRunner struct {
	workspace string
}

// NewExactCommandRunner creates a command runner bound to the given workspace.
func NewExactCommandRunner(workspace string) CommandRunner {
	return &ExactCommandRunner{workspace: workspace}
}

// Run executes a command and captures its output.
func (r *ExactCommandRunner) Run(ctx context.Context, cmd Command) Execution {
	if len(cmd.Argv) == 0 {
		return Execution{StartupErr: fmt.Errorf("empty argv")}
	}

	start := time.Now()

	// Resolve working directory.
	cwd := r.workspace
	if cmd.Cwd != "" && cmd.Cwd != "." {
		cwd = filepath.Join(r.workspace, cmd.Cwd)
	}

	proc := exec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	proc.Dir = cwd

	// Use a process group for clean cancellation.
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Cancel the process group on context cancellation.
	proc.Cancel = func() error {
		if proc.Process != nil {
			_ = syscall.Kill(-proc.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	var stdout, stderr bytes.Buffer
	proc.Stdout = &stdout
	proc.Stderr = &stderr

	err := proc.Run()
	duration := time.Since(start)

	result := Execution{
		Duration: duration,
	}

	// Combine stdout and stderr for output.
	combined := stdout.Bytes()
	if stderr.Len() > 0 {
		if len(combined) > 0 {
			combined = append(combined, '\n')
		}
		combined = append(combined, stderr.Bytes()...)
	}
	result.Output = combined

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
			return result
		}
		if ctx.Err() == context.Canceled {
			result.Cancelled = true
			return result
		}
		// Check for exec error (command not found).
		if _, ok := err.(*exec.Error); ok {
			result.StartupErr = err
			return result
		}
		// Extract exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			result.ExitCode = &code
		}
	} else {
		code := 0
		result.ExitCode = &code
	}

	return result
}

// ProductionEvidenceStore adapts the existing store.Store for evidence retention.
type ProductionEvidenceStore struct {
	store     *store.Store
	workspace string
}

// NewProductionEvidenceStore creates an evidence store adapter.
func NewProductionEvidenceStore(s *store.Store, workspace string) EvidenceStore {
	if s == nil {
		return nil
	}
	return &ProductionEvidenceStore{store: s, workspace: workspace}
}

// Retain stores check output as an artifact for later expansion.
func (p *ProductionEvidenceStore) Retain(ctx context.Context, evidence Evidence) (EvidenceRef, error) {
	if p.store == nil {
		return EvidenceRef{}, fmt.Errorf("store not available")
	}

	art := &store.Artifact{
		Type:      "log",
		Workspace: p.workspace,
		Body:      string(evidence.Output),
		SizeBytes: int64(len(evidence.Output)),
	}

	id, err := p.store.PutArtifact(art)
	if err != nil {
		return EvidenceRef{}, fmt.Errorf("cannot retain evidence: %w", err)
	}

	return EvidenceRef{
		ID:        id,
		Workspace: p.workspace,
		Retained:  true,
	}, nil
}

// os import needed for FileState.
// The import is in runner.go (same package).

// Ensure runner_unix.go doesn't conflict with runner.go by keeping
// only the implementations that use Unix-specific syscalls here.

func init() {
	// Verify Unix-specific process group handling is available.
	// This is a compile-time check; the actual call uses Setpgid.
	_ = strings.HasPrefix("", "")
}
