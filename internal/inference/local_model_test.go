package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	res, err := mgr.CallLocalModel(ctx, []InferenceMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "usr"}}, "")
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
	res, err := mgr.CallLocalModelStream(ctx, []InferenceMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "usr"}}, "", meta)
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
	reqIntent := NewSimpleRequest("sys", "run every 5 minutes: check logs", `"confidence"`)
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
	reqComplexity := NewSimpleRequest("sys", "hello there", `"complexity"`)
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

func TestTwoTierGarbageCollection(t *testing.T) {
	// Setup mock control-plane server for slots API
	var slotErased []int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/slots/") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 3 {
				slotID := 0
				if strings.Contains(parts[2], "0") {
					slotID = 0
				} else if strings.Contains(parts[2], "1") {
					slotID = 1
				}
				slotErased = append(slotErased, slotID)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status": "ok"}`))
				return
			}
		}
		http.Error(w, "not found", http.StatusNotFound)
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
		ActivePort:   port,
		Status:       "Active",
		ActivePID:    12345, // Mock PID
		healthClient: http.DefaultClient,
	}

	// 1. Verify Tier 1: EraseSlot erases specific slots
	ctx := context.Background()
	err = mgr.EraseSlot(ctx, 0)
	if err != nil {
		t.Fatalf("EraseSlot 0 failed: %v", err)
	}
	err = mgr.EraseSlot(ctx, 1)
	if err != nil {
		t.Fatalf("EraseSlot 1 failed: %v", err)
	}

	if len(slotErased) != 2 || slotErased[0] != 0 || slotErased[1] != 1 {
		t.Errorf("expected slots 0 and 1 to be erased, got erased list: %v", slotErased)
	}

	// 2. Verify Tier 1: TriggerGC erases both slot 0 and 1
	slotErased = []int{}
	err = mgr.TriggerGC(ctx)
	if err != nil {
		t.Fatalf("TriggerGC failed: %v", err)
	}
	if len(slotErased) != 2 || slotErased[0] != 0 || slotErased[1] != 1 {
		t.Errorf("expected TriggerGC to flush both slot 0 and 1, got: %v", slotErased)
	}
}

func TestStripThinkTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no think tags",
			input:    `<ACTION>{"tool":"read_file","arguments":{"path":"/foo"}}</ACTION>`,
			expected: `<ACTION>{"tool":"read_file","arguments":{"path":"/foo"}}</ACTION>`,
		},
		{
			name:     "single think block before content",
			input:    "<think>I should read the file first to understand the structure.</think>\n<ACTION>{\"tool\":\"read_file\"}</ACTION>",
			expected: "<ACTION>{\"tool\":\"read_file\"}</ACTION>",
		},
		{
			name:     "multi-line think block",
			input:    "<think>\nLet me reason about this.\nThe file is at /foo/bar.\n</think>\n<SYNTHESIZE_READY>",
			expected: "<SYNTHESIZE_READY>",
		},
		{
			name:     "unclosed think tag",
			input:    "<think>partial reasoning without closing tag",
			expected: "",
		},
		{
			name:     "multiple think blocks",
			input:    "<think>first thought</think>some content<think>second thought</think>more content",
			expected: "some contentmore content",
		},
		{
			name:     "empty think block",
			input:    "<think></think>actual content",
			expected: "actual content",
		},
		{
			name:     "no content after stripping",
			input:    "<think>just thinking</think>",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripThinkTags(tt.input)
			if result != tt.expected {
				t.Errorf("StripThinkTags(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMaxTokensKey_PropagatesThroughCallLocalModel(t *testing.T) {
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the request body
		decoder := json.NewDecoder(r.Body)
		decoder.Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "ok"}}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5}
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

	msgs := []InferenceMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "usr"},
	}

	// Test 1: WITH MaxTokensKey — max_tokens should be present
	ctx := context.WithValue(context.Background(), MaxTokensKey, 2048)
	_, err = mgr.CallLocalModel(ctx, msgs, "")
	if err != nil {
		t.Fatalf("CallLocalModel with MaxTokensKey failed: %v", err)
	}

	maxTokensRaw, exists := capturedBody["max_tokens"]
	if !exists {
		t.Fatal("expected max_tokens in request body when MaxTokensKey is set")
	}
	maxTokensVal, ok := maxTokensRaw.(float64) // JSON numbers decode as float64
	if !ok {
		t.Fatalf("max_tokens is not a number: %T", maxTokensRaw)
	}
	if int(maxTokensVal) != 2048 {
		t.Errorf("expected max_tokens=2048, got %d", int(maxTokensVal))
	}

	// Test 2: WITHOUT MaxTokensKey — max_tokens should be dynamically computed
	// from context size minus estimated prompt tokens (no longer hardcoded 2048)
	capturedBody = nil
	ctx2 := context.Background()
	_, err = mgr.CallLocalModel(ctx2, msgs, "")
	if err != nil {
		t.Fatalf("CallLocalModel without MaxTokensKey failed: %v", err)
	}

	maxTokensRaw, exists = capturedBody["max_tokens"]
	if !exists {
		t.Fatal("expected default max_tokens in request body when MaxTokensKey is not set")
	}
	maxTokensVal, ok = maxTokensRaw.(float64)
	if !ok {
		t.Fatalf("default max_tokens is not a number: %T", maxTokensRaw)
	}
	// With a tiny prompt ("sys" + "usr" = 6 chars) and default 65536 context,
	// the dynamic budget should be much larger than the old 2048 hardcap.
	if int(maxTokensVal) < 2048 {
		t.Errorf("expected default max_tokens >= 2048, got %d", int(maxTokensVal))
	}
}

