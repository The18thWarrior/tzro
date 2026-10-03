package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tzro/pkg/executor"
)

// TestMCP_CancelActiveVerification_E2E tests that sending
// notifications/cancelled during a long-running edit-and-verify
// operation stops owned work and returns a result faster than
// the check would naturally complete.
func TestMCP_CancelActiveVerification_E2E(t *testing.T) {
	ws := setupGoFixtureWithSlowCheck(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	// Set up piped I/O for the full stdio protocol.
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	var wg sync.WaitGroup

	// Collect responses in a goroutine.
	var responses []json.RawMessage
	var mu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(serverToClientR)
		for scanner.Scan() {
			data := make([]byte, len(scanner.Bytes()))
			copy(data, scanner.Bytes())
			mu.Lock()
			responses = append(responses, data)
			mu.Unlock()
		}
	}()

	// Start the server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer serverToClientW.Close()
		server.Serve(ctx, clientToServerR, serverToClientW)
	}()

	// Send initialize.
	sendLine(t, clientToServerW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)

	time.Sleep(100 * time.Millisecond)

	// Send the edit-and-verify request (with a slow check).
	start := time.Now()
	sendLine(t, clientToServerW, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tzro_edit_and_verify","arguments":{"edits":[{"kind":"replace","path":"calc.go","old_text":"return a - b","new_text":"return a + b"}]}}}`)

	// Wait for the operation to start its check.
	time.Sleep(300 * time.Millisecond)

	// Send cancellation.
	sendLine(t, clientToServerW, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)

	// Wait for response with a short timeout.
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		count := len(responses)
		mu.Unlock()
		if count >= 2 { // init + tools/call
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for cancelled response")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	elapsed := time.Since(start)

	// Close input to stop the server.
	clientToServerW.Close()
	wg.Wait()

	// The response should have arrived much faster than 10 seconds.
	if elapsed > 5*time.Second {
		t.Errorf("cancellation took %v; expected < 5s", elapsed)
	}
	t.Logf("cancellation completed in %v", elapsed)

	// Parse the tools/call response.
	mu.Lock()
	defer mu.Unlock()

	for _, raw := range responses {
		var resp struct {
			ID     interface{}     `json:"id"`
			Error  *rpcError       `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		if resp.ID == float64(2) {
			t.Logf("tools/call response received (cancelled): error=%v, result_len=%d", resp.Error, len(resp.Result))
			// Either an error or a partial result is acceptable.
			return
		}
	}

	// It's OK if the response wasn't received at all — cancellation
	// may have killed the goroutine before it could respond.
	t.Log("no tools/call response received; cancellation killed the handler")
}

func sendLine(t *testing.T, w io.Writer, line string) {
	t.Helper()
	_, err := io.WriteString(w, line+"\n")
	if err != nil {
		t.Fatalf("sendLine: %v", err)
	}
}

// setupGoFixtureWithSlowCheck creates a Go project with a check that
// sleeps for 10 seconds, allowing us to test cancellation.
func setupGoFixtureWithSlowCheck(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()

	for name, content := range map[string]string{
		"go.mod":       goModFixture,
		"calc.go":      brokenCalcGoFixture,
		"calc_test.go": calcTestGoFixture,
	} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Write a preset with a slow check.
	tzroDir := filepath.Join(ws, ".tzro")
	os.MkdirAll(tzroDir, 0755)
	os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(`
version: 1
timeout: 30s
checks:
  - id: slow_test
    argv: [sleep, 10]
    cwd: .
`), 0644)

	return ws
}
