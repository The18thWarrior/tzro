package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// CallCloudModel executes standard remote API calls for cloud planning and fallback
func CallCloudModel(ctx context.Context, systemPrompt, userPrompt string, schemaStr string) (string, error) {
	return callCloudModel(ctx, systemPrompt, userPrompt, schemaStr)
}

func callCloudModel(ctx context.Context, systemPrompt, userPrompt string, schemaStr string) (string, error) {
	if schemaStr != "" {
		systemPrompt = fmt.Sprintf("%s\n\nYou MUST return a JSON object that strictly adheres to the following JSON schema:\n%s", systemPrompt, schemaStr)
	}

	cfg := config.Get()
	cloudKey := config.GetCloudAPIKey()
	if cloudKey == "" {
		return "", fmt.Errorf("Cloud API key is missing. Please provide a valid Cloud API Key in configurations.")
	}

	var url string
	modelName := config.GetCloudModel()

	switch cfg.CloudProvider {
	case "google":
		url = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	case "openai":
		url = "https://api.openai.com/v1/chat/completions"
	default:
		return "", fmt.Errorf("unsupported cloud provider '%s'; please configure 'google' or 'openai'", cfg.CloudProvider)
	}

	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type ResponseFormatStruct struct {
		Type   string                 `json:"type"`
		Schema map[string]interface{} `json:"schema,omitempty"`
	}
	type CompletionRequest struct {
		Model          string                `json:"model"`
		Messages       []Message             `json:"messages"`
		Temperature    float64               `json:"temperature"`
		ResponseFormat *ResponseFormatStruct `json:"response_format,omitempty"`
	}

	reqBody := CompletionRequest{
		Model: modelName,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1,
	}

	if schemaStr != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schemaObj) == nil {
			reqBody.ResponseFormat = &ResponseFormatStruct{
				Type: "json_object",
			}
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.GetCloudAPIKey())

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("cloud API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) > 0 {
		if tracker, ok := GetTokenTracker(ctx); ok {
			tracker.Record(true, result.Usage.PromptTokens, result.Usage.CompletionTokens)
		}
		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("empty choice array returned from cloud provider")
}

// CallCloudModelStream executes standard remote API calls with SSE streaming for cloud planning and fallback
func CallCloudModelStream(ctx context.Context, systemPrompt, userPrompt string, schemaStr string, meta StreamMeta, pub telemetry.EventPublisher) (string, error) {
	if pub == nil {
		pub = telemetry.Default
	}
	if schemaStr != "" {
		systemPrompt = fmt.Sprintf("%s\n\nYou MUST return a JSON object that strictly adheres to the following JSON schema:\n%s", systemPrompt, schemaStr)
	}

	cfg := config.Get()
	cloudKey := config.GetCloudAPIKey()
	if cloudKey == "" {
		return "", fmt.Errorf("Cloud API key is missing. Please provide a valid Cloud API Key in configurations.")
	}

	var url string
	modelName := config.GetCloudModel()

	switch cfg.CloudProvider {
	case "google":
		url = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	case "openai":
		url = "https://api.openai.com/v1/chat/completions"
	default:
		return "", fmt.Errorf("unsupported cloud provider '%s'; please configure 'google' or 'openai'", cfg.CloudProvider)
	}

	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type StreamOptionsStruct struct {
		IncludeUsage bool `json:"include_usage"`
	}

	type ResponseFormatStruct struct {
		Type   string                 `json:"type"`
		Schema map[string]interface{} `json:"schema,omitempty"`
	}

	type CompletionRequest struct {
		Model          string                `json:"model"`
		Messages       []Message             `json:"messages"`
		Temperature    float64               `json:"temperature"`
		Stream         bool                  `json:"stream"`
		StreamOptions  *StreamOptionsStruct  `json:"stream_options,omitempty"`
		ResponseFormat *ResponseFormatStruct `json:"response_format,omitempty"`
	}

	reqBody := CompletionRequest{
		Model: modelName,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1,
		Stream:      true,
		StreamOptions: &StreamOptionsStruct{
			IncludeUsage: true,
		},
	}

	if schemaStr != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schemaObj) == nil {
			reqBody.ResponseFormat = &ResponseFormatStruct{
				Type: "json_object",
			}
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.GetCloudAPIKey())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("cloud stream API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	reader := bufio.NewReader(resp.Body)
	var accumulatedContent strings.Builder
	promptTokens := 0
	completionTokens := 0

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("failed to read cloud stream line: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data: ")
		if dataStr == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			contentDelta := chunk.Choices[0].Delta.Content
			if contentDelta != "" {
				accumulatedContent.WriteString(contentDelta)
				pub.PublishStream(stream.StreamChunk{
					StreamID: meta.StreamID,
					Source:   meta.Source,
					TaskID:   meta.TaskID,
					NodeID:   meta.NodeID,
					Type:     "token",
					Content:  contentDelta,
				})
			}
		}

		if chunk.Usage != nil {
			promptTokens = chunk.Usage.PromptTokens
			completionTokens = chunk.Usage.CompletionTokens
		}
	}

	if tracker, ok := GetTokenTracker(ctx); ok {
		tracker.Record(true, promptTokens, completionTokens)
	}

	resContent := accumulatedContent.String()

	pub.PublishStream(stream.StreamChunk{
		StreamID: meta.StreamID,
		Source:   meta.Source,
		TaskID:   meta.TaskID,
		NodeID:   meta.NodeID,
		Type:     "done",
		Content:  resContent,
		Usage: stream.UsageInfo{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
		},
	})

	return resContent, nil
}
