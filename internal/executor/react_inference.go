package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
)

// LiveReActInference connects to the active local sidecar or remote endpoint.
type LiveReActInference struct {
	client *http.Client
}

// NewLiveReActInference creates a LiveReActInference instance.
func NewLiveReActInference() *LiveReActInference {
	return &LiveReActInference{
		client: &http.Client{
			Timeout: 300 * time.Second,
		},
	}
}

// openAIChatMessage matches the wire format expected by /v1/chat/completions.
type openAIChatMessage struct {
	Role       string                   `json:"role"`
	Content    string                   `json:"content,omitempty"`
	ToolCalls  []openAIToolCall         `json:"tool_calls,omitempty"`
	ToolCallID string                   `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIFunctionCall     `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Tools       []ReActToolDef      `json:"tools,omitempty"`
	Temperature float64             `json:"temperature"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (l *LiveReActInference) Call(ctx context.Context, messages []ReActMessage, tools []ReActToolDef) (*ReActResponse, error) {
	// Determine endpoint URL, API key, and model name
	url := ""
	apiKey := ""
	modelName := "local-model"

	cfg := config.Get()
	if cfg.InferenceBackend.Type == "openai-compatible" && cfg.InferenceBackend.URL != "" {
		url = cfg.InferenceBackend.URL
		if !strings.HasSuffix(url, "/chat/completions") {
			trimmed := strings.TrimSuffix(url, "/")
			if strings.Contains(trimmed, "/v1") || strings.Contains(trimmed, "/v2") {
				url = trimmed + "/chat/completions"
			} else {
				url = trimmed + "/v1/chat/completions"
			}
		}
		apiKey = cfg.InferenceBackend.APIKey
		if strings.HasPrefix(apiKey, "$") {
			apiKey = os.Getenv(strings.TrimPrefix(apiKey, "$"))
		}
		if cfg.InferenceBackend.Model != "" {
			modelName = cfg.InferenceBackend.Model
		}
	} else if inference.GlobalWorkerModel != nil && inference.GlobalWorkerModel.ActivePort > 0 {
		url = fmt.Sprintf("http://localhost:%d/v1/chat/completions", inference.GlobalWorkerModel.ActivePort)
	} else if inference.GlobalRouterModel != nil && inference.GlobalRouterModel.ActivePort > 0 {
		url = fmt.Sprintf("http://localhost:%d/v1/chat/completions", inference.GlobalRouterModel.ActivePort)
	} else {
		url = "http://localhost:8080/v1/chat/completions"
	}

	// Prepare wire messages
	var wireMsgs []openAIChatMessage
	for _, m := range messages {
		msg := openAIChatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openAIFunctionCall{
					Name:      tc.Name,
					Arguments: string(argsJSON),
				},
			})
		}
		wireMsgs = append(wireMsgs, msg)
	}

	reqBody := openAIChatRequest{
		Model:       modelName,
		Messages:    wireMsgs,
		Tools:       tools,
		Temperature: 0.2, // Low temperature for deterministic exploration
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call to %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inference server returned status %d from %s: %s", resp.StatusCode, url, string(respBytes))
	}

	var chatResp openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode chat response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("inference server returned 0 choices")
	}

	choice := chatResp.Choices[0]
	var parsedToolCalls []ReActToolCall
	for _, tc := range choice.Message.ToolCalls {
		var args map[string]interface{}
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				// Wrap as raw string parameter if not a valid JSON object
				args = map[string]interface{}{"raw_input": tc.Function.Arguments}
			}
		}
		parsedToolCalls = append(parsedToolCalls, ReActToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	// Fallback: if no native tool_calls but content contains tool call patterns,
	// parse them from text. Common with OpenAI-compatible backends (LM Studio,
	// vLLM, Ollama) that don't wire the tool_calls response field correctly.
	content := choice.Message.Content
	if len(parsedToolCalls) == 0 && content != "" {
		extracted := parseToolCallsFromContent(content, tools)
		if len(extracted) > 0 {
			parsedToolCalls = extracted
			// Strip the tool call text from content so it doesn't pollute
			// the conversation history as a "final answer"
			content = stripToolCallText(content)
			fmt.Fprintf(os.Stderr, "[ReAct] Extracted %d tool call(s) from content text (backend lacks native tool_calls support)\n", len(extracted))
		}
	}

	return &ReActResponse{
		Content:          content,
		ToolCalls:        parsedToolCalls,
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
	}, nil
}

// parseToolCallsFromContent extracts tool calls from model text output.
// Handles three common patterns that models produce when the backend doesn't
// support native function calling:
//
//  1. Qwen-style XML:     <tool_call>{"name":"web_search","arguments":{...}}</tool_call>
//  2. Raw JSON object:    {"name":"web_search","arguments":{...}}
//  3. Markdown code block: ```json\n{"name":"web_search",...}\n```
//
// Only extracts calls for tools that are in the provided allowed tools list
// to avoid executing hallucinated tool names.
func parseToolCallsFromContent(content string, allowedTools []ReActToolDef) []ReActToolCall {
	// Build lookup set of allowed tool names
	allowed := make(map[string]bool, len(allowedTools))
	for _, t := range allowedTools {
		allowed[t.Function.Name] = true
	}

	var results []ReActToolCall
	callID := 0

	// Pattern 1: Qwen-style <tool_call>...</tool_call> XML tags
	for _, block := range extractBetweenTags(content, "<tool_call>", "</tool_call>") {
		if tc, ok := tryParseToolCallJSON(block, allowed); ok {
			tc.ID = fmt.Sprintf("parsed_%d", callID)
			callID++
			results = append(results, tc)
		}
	}
	if len(results) > 0 {
		return results
	}

	// Pattern 2: Markdown code blocks (```json ... ```)
	for _, block := range extractCodeBlocks(content) {
		if tc, ok := tryParseToolCallJSON(block, allowed); ok {
			tc.ID = fmt.Sprintf("parsed_%d", callID)
			callID++
			results = append(results, tc)
		}
	}
	if len(results) > 0 {
		return results
	}

	// Pattern 3: Raw JSON objects in the content text
	for _, block := range extractJSONObjects(content) {
		if tc, ok := tryParseToolCallJSON(block, allowed); ok {
			tc.ID = fmt.Sprintf("parsed_%d", callID)
			callID++
			results = append(results, tc)
		}
	}

	return results
}

// tryParseToolCallJSON attempts to parse a JSON string as a tool call.
// Accepts two shapes:
//   - {"name": "...", "arguments": {...}}
//   - {"name": "...", "args": {...}}           (common variant)
func tryParseToolCallJSON(jsonStr string, allowed map[string]bool) (ReActToolCall, bool) {
	jsonStr = strings.TrimSpace(jsonStr)

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return ReActToolCall{}, false
	}

	name, _ := raw["name"].(string)
	if name == "" {
		return ReActToolCall{}, false
	}
	if !allowed[name] {
		return ReActToolCall{}, false
	}

	// Accept both "arguments" and "args"
	var args map[string]interface{}
	if a, ok := raw["arguments"].(map[string]interface{}); ok {
		args = a
	} else if a, ok := raw["args"].(map[string]interface{}); ok {
		args = a
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	return ReActToolCall{
		Name:      name,
		Arguments: args,
	}, true
}

// extractBetweenTags returns all substrings between open and close tag pairs.
func extractBetweenTags(s, open, close string) []string {
	var results []string
	remaining := s
	for {
		start := strings.Index(remaining, open)
		if start < 0 {
			break
		}
		after := remaining[start+len(open):]
		end := strings.Index(after, close)
		if end < 0 {
			break
		}
		results = append(results, strings.TrimSpace(after[:end]))
		remaining = after[end+len(close):]
	}
	return results
}

// extractCodeBlocks returns contents of ```json ... ``` or ``` ... ``` blocks.
func extractCodeBlocks(s string) []string {
	var results []string
	remaining := s
	for {
		// Find opening fence
		idx := strings.Index(remaining, "```")
		if idx < 0 {
			break
		}
		after := remaining[idx+3:]
		// Skip optional language tag (e.g., "json")
		nlIdx := strings.Index(after, "\n")
		if nlIdx < 0 {
			break
		}
		blockStart := after[nlIdx+1:]
		// Find closing fence
		endIdx := strings.Index(blockStart, "```")
		if endIdx < 0 {
			break
		}
		results = append(results, strings.TrimSpace(blockStart[:endIdx]))
		remaining = blockStart[endIdx+3:]
	}
	return results
}

