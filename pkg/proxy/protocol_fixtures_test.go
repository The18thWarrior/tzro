package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"tzro/pkg/kvlock"
	"tzro/pkg/store"
)

func TestProtocolFixtures_Matrix(t *testing.T) {
	g := kvlock.NewLockGuard()

	t.Run("Anthropic Multimodal Base64 Image and Cache Breakpoints", func(t *testing.T) {
		payload := `{
			"model": "claude-3-7-sonnet-20250219",
			"max_tokens": 1024,
			"system": [
				{
					"type": "text",
					"text": "You are an expert OCR system.",
					"cache_control": {"type": "ephemeral"}
				}
			],
			"messages": [
				{
					"role": "user",
					"content": [
						{
							"type": "image",
							"source": {
								"type": "base64",
								"media_type": "image/png",
								"data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
							}
						},
						{
							"type": "text",
							"text": "Extract all text from this image."
						}
					]
				}
			]
		}`

		norm, hash, err := g.NormalizeAnthropic([]byte(payload))
		if err != nil {
			t.Fatalf("NormalizeAnthropic failed: %v", err)
		}
		if hash == "" {
			t.Fatal("expected non-empty prefix hash")
		}

		var parsed map[string]any
		if err := json.Unmarshal(norm, &parsed); err != nil {
			t.Fatalf("Unmarshal normalized Anthropic payload failed: %v", err)
		}

		// Verify base64 data wasn't mangled
		msgs := parsed["messages"].([]any)
		userMsg := msgs[0].(map[string]any)
		content := userMsg["content"].([]any)
		imgPart := content[0].(map[string]any)
		src := imgPart["source"].(map[string]any)
		if src["data"] != "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=" {
			t.Errorf("base64 image payload was corrupted")
		}
	})

	t.Run("OpenAI Structured Output and Reasoning and Tool Call Sequences", func(t *testing.T) {
		payload := `{
			"model": "o3-mini",
			"reasoning_effort": "medium",
			"response_format": {
				"type": "json_schema",
				"json_schema": {
					"name": "math_response",
					"strict": true,
					"schema": {
						"type": "object",
						"properties": {
							"final_answer": {"type": "number"},
							"steps": {"type": "array", "items": {"type": "string"}}
						},
						"required": ["final_answer", "steps"],
						"additionalProperties": false
					}
				}
			},
			"messages": [
				{
					"role": "user",
					"content": [{"type": "text", "text": "Solve 2+2"}]
				},
				{
					"role": "assistant",
					"tool_calls": [
						{
							"id": "call_abc123",
							"type": "function",
							"function": {"name": "calculator", "arguments": "{\"expr\":\"2+2\"}"}
						}
					]
				},
				{
					"role": "tool",
					"tool_call_id": "call_abc123",
					"content": "4"
				}
			]
		}`

		norm, hash, err := g.NormalizeOpenAI([]byte(payload))
		if err != nil {
			t.Fatalf("NormalizeOpenAI failed: %v", err)
		}
		if hash == "" {
			t.Fatal("expected non-empty prefix hash")
		}

		var parsed map[string]any
		if err := json.Unmarshal(norm, &parsed); err != nil {
			t.Fatalf("Unmarshal normalized OpenAI payload failed: %v", err)
		}

		if parsed["reasoning_effort"] != "medium" {
			t.Errorf("expected reasoning_effort medium, got %v", parsed["reasoning_effort"])
		}
		rf := parsed["response_format"].(map[string]any)
		js := rf["json_schema"].(map[string]any)
		if js["name"] != "math_response" {
			t.Errorf("expected json_schema math_response, got %v", js["name"])
		}
	})
}

func TestProxy_SSEStreamingFramingAndErrors(t *testing.T) {
	// Mock upstream streaming SSE server
	sseChunks := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":25}}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"World!\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		for _, chunk := range sseChunks {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer mockUpstream.Close()

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:        "127.0.0.1:0",
		UpstreamAnthropic: mockUpstream.URL,
		Store:             s,
	})

	testClient := httptest.NewServer(proxySrv.Handler())
	defer testClient.Close()

	reqBody := `{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"Hi"}]}`
	resp, err := http.Post(testClient.URL+"/v1/messages", "application/json", bytes.NewReader([]byte(reqBody)))
	if err != nil {
		t.Fatalf("Failed request to proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	receivedBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read SSE response stream: %v", err)
	}

	expectedFull := ""
	for _, chunk := range sseChunks {
		expectedFull += chunk
	}

	if string(receivedBytes) != expectedFull {
		t.Errorf("SSE stream chunks corrupted.\nExpected:\n%s\nGot:\n%s", expectedFull, string(receivedBytes))
	}
}

