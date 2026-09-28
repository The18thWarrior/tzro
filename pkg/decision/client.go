package decision

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// LocalDaemonProvider manages a local decision daemon (such as bin/jev-score)
// running as a child process over stdin/stdout JSON-RPC.
type LocalDaemonProvider struct {
	cmdPath string
	args    []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	running bool
	closed  bool
}

// NewLocalDaemonProvider creates a new provider targeting a local executable.
func NewLocalDaemonProvider(cmdPath string, args ...string) *LocalDaemonProvider {
	return &LocalDaemonProvider{
		cmdPath: cmdPath,
		args:    args,
	}
}

// Start spawns the daemon process and drains the ready line.
func (p *LocalDaemonProvider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("provider closed")
	}
	return p.startLocked(ctx)
}

func (p *LocalDaemonProvider) startLocked(ctx context.Context) error {
	if p.running {
		return nil
	}

	p.cmd = exec.CommandContext(ctx, p.cmdPath, p.args...)

	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	p.cmd.Stderr = os.Stderr

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	p.stdin = stdin
	p.stdout = bufio.NewReader(stdout)
	p.running = true

	// Drain the initial ready line if emitted
	readyCh := make(chan struct{})
	go func() {
		defer close(readyCh)
		line, err := p.stdout.ReadBytes('\n')
		if err != nil {
			return
		}
		var status struct {
			Status string `json:"status"`
			Ready  bool   `json:"ready"`
		}
		if json.Unmarshal(line, &status) == nil && (status.Status == "ready" || status.Ready) {
			return
		}
	}()

	select {
	case <-readyCh:
		// Ready handshake drained
	case <-time.After(30 * time.Second):
		// Timed out or no ready line emitted; proceed
	case <-ctx.Done():
		p.cleanupLocked()
		return ctx.Err()
	}

	go func(cmd *exec.Cmd) {
		_ = cmd.Wait()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}(p.cmd)

	return nil
}

// Evaluate sends a decision request to the daemon and returns the parsed response.
func (p *LocalDaemonProvider) Evaluate(ctx context.Context, req *DecisionRequest) (*DecisionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, errors.New("provider closed")
	}

	// Self-heal: restart daemon if terminated
	if !p.running {
		if err := p.startLocked(ctx); err != nil {
			return nil, fmt.Errorf("daemon restart failed: %w", err)
		}
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	payload = append(payload, '\n')

	if _, err := p.stdin.Write(payload); err != nil {
		p.cleanupLocked()
		// Try single reconnect
		if restartErr := p.startLocked(ctx); restartErr != nil {
			return nil, fmt.Errorf("write request failed, restart failed: %w", restartErr)
		}
		if _, writeErr := p.stdin.Write(payload); writeErr != nil {
			return nil, fmt.Errorf("write request after restart: %w", writeErr)
		}
	}

	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := p.stdout.ReadBytes('\n')
		ch <- readResult{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			p.cleanupLocked()
			return nil, fmt.Errorf("read response: %w", res.err)
		}
		var resp DecisionResponse
		if err := json.Unmarshal(res.line, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal response (%s): %w", string(res.line), err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("daemon error: %s", resp.Error)
		}
		return &resp, nil
	}
}

// Close gracefully terminates the daemon process.
func (p *LocalDaemonProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true
	p.cleanupLocked()
	return nil
}

func (p *LocalDaemonProvider) cleanupLocked() {
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(50 * time.Millisecond)
		_ = p.cmd.Process.Kill()
	}
	p.running = false
	p.cmd = nil
	p.stdout = nil
}
