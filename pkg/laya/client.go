package laya

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

type DaemonClient struct {
	cmdPath string
	args    []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	running bool
	closed  bool
}

func NewDaemonClient(cmdPath string, args ...string) *DaemonClient {
	return &DaemonClient{
		cmdPath: cmdPath,
		args:    args,
	}
}

func (c *DaemonClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("client closed")
	}
	return c.startLocked(ctx)
}

func (c *DaemonClient) startLocked(ctx context.Context) error {
	if c.running {
		return nil
	}

	c.cmd = exec.CommandContext(ctx, c.cmdPath, c.args...)

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	c.cmd.Stderr = os.Stderr

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	c.running = true

	// Drain the initial ready/status line if the daemon emits one.
	// The laya_worker.py and gliner_worker.py both print {"status":"ready"}\n
	// before accepting requests. We read it here so the first Evaluate()
	// call gets the actual response, not the ready line.
	readyCh := make(chan struct{})
	go func() {
		defer close(readyCh)
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return
		}
		// Check if it's a status/ready line; if not, this is a problem
		// but we can't unread it, so just log and move on.
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(line, &status) == nil && status.Status != "" {
			return // Successfully drained the ready line
		}
	}()

	// Wait up to 30 seconds for the ready line (model loading can be slow)
	select {
	case <-readyCh:
		// Ready line drained
	case <-time.After(30 * time.Second):
		// Timed out waiting for ready — daemon may not emit one, proceed anyway
	}

	go func(cmd *exec.Cmd) {
		cmd.Wait()
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.cmd == cmd {
			c.running = false
		}
	}(c.cmd)

	return nil
}

func (c *DaemonClient) Evaluate(ctx context.Context, req *DecisionRequest) (*DecisionResponse, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("client closed")
	}
	if !c.running {
		if err := c.startLocked(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	// Hold the lock through the entire evaluation to serialize concurrent
	// callers and prevent interleaved stdin/stdout corruption.
	resp, err := c.doEvaluateLocked(ctx, req)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.EPIPE) || isClosedError(err) {
			if c.cmd != nil && c.cmd.Process != nil {
				c.cmd.Process.Kill()
			}
			c.running = false
			if err := c.startLocked(ctx); err != nil {
				c.mu.Unlock()
				return nil, fmt.Errorf("restart failed: %w", err)
			}

			resp, err = c.doEvaluateLocked(ctx, req)
			c.mu.Unlock()
			return resp, err
		}
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()
	return resp, nil
}

func isClosedError(err error) bool {
	return errors.Is(err, os.ErrClosed) || err.Error() == "io: read/write on closed pipe"
}

// doEvaluateLocked performs a single request-response cycle over stdin/stdout.
// The caller MUST hold c.mu for the entire duration.
func (c *DaemonClient) doEvaluateLocked(ctx context.Context, req *DecisionRequest) (*DecisionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')

	stdin := c.stdin
	stdout := c.stdout

	if stdin == nil || stdout == nil {
		return nil, errors.New("daemon not running")
	}

	// Write request — run in goroutine so we can select on ctx
	errCh := make(chan error, 1)
	go func() {
		_, err := stdin.Write(b)
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
	}

	// Read response
	type readResult struct {
		line []byte
		err  error
	}
	resCh := make(chan readResult, 1)
	go func() {
		line, err := stdout.ReadBytes('\n')
		resCh <- readResult{line, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resCh:
		if res.err != nil {
			return nil, res.err
		}
		var resp DecisionResponse
		if err := json.Unmarshal(res.line, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}
}

func (c *DaemonClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.running = false

	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	return nil
}
