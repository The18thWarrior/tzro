package comparison

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// mockCloudServer creates a test server that simulates OpenAI-compatible chat completions with SSE streaming.
// responseSequence is a list of responses to return in order for each request.
func mockCloudServer(t *testing.T, responseSequence []reactCompletionResponse) *httptest.Server {
	t.Helper()
	var callIdx int64

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt64(&callIdx, 1) - 1)
		if idx >= len(responseSequence) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		resp := responseSequence[idx]
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		for _, choice := range resp.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				type toolCallDelta struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				}
				var tcs []toolCallDelta
				for i, tc := range choice.Message.ToolCalls {
					tcs = append(tcs, toolCallDelta{
						Index:    i,
						ID:       tc.ID,
						Type:     tc.Type,
						Function: tc.Function,
					})
				}
				chunk := map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"delta": map[string]interface{}{
								"role":       "assistant",
								"tool_calls": tcs,
							},
							"finish_reason": choice.FinishReason,
						},
					},
					"usage": resp.Usage,
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
			} else if choice.Message.Content != nil {
				chunk := map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"delta": map[string]interface{}{
								"role":    "assistant",
								"content": *choice.Message.Content,
							},
							"finish_reason": choice.FinishReason,
						},
					},
					"usage": resp.Usage,
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
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

func TestRunReAct_ToolsExtension(t *testing.T) {
	ext := generateToolsExtension()
	requiredTools := []string{"read_file", "list_dir", "search_files", "write_file", "web_search", "web_browse"}
	for _, tool := range requiredTools {
		if !strings.Contains(ext, fmt.Sprintf(`name: %q`, tool)) {
			t.Errorf("generateToolsExtension() missing tool %q", tool)
		}
	}
}
