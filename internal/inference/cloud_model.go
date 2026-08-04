package inference

import (
	"bufio"
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
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// CallCloudModel executes standard remote API calls for cloud planning and fallback
func CallCloudModel(ctx context.Context, messages []InferenceMessage, schemaStr string) (string, error) {
	if config.Get().PrivacyLevel == "strict-local" {
		return "", fmt.Errorf("cloud execution disabled under strict-local privacy level")
	}
	return callCloudModel(ctx, messages, schemaStr)
}

func callCloudModel(ctx context.Context, messages []InferenceMessage, schemaStr string) (string, error) {
	systemPrompt := GetSystemPrompt(messages)
	if schemaStr != "" {
		// Inject schema instruction into the system prompt for cloud models
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
		Role    string      `json:"role"`
		Content interface{} `json:"content"` // string or []ContentPart
	}
	type JSONSchemaWrapper struct {
		Name   string                 `json:"name"`
		Schema map[string]interface{} `json:"schema"`
	}
	type ResponseFormatStruct struct {
		Type       string             `json:"type"`
		JSONSchema *JSONSchemaWrapper `json:"json_schema,omitempty"`
	}
	type CompletionRequest struct {
		Model          string                `json:"model"`
		Messages       []Message             `json:"messages"`
		Temperature    float64               `json:"temperature"`
		ResponseFormat *ResponseFormatStruct `json:"response_format,omitempty"`
	}

	// Rebuild messages with the potentially-modified system prompt
	var cloudMessages []Message
	for _, m := range messages {
		if m.Role == "system" {
			cloudMessages = append(cloudMessages, Message{Role: "system", Content: systemPrompt})
		} else if m.HasMultimodalContent() {
			cloudMessages = append(cloudMessages, Message{Role: m.Role, Content: m.Parts})
		} else {
			cloudMessages = append(cloudMessages, Message{Role: m.Role, Content: m.Content})
		}
	}

	reqBody := CompletionRequest{
		Model:       modelName,
		Messages:    cloudMessages,
		Temperature: 0.1,
	}

	if schemaStr != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schemaObj) == nil {
			reqBody.ResponseFormat = &ResponseFormatStruct{
				Type: "json_schema",
				JSONSchema: &JSONSchemaWrapper{
					Name:   "response",
					Schema: schemaObj,
				},
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

	client := &http.Client{Timeout: 60 * time.Second}
	startTime := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[Cloud API Error] status=%d model=%s bodySize=%d messages=%d\n",
			resp.StatusCode, modelName, len(bodyBytes), len(cloudMessages))
		for i, m := range cloudMessages {
			cStr, _ := json.Marshal(m.Content)
			fmt.Fprintf(os.Stderr, "  msg[%d] role=%s size=%d\n", i, m.Role, len(cStr))
		}
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
		duration := time.Since(startTime).Seconds()
		speed := 0.0
		if duration > 0 && result.Usage.CompletionTokens > 0 {
			speed = float64(result.Usage.CompletionTokens) / duration
		}
		if tracker, ok := GetTokenTracker(ctx); ok {
			tracker.Record(true, result.Usage.PromptTokens, result.Usage.CompletionTokens, duration, speed)
		}
		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("empty choice array returned from cloud provider")
}

// CallCloudModelStream executes standard remote API calls with SSE streaming for cloud planning and fallback
func CallCloudModelStream(ctx context.Context, messages []InferenceMessage, schemaStr string, meta StreamMeta, pub telemetry.EventPublisher) (string, error) {
	if config.Get().PrivacyLevel == "strict-local" {
		return "", fmt.Errorf("cloud execution disabled under strict-local privacy level")
	}
	if pub == nil {
		pub = telemetry.Default
	}
	systemPrompt := GetSystemPrompt(messages)
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
		Role    string      `json:"role"`
		Content interface{} `json:"content"` // string or []ContentPart
	}

	type StreamOptionsStruct struct {
		IncludeUsage bool `json:"include_usage"`
	}

	type JSONSchemaWrapper struct {
		Name   string                 `json:"name"`
		Schema map[string]interface{} `json:"schema"`
	}
	type ResponseFormatStruct struct {
		Type       string             `json:"type"`
		JSONSchema *JSONSchemaWrapper `json:"json_schema,omitempty"`
	}

	type CompletionRequest struct {
		Model          string                `json:"model"`
		Messages       []Message             `json:"messages"`
		Temperature    float64               `json:"temperature"`
		Stream         bool                  `json:"stream"`
		StreamOptions  *StreamOptionsStruct  `json:"stream_options,omitempty"`
		ResponseFormat *ResponseFormatStruct `json:"response_format,omitempty"`
	}

	// Rebuild messages with the potentially-modified system prompt
	var cloudMessages []Message
	for _, m := range messages {
		if m.Role == "system" {
			cloudMessages = append(cloudMessages, Message{Role: "system", Content: systemPrompt})
		} else if m.HasMultimodalContent() {
			cloudMessages = append(cloudMessages, Message{Role: m.Role, Content: m.Parts})
		} else {
			cloudMessages = append(cloudMessages, Message{Role: m.Role, Content: m.Content})
		}
	}

	reqBody := CompletionRequest{
		Model:       modelName,
		Messages:    cloudMessages,
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
				Type: "json_schema",
				JSONSchema: &JSONSchemaWrapper{
					Name:   "response",
					Schema: schemaObj,
				},
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
	startTime := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[Cloud Stream API Error] status=%d model=%s bodySize=%d messages=%d\n",
			resp.StatusCode, modelName, len(bodyBytes), len(cloudMessages))
		for i, m := range cloudMessages {
			cStr, _ := json.Marshal(m.Content)
			fmt.Fprintf(os.Stderr, "  msg[%d] role=%s size=%d\n", i, m.Role, len(cStr))
		}
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

	duration := time.Since(startTime).Seconds()
	speed := 0.0
	if duration > 0 && completionTokens > 0 {
		speed = float64(completionTokens) / duration
	}

	if tracker, ok := GetTokenTracker(ctx); ok {
		tracker.Record(true, promptTokens, completionTokens, duration, speed)
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