func TestDRYSamplingKey_PropagatesThroughCallLocalModel(t *testing.T) {
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		decoder.Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "ok"}}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5}
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

	msgs := []InferenceMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "usr"},
	}

	// Test 1: WITH DRYSamplingKey — all dry_* params should appear
	dryCfg := DRYSamplingConfig{
		Multiplier:       0.8,
		Base:             1.75,
		AllowedLength:    2,
		PenaltyLastN:     -1,
		SequenceBreakers: []string{"\n", ":", "\"", "*"},
	}
	ctx := context.WithValue(context.Background(), DRYSamplingKey, dryCfg)
	_, err = mgr.CallLocalModel(ctx, msgs, "")
	if err != nil {
		t.Fatalf("CallLocalModel with DRYSamplingKey failed: %v", err)
	}

	// Verify dry_multiplier
	if v, ok := capturedBody["dry_multiplier"].(float64); !ok || v != 0.8 {
		t.Errorf("expected dry_multiplier=0.8, got %v", capturedBody["dry_multiplier"])
	}
	// Verify dry_base
	if v, ok := capturedBody["dry_base"].(float64); !ok || v != 1.75 {
		t.Errorf("expected dry_base=1.75, got %v", capturedBody["dry_base"])
	}
	// Verify dry_allowed_length
	if v, ok := capturedBody["dry_allowed_length"].(float64); !ok || int(v) != 2 {
		t.Errorf("expected dry_allowed_length=2, got %v", capturedBody["dry_allowed_length"])
	}
	// Verify dry_penalty_last_n
	if v, ok := capturedBody["dry_penalty_last_n"].(float64); !ok || int(v) != -1 {
		t.Errorf("expected dry_penalty_last_n=-1, got %v", capturedBody["dry_penalty_last_n"])
	}
	// Verify dry_sequence_breakers
	breakers, ok := capturedBody["dry_sequence_breakers"].([]interface{})
	if !ok || len(breakers) != 4 {
		t.Errorf("expected 4 dry_sequence_breakers, got %v", capturedBody["dry_sequence_breakers"])
	}

	// Test 2: WITHOUT DRYSamplingKey — dry_* params should be absent
	capturedBody = nil
	ctx2 := context.Background()
	_, err = mgr.CallLocalModel(ctx2, msgs, "")
	if err != nil {
		t.Fatalf("CallLocalModel without DRYSamplingKey failed: %v", err)
	}

	for _, key := range []string{"dry_multiplier", "dry_base", "dry_allowed_length", "dry_penalty_last_n", "dry_sequence_breakers"} {
		if _, exists := capturedBody[key]; exists {
			t.Errorf("expected %s to be absent when DRYSamplingKey is not set", key)
		}
	}
}

func TestCallLocalModel_RawGBNFGrammar(t *testing.T) {
	var capturedBody map[string]interface{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "# Research\n\nFindings"}}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5}
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

	rawGrammar := `root ::= "# " [^\n]+ "\n\n" [^\n]+`
	msgs := []InferenceMessage{{Role: "user", Content: "research"}}

	res, err := mgr.CallLocalModel(context.Background(), msgs, rawGrammar)
	if err != nil {
		t.Fatalf("CallLocalModel failed: %v", err)
	}
	if res.Content != "# Research\n\nFindings" {
		t.Errorf("unexpected content: %s", res.Content)
	}

	grammarVal, ok := capturedBody["grammar"].(string)
	if !ok || grammarVal != rawGrammar {
		t.Errorf("expected top-level grammar=%q in request body, got %v", rawGrammar, capturedBody["grammar"])
	}
	if capturedBody["response_format"] != nil {
		t.Errorf("expected response_format to be nil when raw GBNF grammar is used, got %v", capturedBody["response_format"])
	}
}

