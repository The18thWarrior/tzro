package comparison

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// mockCloudServer creates a test server that simulates OpenAI-compatible chat completions.
// responseSequence is a list of responses to return in order for each request.
func mockCloudServer(t *testing.T, responseSequence []reactCompletionResponse) *httptest.Server {
	t.Helper()
	var callIdx int64

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt64(&callIdx, 1) - 1)
		if idx >= len(responseSequence) {
			t.Errorf("unexpected call #%d beyond response sequence length %d", idx, len(responseSequence))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responseSequence[idx])
	}))
}

func TestRunReAct_SingleToolCallRoundTrip(t *testing.T) {
	// Create a temp file for the tool to read
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "hello.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Hello() string {\n\treturn \"world\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to tmpDir so PathValidator allows reads
	origDir, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origDir) }()

	finalContent := "# Function Index\n\n- `Hello() string` — returns \"world\""

	// Mock server: first call returns tool_call for read_file, second returns final text
	responses := []reactCompletionResponse{
		{
			Choices: []struct {
				Message struct {
					Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
					ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content          *string         `json:"content"`
						ReasoningContent *string         `json:"reasoning_content,omitempty"`
						ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
					}{
						ToolCalls: []reactToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "read_file",
									Arguments: fmt.Sprintf(`{"path": "%s"}`, testFile),
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
			Usage: struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			}{PromptTokens: 100, CompletionTokens: 20},
		},
		{
			Choices: []struct {
				Message struct {
					Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
					ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content          *string         `json:"content"`
						ReasoningContent *string         `json:"reasoning_content,omitempty"`
						ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
					}{
						Content: &finalContent,
					},
					FinishReason: "stop",
				},
			},
			Usage: struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			}{PromptTokens: 200, CompletionTokens: 50},
		},
	}

	server := mockCloudServer(t, responses)
	defer server.Close()

	task := ComparisonTask{
		ID:     "test_task",
		Tier:   1,
		Prompt: "Generate a function index for hello.go",
	}

	result, err := RunReActWithEndpoint(t.Context(), task, DefaultPricing(), server.URL)
	if err != nil {
		t.Fatalf("RunReAct failed: %v", err)
	}

	if result.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", result.ToolCallCount)
	}
	if result.OutputText != finalContent {
		t.Errorf("OutputText = %q, want %q", result.OutputText, finalContent)
	}
	if result.CloudTokens.PromptTokens == 0 {
		t.Error("CloudTokens.PromptTokens should be non-zero")
	}
	if result.CloudTokens.CompletionTokens == 0 {
		t.Error("CloudTokens.CompletionTokens should be non-zero")
	}
	if result.Condition != ConditionCloudReAct {
		t.Errorf("Condition = %q, want %q", result.Condition, ConditionCloudReAct)
	}
	if result.EstCostUSD <= 0 {
		t.Error("EstCostUSD should be positive")
	}
}