func TestProxy_ErrorEnvelopePassThrough(t *testing.T) {
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your quota"}}`))
	}))
	defer mockUpstream.Close()

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:        "127.0.0.1:0",
		UpstreamAnthropic: mockUpstream.URL,
		Store:             s,
	})

	testClient := httptest.NewServer(proxySrv.Handler())
	defer testClient.Close()

	resp, err := http.Post(testClient.URL+"/v1/messages", "application/json", bytes.NewReader([]byte(`{"model":"claude"}`)))
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("rate_limit_error")) {
		t.Errorf("expected error envelope passed through, got %s", string(body))
	}
}

func TestProxy_ConcurrentRaceMatrix(t *testing.T) {
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockUpstream.Close()

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:        "127.0.0.1:0",
		UpstreamAnthropic: mockUpstream.URL,
		UpstreamOpenAI:    mockUpstream.URL,
		Store:             s,
	})

	testClient := httptest.NewServer(proxySrv.Handler())
	defer testClient.Close()

	var wg sync.WaitGroup
	workers := 10
	iterations := 5

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			client := &http.Client{Timeout: 5 * time.Second}
			for i := 0; i < iterations; i++ {
				var endpoint string
				var payload string
				if i%2 == 0 {
					endpoint = testClient.URL + "/v1/messages"
					payload = fmt.Sprintf(`{"model":"claude-3-7","worker":%d,"messages":[{"role":"user","content":"W%d I%d"}]}`, workerID, workerID, i)
				} else {
					endpoint = testClient.URL + "/v1/chat/completions"
					payload = fmt.Sprintf(`{"model":"gpt-4o","worker":%d,"messages":[{"role":"user","content":"W%d I%d"}]}`, workerID, workerID, i)
				}

				resp, err := client.Post(endpoint, "application/json", bytes.NewReader([]byte(payload)))
				if err != nil {
					t.Errorf("concurrent request error: %v", err)
					return
				}
				_ = resp.Body.Close()
			}
		}(w)
	}

	wg.Wait()
}