// extractJSONObjects finds top-level JSON objects ({...}) in a string.
// Uses brace counting to handle nested objects.
func extractJSONObjects(s string) []string {
	var results []string
	i := 0
	for i < len(s) {
		if s[i] != '{' {
			i++
			continue
		}
		depth := 0
		inString := false
		escaped := false
		j := i
		for j < len(s) {
			ch := s[j]
			if escaped {
				escaped = false
				j++
				continue
			}
			if ch == '\\' && inString {
				escaped = true
				j++
				continue
			}
			if ch == '"' {
				inString = !inString
			} else if !inString {
				if ch == '{' {
					depth++
				} else if ch == '}' {
					depth--
					if depth == 0 {
						candidate := s[i : j+1]
						// Quick sanity check: must contain "name"
						if strings.Contains(candidate, `"name"`) {
							results = append(results, candidate)
						}
						i = j + 1
						break
					}
				}
			}
			j++
		}
		if depth != 0 {
			// Unbalanced braces — skip
			i = j + 1
		}
	}
	return results
}

// stripToolCallText removes tool-call patterns from content so the remaining
// text can be preserved as the model's reasoning/thought output.
func stripToolCallText(content string) string {
	// Remove <tool_call>...</tool_call> blocks
	for {
		start := strings.Index(content, "<tool_call>")
		if start < 0 {
			break
		}
		end := strings.Index(content[start:], "</tool_call>")
		if end < 0 {
			break
		}
		content = content[:start] + content[start+end+len("</tool_call>"):]
	}

	// Don't strip raw JSON or code blocks — too aggressive, might remove
	// legitimate content. The tool_call XML tag is unambiguous.
	return strings.TrimSpace(content)
}
