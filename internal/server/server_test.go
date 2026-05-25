package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/stream"
)

func TestSSEEventsEndpoint(t *testing.T) {
	// Initialize GlobalBus
	if stream.GlobalBus == nil {
		stream.GlobalBus = stream.NewBus()
	}

	// Set up a mock http router matching the actual server routing
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", handleEvents)

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Make GET request to the event stream
	clientReq, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	client := &http.Client{}
	resp, err := client.Do(clientReq)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	defer resp.Body.Close()

	// 1. Verify headers
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %s", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("Connection") != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %s", resp.Header.Get("Connection"))
	}

	// 2. Publish chunk and verify client receives it
	testChunk := stream.StreamChunk{
		StreamID: "test-stream-sse",
		Source:   "chat",
		Type:     "token",
		Content:  "hello-sse-world",
	}

	go func() {
		// Wait to guarantee connection and subscriber setup are complete
		time.Sleep(100 * time.Millisecond)
		stream.GlobalBus.Publish(testChunk)
	}()

	buf := make([]byte, 1024)
	n, err := resp.Body.Read(buf)
	if err != nil && err.Error() != "EOF" {
		t.Fatalf("failed to read response: %v", err)
	}

	respStr := string(buf[:n])
	if !strings.Contains(respStr, "data: ") {
		t.Fatalf("expected SSE data format in body, got %q", respStr)
	}

	// Extract and parse JSON
	lines := strings.Split(respStr, "\n")
	var dataLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	if dataLine == "" {
		t.Fatalf("no data: prefix line found in %q", respStr)
	}

	var received stream.StreamChunk
	if err := json.Unmarshal([]byte(dataLine), &received); err != nil {
		t.Fatalf("failed to parse chunk JSON: %v", err)
	}

	if received.StreamID != testChunk.StreamID || received.Content != testChunk.Content {
		t.Errorf("mismatched stream chunk, expected %+v, got %+v", testChunk, received)
	}
}

func TestHandleChatStreaming(t *testing.T) {
	// Initialize GlobalBus
	if stream.GlobalBus == nil {
		stream.GlobalBus = stream.NewBus()
	}

	// Configure local model manager for mock responses
	// In local/cooperative mode, it will use llama-server mock
	mockLlamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"hello"}}]}

data: {"choices":[{"delta":{"content":" world"}}]}

data: [DONE]

`))
	}))
	defer mockLlamaServer.Close()

	// Update local manager status to mock active sidecar
	mgr := inference.GlobalLocalModel
	mgr.Status = "Active"
	listenerAddr := mockLlamaServer.Listener.Addr().String()
	parts := strings.Split(listenerAddr, ":")
	portStr := parts[len(parts)-1]
	
	// Set the port
	var activePort int
	for _, char := range portStr {
		if char >= '0' && char <= '9' {
			activePort = activePort*10 + int(char-'0')
		}
	}
	mgr.ActivePort = activePort

	// Set model mode to cooperative (default fallback local model)
	_ = os.MkdirAll(".tzro", 0755)
	defer os.RemoveAll(".tzro")

	oldConfig := config.Get()
	cfg := oldConfig
	cfg.ModelMode = "cooperative"
	if err := config.Save(&cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}
	defer func() {
		_ = config.Save(&oldConfig)
	}()

	// Create test handler and POST request
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", handleChat)

	reqBody, _ := json.Marshal(ChatRequest{Message: "hello tzro"})
	req := httptest.NewRequest("POST", "/api/chat", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	// Subscribe to GlobalBus to watch for streamed tokens from the background thread
	sub := stream.GlobalBus.Subscribe(nil)
	defer sub.Unsubscribe()

	// Execute request
	mux.ServeHTTP(w, req)

	// 1. Verify standard POST response returns immediately
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp ChatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Complexity != "T0" {
		t.Errorf("expected T0 complexity, got %s", resp.Complexity)
	}
	if resp.Intent.Type != "chat" {
		t.Errorf("expected intent type chat, got %s", resp.Intent.Type)
	}
	// Verify that a stream ID was returned
	if !strings.HasPrefix(resp.TaskID, "task_") {
		t.Errorf("expected taskId prefix task_, got %s", resp.TaskID)
	}

	// 2. Verify that tokens were published to the GlobalBus asynchronously
	var tokensReceived []string
	timeout := time.After(2 * time.Second)
	doneChan := make(chan bool)

	go func() {
		for {
			select {
			case chunk, ok := <-sub.Ch:
				if !ok {
					return
				}
				if chunk.Source == "chat" {
					if chunk.Type == "token" {
						tokensReceived = append(tokensReceived, chunk.Content)
					} else if chunk.Type == "done" {
						doneChan <- true
						return
					}
				}
			case <-timeout:
				doneChan <- false
				return
			}
		}
	}()

	success := <-doneChan
	if !success {
		t.Errorf("timed out waiting for tokens on GlobalBus, received so far: %q", tokensReceived)
	}

	fullReply := strings.Join(tokensReceived, "")
	if fullReply != "hello world" {
		t.Errorf("expected full reply 'hello world', got %q", fullReply)
	}
}