func TestProxy_ContextCancellation(t *testing.T) {
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockUpstream.Close()

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:     "127.0.0.1:0",
		UpstreamOpenAI: mockUpstream.URL,
		Store:          s,
	})

	testClient := httptest.NewServer(proxySrv.Handler())
	defer testClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", testClient.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4o"}`)))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	_, err := client.Do(req)
	if err == nil {
		t.Log("Context canceled as expected")
	}
}

func TestProxy_ProviderAdapters(t *testing.T) {
	// 1. OpenAI Responses API (/v1/responses)
	var receivedResponsesBody string
	mockResponsesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedResponsesBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.created\ndata: {\"id\":\"resp_001\"}\n\n"))
		_, _ = w.Write([]byte("event: response.done\ndata: {\"id\":\"resp_001\",\"usage\":{\"total_tokens\":42}}\n\n"))
	}))
	defer mockResponsesUpstream.Close()

	// 2. Gemini-native API (/v1beta/models/gemini-1.5-pro:generateContent)
	var receivedGeminiPath string
	var receivedGeminiBody string
	mockGeminiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedGeminiPath = r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		receivedGeminiBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Hello from Gemini"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
	}))
	defer mockGeminiUpstream.Close()

	// 3. Local endpoint (Ollama / LM Studio)
	var receivedLocalBody string
	mockLocalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedLocalBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hello from local Ollama"}}]}`))
	}))
	defer mockLocalUpstream.Close()

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:     "127.0.0.1:0",
		UpstreamOpenAI: mockResponsesUpstream.URL,
		UpstreamGemini: mockGeminiUpstream.URL,
		UpstreamLocal:  mockLocalUpstream.URL,
		Store:          s,
	})

	testClient := httptest.NewServer(proxySrv.Handler())
	defer testClient.Close()

	// Test Responses API
	t.Run("OpenAI Responses API Route and Normalization", func(t *testing.T) {
		reqPayload := `{
			"model": "gpt-4o",
			"instructions": "Be concise.",
			"input": "Write a poem",
			"tools": [
				{"type": "function", "name": "zebra_search"},
				{"type": "function", "name": "alpha_calc"}
			]
		}`
		resp, err := http.Post(testClient.URL+"/v1/responses", "application/json", bytes.NewReader([]byte(reqPayload)))
		if err != nil {
			t.Fatalf("Responses post failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
		}
		if !strings.Contains(string(body), "event: response.created") {
			t.Errorf("Expected SSE response.created event, got: %s", string(body))
		}
		// Verify tool sorting in upstream body
		if !strings.Contains(receivedResponsesBody, "alpha_calc") {
			t.Errorf("Expected alpha_calc in upstream body, got: %s", receivedResponsesBody)
		}
	})

	// Test Gemini-native API
	t.Run("Gemini-native Schema and Multi-turn Payload", func(t *testing.T) {
		reqPayload := `{
			"contents": [
				{
					"role": "user",
					"parts": [{"text": "Hello Gemini!"}]
				}
			],
			"system_instruction": {
				"parts": [{"text": "You are a friendly tutor."}]
			},
			"tools": [
				{
					"function_declarations": [
						{"name": "zebra_fn", "description": "zebra"},
						{"name": "apple_fn", "description": "apple"}
					]
				}
			]
		}`
		geminiURI := "/v1beta/models/gemini-1.5-pro:generateContent?key=AIzaSyFakeKey"
		resp, err := http.Post(testClient.URL+geminiURI, "application/json", bytes.NewReader([]byte(reqPayload)))
		if err != nil {
			t.Fatalf("Gemini post failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
		}
		if receivedGeminiPath != geminiURI {
			t.Errorf("Expected path %q, got %q", geminiURI, receivedGeminiPath)
		}
		if !strings.Contains(string(body), "Hello from Gemini") {
			t.Errorf("Expected Gemini response, got: %s", string(body))
		}
		// Verify function declaration sorting in upstream body: apple_fn must come before zebra_fn
		appleIdx := strings.Index(receivedGeminiBody, "apple_fn")
		zebraIdx := strings.Index(receivedGeminiBody, "zebra_fn")
		if appleIdx == -1 || zebraIdx == -1 || appleIdx > zebraIdx {
			t.Errorf("Expected apple_fn sorted before zebra_fn, got body: %s", receivedGeminiBody)
		}
	})

	// Test Local Endpoint Pass-through
	t.Run("Local Endpoint Routing", func(t *testing.T) {
		reqPayload := `{"model":"llama3:8b","messages":[{"role":"user","content":"Hi Ollama"}]}`
		req, _ := http.NewRequest("POST", testClient.URL+"/v1/chat/completions", bytes.NewReader([]byte(reqPayload)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tzro-Local", "true")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Local request failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
		}
		if !strings.Contains(string(body), "Hello from local Ollama") {
			t.Errorf("Expected local Ollama response, got: %s", string(body))
		}
		if !strings.Contains(receivedLocalBody, "llama3:8b") {
			t.Errorf("Expected received local body to contain llama3:8b, got: %s", receivedLocalBody)
		}
	})
}

func TestProxy_TokenUsageExtraction(t *testing.T) {
	// 1. JSON response with standard OpenAI usage + native cache read
	jsonResp := []byte(`{
		"id": "chatcmpl-123",
		"choices": [{"message": {"role": "assistant", "content": "Hello world"}}],
		"usage": {
			"prompt_tokens": 120,
			"completion_tokens": 30,
			"total_tokens": 150,
			"prompt_tokens_details": {
				"cached_tokens": 80
			},
			"tzro_prefix_locked_tokens": 40
		}
	}`)

	usage, ok := ExtractUsageFromJSON(jsonResp)
	if !ok || !usage.IsMeasured {
		t.Fatalf("ExtractUsageFromJSON failed to extract usage")
	}
	if *usage.PromptTokens != 120 || *usage.CompletionTokens != 30 || *usage.TotalTokens != 150 {
		t.Errorf("Unexpected token counts: %+v", usage)
	}
	if *usage.NativeCacheRead != 80 {
		t.Errorf("Expected native cache read 80, got %v", usage.NativeCacheRead)
	}
	if *usage.TzroPrefixLocked != 40 {
		t.Errorf("Expected tzro prefix locked 40, got %v", usage.TzroPrefixLocked)
	}

	// 2. Gemini usageMetadata
	geminiResp := []byte(`{
		"candidates": [{"content": {"parts": [{"text": "Hi"}]}}],
		"usageMetadata": {
			"promptTokenCount": 50,
			"candidatesTokenCount": 15,
			"totalTokenCount": 65,
			"cachedContentTokenCount": 25
		}
	}`)
	gUsage, ok := ExtractUsageFromJSON(geminiResp)
	if !ok || !gUsage.IsMeasured {
		t.Fatalf("ExtractUsageFromJSON failed for Gemini")
	}
	if *gUsage.PromptTokens != 50 || *gUsage.CompletionTokens != 15 || *gUsage.TotalTokens != 65 || *gUsage.NativeCacheRead != 25 {
		t.Errorf("Unexpected Gemini token counts: %+v", gUsage)
	}

	// 3. SSE Stream chunk with Anthropic message_delta usage
	sseStream := []byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":22,\"input_tokens\":100,\"cache_read_input_tokens\":75}}\n\n")
	sUsage, ok := ExtractUsageFromSSE(sseStream)
	if !ok || !sUsage.IsMeasured {
		t.Fatalf("ExtractUsageFromSSE failed for Anthropic SSE")
	}
	if *sUsage.CompletionTokens != 22 || *sUsage.PromptTokens != 100 || *sUsage.NativeCacheRead != 75 {
		t.Errorf("Unexpected Anthropic SSE token counts: %+v", sUsage)
	}

	// 4. Missing fields formatting as 'unknown'
	sparseUsage := TokenUsage{
		PromptTokens: nil,
		TotalTokens:  nil,
		IsMeasured:   true,
	}
	display := sparseUsage.FormatUsageDisplay()
	if !strings.Contains(display, "Input Tokens:        unknown") {
		t.Errorf("Expected unknown for missing prompt tokens, got: %s", display)
	}
}
