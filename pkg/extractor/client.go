package extractor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type WorkerClient struct {
	cmdPath string
	args    []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	running bool
}

func NewWorkerClient(cmdPath string, args ...string) *WorkerClient {
	return &WorkerClient{
		cmdPath: cmdPath,
		args:    args,
	}
}

func (c *WorkerClient) startInternal(ctx context.Context) error {
	c.cmd = exec.CommandContext(context.Background(), c.cmdPath, c.args...)
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start cmd: %w", err)
	}

	c.stdin = stdin
	c.stdout = bufio.NewScanner(stdout)
	c.running = true

	// Consume the initial "ready" status line from the worker.
	if c.stdout.Scan() {
		// Optionally parse to verify readiness, but for now just discard.
		_ = c.stdout.Text()
	}
	return nil
}

func (c *WorkerClient) stopInternal() {
	if c.running {
		c.stdin.Close()
		if c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
		c.cmd.Wait()
		c.running = false
	}
}

func (c *WorkerClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	return c.startInternal(ctx)
}

func (c *WorkerClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopInternal()
	return nil
}

func (c *WorkerClient) Extract(ctx context.Context, req *ExtractionRequest) (*ExtractionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		if err := c.startInternal(ctx); err != nil {
			return nil, err
		}
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	_, err = c.stdin.Write(reqBytes)
	if err != nil {
		c.stopInternal()
		if err := c.startInternal(ctx); err != nil {
			return nil, fmt.Errorf("restart worker: %w", err)
		}
		if _, err := c.stdin.Write(reqBytes); err != nil {
			return nil, fmt.Errorf("write request after restart: %w", err)
		}
	}

	type result struct {
		resp *ExtractionResponse
		err  error
	}
	resCh := make(chan result, 1)

	go func() {
		if c.stdout.Scan() {
			var resp ExtractionResponse
			if err := json.Unmarshal(c.stdout.Bytes(), &resp); err != nil {
				resCh <- result{err: fmt.Errorf("unmarshal response: %w", err)}
			} else {
				resCh <- result{resp: &resp}
			}
		} else {
			if err := c.stdout.Err(); err != nil {
				resCh <- result{err: fmt.Errorf("read response: %w", err)}
			} else {
				resCh <- result{err: fmt.Errorf("worker closed connection")}
			}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resCh:
		if res.err != nil {
			c.stopInternal()
		}
		return res.resp, res.err
	}
}
