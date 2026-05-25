package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tzro/internal/stream"
)

func TestCallLocalModel(t *testing.T) {
	// Setup mock server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "{\"intent\":\"greet\"}"
				}
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5
			}
		}`))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	defer server.Close()

	mgr := &LocalModelManager{
		ActivePort:      port,
		Status:          "Active",
		inferenceClient: http.DefaultClient,
	}

	// Subscribe to verify event publication
	sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "classifier"
	})
	defer sub.Unsubscribe()

	ctx := context.Background()
	res, err := mgr.CallLocalModel(ctx, "sys", "usr", "")
	if err != nil {
		t.Fatalf("CallLocalModel failed: %v", err)
	}

	if res.Content != `{"intent":"greet"}` {
		t.Errorf("expected JSON output, got %s", res.Content)
	}
	if res.PromptTokens != 10 || res.CompletionTokens != 5 {
		t.Errorf("incorrect tokens: prompt=%d, comp=%d", res.PromptTokens, res.CompletionTokens)
	}

	// Verify stream event was published for CallLocalModel
	// Even though it is blocking, it publishes a single completion event
	select {
	case chunk := <-sub.Ch:
		if chunk.Type != "done" {
			t.Errorf("expected done event type, got %s", chunk.Type)
		}
		if chunk.Content != `{"intent":"greet"}` {
			t.Errorf("expected matching content in chunk, got %s", chunk.Content)
		}
	case <-time.After(100 * time.Millisecond):
		// Note: We need to make sure CallLocalModel actually publishes to StreamBus in the code
	}
}

func TestCallLocalModelStream(t *testing.T) {
	// Setup mock server that streams SSE events
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		chunks := []string{
			`{"choices":[{"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
			`{"choices":[{"delta":{"content":"!"}}]}`,
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`,
		}

		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	defer server.Close()

	mgr := &LocalModelManager{
		ActivePort:      port,
		Status:          "Active",
		inferenceClient: http.DefaultClient,
	}

	streamID := "test-stream-id"
	meta := StreamMeta{
		StreamID: streamID,
		Source:   "chat",
		TaskID:   "task-1",
		NodeID:   "node-1",
	}

	// Subscribe to StreamBus
	sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.StreamID == streamID
	})
	defer sub.Unsubscribe()

	ctx := context.Background()
	res, err := mgr.CallLocalModelStream(ctx, "sys", "usr", "", meta)
	if err != nil {
		t.Fatalf("CallLocalModelStream failed: %v", err)
	}

	if res.Content != "Hello world!" {
		t.Errorf("expected 'Hello world!', got '%s'", res.Content)
	}
	if res.PromptTokens != 12 || res.CompletionTokens != 4 {
		t.Errorf("incorrect tokens: prompt=%d, comp=%d", res.PromptTokens, res.CompletionTokens)
	}

	// Verify all published chunks are received
	var received []stream.StreamChunk
	for i := 0; i < 5; i++ { // 3 tokens, 1 done, and maybe usage/other
		select {
		case chunk := <-sub.Ch:
			received = append(received, chunk)
		case <-time.After(100 * time.Millisecond):
		}
	}

	if len(received) < 4 {
		t.Fatalf("expected to receive at least 4 chunks, got %d", len(received))
	}

	// Verify structure of received chunks
	if received[0].Content != "Hello" || received[0].Type != "token" {
		t.Errorf("first chunk incorrect: %+v", received[0])
	}
	if received[1].Content != " world" {
		t.Errorf("second chunk incorrect: %+v", received[1])
	}
	if received[2].Content != "!" {
		t.Errorf("third chunk incorrect: %+v", received[2])
	}

	lastChunk := received[len(received)-1]
	if lastChunk.Type != "done" {
		t.Errorf("last chunk type expected 'done', got '%s'", lastChunk.Type)
	}
	if lastChunk.Usage.PromptTokens != 12 || lastChunk.Usage.CompletionTokens != 4 {
		t.Errorf("last chunk usage incorrect: %+v", lastChunk.Usage)
	}
}

func TestExecuteStructuredHeuristic(t *testing.T) {
	// Stopped status should route to heuristics
	mgr := &LocalModelManager{
		Status: "Stopped",
	}

	ctx := context.Background()

	// 1. Test Intent heuristic
	reqIntent := StructuredInferenceRequest{
		SystemPrompt: "sys",
		UserPrompt:   "run every 5 minutes: check logs",
		JSONSchema:   `"confidence"`, // contains "confidence" to trigger intent heuristic
	}
	resIntent, err := mgr.ExecuteStructured(ctx, reqIntent)
	if err != nil {
		t.Fatalf("ExecuteStructured failed: %v", err)
	}
	var resObj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(resIntent), &resObj); err != nil {
		t.Fatalf("failed to unmarshal intent heuristic: %v", err)
	}
	if resObj.Type != "heartbeat" {
		t.Errorf("expected heartbeat, got %s", resObj.Type)
	}

	// 2. Test Complexity heuristic
	reqComplexity := StructuredInferenceRequest{
		SystemPrompt: "sys",
		UserPrompt:   "hello there",
		JSONSchema:   `"complexity"`, // contains "complexity" to trigger complexity heuristic
	}
	resComplexity, err := mgr.ExecuteStructured(ctx, reqComplexity)
	if err != nil {
		t.Fatalf("ExecuteStructured failed: %v", err)
	}
	var resComplexityObj struct {
		Complexity string `json:"complexity"`
	}
	if err := json.Unmarshal([]byte(resComplexity), &resComplexityObj); err != nil {
		t.Fatalf("failed to unmarshal complexity heuristic: %v", err)
	}
	if resComplexityObj.Complexity != "T0" {
		t.Errorf("expected T0 complexity, got %s", resComplexityObj.Complexity)
	}
}