func TestRunReAct_TerminatesAtMaxIterations(t *testing.T) {
	// Create a temp dir with a file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "dummy.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origDir) }()

	// Mock server: always returns tool_call, never final text
	infiniteResponse := reactCompletionResponse{
		Choices: []struct {
			Message struct {
				Content          *string         `json:"content"`
				ReasoningContent *string         `json:"reasoning_content,omitempty"`
				ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Message: struct {
					Content          *string         `json:"content"`
					ReasoningContent *string         `json:"reasoning_content,omitempty"`
					ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
				}{
					ToolCalls: []reactToolCall{
						{
							ID:   "call_loop",
							Type: "function",
							Function: struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							}{
								Name:      "read_file",
								Arguments: fmt.Sprintf(`{"path": "%s"}`, testFile),
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		}{PromptTokens: 50, CompletionTokens: 10},
	}

	// Build 50 identical responses (one per iteration)
	responses := make([]reactCompletionResponse, maxReActIterations)
	for i := range responses {
		responses[i] = infiniteResponse
	}

	server := mockCloudServer(t, responses)
	defer server.Close()

	task := ComparisonTask{
		ID:     "loop_task",
		Tier:   1,
		Prompt: "Generate docs",
	}

	result, err := RunReActWithEndpoint(t.Context(), task, DefaultPricing(), server.URL)
	// RunReAct returns a result (not an error) when hitting max iterations
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ToolCallCount != maxReActIterations {
		t.Errorf("ToolCallCount = %d, want %d", result.ToolCallCount, maxReActIterations)
	}
	if result.Error == "" {
		t.Error("Error should be set when max iterations reached")
	}
}

func TestRunReAct_EchoesThoughtSignature(t *testing.T) {
	// This test verifies that extra_content (carrying thought_signature from
	// Gemini thinking models) is echoed back verbatim in subsequent requests.
	// Without this, the Gemini API returns 400 INVALID_ARGUMENT.

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origDir) }()

	// The opaque thought_signature blob that Gemini returns
	thoughtSig := json.RawMessage(`{"google":{"thought_signature":"EpoGCpcGAXLI2nx_TEST_SIG"}}`)

	finalContent := "# Docs\nDone."

	var capturedSecondRequest reactCompletionRequest
	var callIdx int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt64(&callIdx, 1) - 1)

		// Capture the second request to verify extra_content was echoed back
		if idx == 1 {
			if err := json.NewDecoder(r.Body).Decode(&capturedSecondRequest); err != nil {
				t.Errorf("failed to decode second request: %v", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")

		switch idx {
		case 0:
			// First response: tool call with extra_content containing thought_signature
			resp := reactCompletionResponse{
				Choices: []struct {
					Message struct {
						Content          *string         `json:"content"`
						ReasoningContent *string         `json:"reasoning_content,omitempty"`
						ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{
					{
						Message: struct {
							Content          *string         `json:"content"`
							ReasoningContent *string         `json:"reasoning_content,omitempty"`
							ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
						}{
							ToolCalls: []reactToolCall{
								{
									ID:   "call_sig",
									Type: "function",
									Function: struct {
										Name      string `json:"name"`
										Arguments string `json:"arguments"`
									}{
										Name:      "read_file",
										Arguments: fmt.Sprintf(`{"path": "%s"}`, testFile),
									},
									ExtraContent: thoughtSig,
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
				Usage: struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				}{PromptTokens: 100, CompletionTokens: 20},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case 1:
			// Second response: final text
			resp := reactCompletionResponse{
				Choices: []struct {
					Message struct {
						Content          *string         `json:"content"`
						ReasoningContent *string         `json:"reasoning_content,omitempty"`
						ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{
					{
						Message: struct {
							Content          *string         `json:"content"`
							ReasoningContent *string         `json:"reasoning_content,omitempty"`
							ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
						}{
							Content: &finalContent,
						},
						FinishReason: "stop",
					},
				},
				Usage: struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				}{PromptTokens: 200, CompletionTokens: 50},
			}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			t.Errorf("unexpected call #%d", idx)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	task := ComparisonTask{
		ID:     "sig_test",
		Tier:   1,
		Prompt: "Generate docs for main.go",
	}

	result, err := RunReActWithEndpoint(t.Context(), task, DefaultPricing(), server.URL)
	if err != nil {
		t.Fatalf("RunReAct failed: %v", err)
	}
	if result.OutputText != finalContent {
		t.Errorf("OutputText = %q, want %q", result.OutputText, finalContent)
	}

	// Verify the second request contains the assistant message with extra_content echoed back
	if len(capturedSecondRequest.Messages) == 0 {
		t.Fatal("second request was not captured")
	}

	// Find the assistant message with tool_calls in the second request
	var foundExtraContent bool
	for _, msg := range capturedSecondRequest.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ExtraContent != nil {
					foundExtraContent = true
					// Verify the signature is present verbatim
					var ec map[string]interface{}
					if err := json.Unmarshal(tc.ExtraContent, &ec); err != nil {
						t.Fatalf("failed to unmarshal echoed extra_content: %v", err)
					}
					google, ok := ec["google"].(map[string]interface{})
					if !ok {
						t.Fatal("extra_content missing 'google' key")
					}
					sig, ok := google["thought_signature"].(string)
					if !ok || sig != "EpoGCpcGAXLI2nx_TEST_SIG" {
						t.Errorf("thought_signature = %q, want %q", sig, "EpoGCpcGAXLI2nx_TEST_SIG")
					}
				}
			}
		}
	}

	if !foundExtraContent {
		t.Error("extra_content with thought_signature was not echoed back in the second request — Gemini thinking models will return 400")
	}
}
