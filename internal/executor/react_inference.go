package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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
			Timeout: 180 * time.Second,
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
	// Determine endpoint URL
	url := ""
	apiKey := ""

	if inference.GlobalWorkerModel != nil && inference.GlobalWorkerModel.ActivePort > 0 {
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
		Model:       "local-model",
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

	return &ReActResponse{
		Content:          choice.Message.Content,
		ToolCalls:        parsedToolCalls,
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
	}, nil
}
