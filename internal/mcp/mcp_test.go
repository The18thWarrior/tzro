package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHelperProcess serves as the stdio-based JSON-RPC mock subprocess for our tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()

		var req struct {
			JSONRPC string                 `json:"jsonrpc"`
			ID      string                 `json:"id"`
			Method  string                 `json:"method"`
			Params  map[string]interface{} `json:"params"`
		}

		if err := json.Unmarshal([]byte(line), &req); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal error: %v\n", err)
			os.Exit(1)
		}

		// Handle methods
		if req.Method == "tools/list" {
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "mock_tool",
							"description": "A test tool",
							"inputSchema": map[string]interface{}{"type": "object"},
						},
					},
				},
			}
			respBytes, _ := json.Marshal(resp)
			fmt.Println(string(respBytes))
		} else if req.Method == "tools/call" {
			args, _ := req.Params["arguments"].(map[string]interface{})
			if args != nil && args["crash"] == true {
				os.Exit(2)
			}

			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": fmt.Sprintf("echo: %v", args["val"]),
						},
					},
				},
			}
			respBytes, _ := json.Marshal(resp)
			fmt.Println(string(respBytes))
		} else {
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]interface{}{},
			}
			respBytes, _ := json.Marshal(resp)
			fmt.Println(string(respBytes))
		}
	}
	os.Exit(0)
}

func TestMCPDaemon_AutoRecovery(t *testing.T) {
	config := MCPServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}

	d := NewMCPDaemon("test-daemon", config)
	ctx := context.Background()

	// 1. Start the daemon
	if err := d.Start(ctx); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	defer d.Stop()

	// 2. Perform a successful RPC call
	res, err := d.Call("tools/call", map[string]interface{}{
		"name": "mock_tool",
		"arguments": map[string]interface{}{
			"val": "hello",
		},
	})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	var parsed struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(parsed.Result.Content) == 0 || parsed.Result.Content[0].Text != "echo: hello" {
		t.Fatalf("unexpected first response: %s", res)
	}

	// Capture the original process PID
	d.mutex.Lock()
	oldPID := d.cmd.Process.Pid
	d.mutex.Unlock()

	// 3. Force a process crash externally by sending SIGKILL to the child process
	d.mutex.Lock()
	err = d.cmd.Process.Kill()
	d.mutex.Unlock()
	if err != nil {
		t.Fatalf("failed to kill process: %v", err)
	}

	// 4. Perform another call and verify auto-recovery resolves it successfully
	res2, err := d.Call("tools/call", map[string]interface{}{
		"name": "mock_tool",
		"arguments": map[string]interface{}{
			"val": "world",
		},
	})
	if err != nil {
		t.Fatalf("call after crash failed: %v", err)
	}

	if err := json.Unmarshal([]byte(res2), &parsed); err != nil {
		t.Fatalf("failed to parse second response: %v", err)
	}
	if len(parsed.Result.Content) == 0 || parsed.Result.Content[0].Text != "echo: world" {
		t.Fatalf("unexpected second response: %s", res2)
	}

	// Capture the new process PID and assert it has changed
	d.mutex.Lock()
	newPID := d.cmd.Process.Pid
	d.mutex.Unlock()

	if oldPID == newPID {
		t.Fatalf("process was not restarted, PID is still %d", oldPID)
	}
}

func TestMCPDaemon_ConcurrentRecovery(t *testing.T) {
	config := MCPServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}

	d := NewMCPDaemon("test-daemon-concurrent", config)
	ctx := context.Background()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	defer d.Stop()

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Perform an initial successful call
			_, err := d.Call("tools/call", map[string]interface{}{
				"name": "mock_tool",
				"arguments": map[string]interface{}{
					"val": fmt.Sprintf("initial-%d", id),
				},
			})
			if err != nil {
				errChan <- fmt.Errorf("goroutine %d initial call failed: %w", id, err)
				return
			}

			// If we are goroutine 0, simulate a sudden process crash in the middle
			if id == 0 {
				time.Sleep(10 * time.Millisecond)
				d.mutex.Lock()
				if d.cmd != nil && d.cmd.Process != nil {
					_ = d.cmd.Process.Kill()
				}
				d.mutex.Unlock()
			}

			// Wait a bit to ensure the crash has occurred or is handled concurrently
			time.Sleep(20 * time.Millisecond)

			// Perform a call which should trigger/await self-healing
			res, err := d.Call("tools/call", map[string]interface{}{
				"name": "mock_tool",
				"arguments": map[string]interface{}{
					"val": fmt.Sprintf("after-%d", id),
				},
			})
			if err != nil {
				errChan <- fmt.Errorf("goroutine %d call after crash failed: %w", id, err)
				return
			}

			var parsed struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(res), &parsed); err != nil {
				errChan <- fmt.Errorf("goroutine %d failed to parse response: %w", id, err)
				return
			}
			expectedText := fmt.Sprintf("echo: after-%d", id)
			if len(parsed.Result.Content) == 0 || parsed.Result.Content[0].Text != expectedText {
				errChan <- fmt.Errorf("goroutine %d got unexpected response: %s", id, res)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Error(err)
	}
}

func TestMCPDaemon_Docker(t *testing.T) {
	// Override execCommandContext to capture the arguments that would be executed
	var capturedCmd string
	var capturedArgs []string

	oldExecCommandContext := execCommandContext
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedCmd = name
		capturedArgs = args
		// Return a harmless helper process cmd instead of running real docker
		cmd := oldExecCommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}
	defer func() { execCommandContext = oldExecCommandContext }()

	// Configure a containerized daemon
	config := MCPServerConfig{
		Command:     "python",
		Args:        []string{"server.py"},
		Env:         map[string]string{"SLACK_BOT_TOKEN": "$TEST_DOCKER_SLACK_TOKEN", "STATIC_VAR": "static_val"},
		UseDocker:   true,
		DockerImage: "mcp/slack:latest",
		DockerOpts:  []string{"--cpus=0.5", "--memory=512m"},
	}

	// Set env var to test resolution
	os.Setenv("TEST_DOCKER_SLACK_TOKEN", "xoxb-resolved-123")
	defer os.Unsetenv("TEST_DOCKER_SLACK_TOKEN")

	d := NewMCPDaemon("docker-test", config)
	ctx := context.Background()

	// Start the daemon (this triggers startNoLock under our mocked execCommandContext)
	err := d.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start containerized daemon: %v", err)
	}
	defer d.Stop()

	// Assert correct command name (docker)
	if capturedCmd != "docker" {
		t.Errorf("expected command 'docker', got: %s", capturedCmd)
	}

	// Assert docker run args are correct
	argsStr := strings.Join(capturedArgs, " ")
	expectedArgs := []string{
		"run", "-i", "--rm", "--cpus=0.5", "--memory=512m",
		"-v", "mcp/slack:latest", "python", "server.py",
	}
	for _, arg := range expectedArgs {
		if !strings.Contains(argsStr, arg) {
			t.Errorf("expected docker run args to contain '%s', got: %s", arg, argsStr)
		}
	}

	// Assert strict environment declarations are resolved and injected via -e
	if !strings.Contains(argsStr, "-e SLACK_BOT_TOKEN=xoxb-resolved-123") {
		t.Errorf("expected SLACK_BOT_TOKEN to be resolved and injected, got: %s", argsStr)
	}
	if !strings.Contains(argsStr, "-e STATIC_VAR=static_val") {
		t.Errorf("expected STATIC_VAR to be injected, got: %s", argsStr)
	}
}
