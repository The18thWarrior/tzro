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

// RemoteOpenAIBackend targets an arbitrary OpenAI-compatible model endpoint.
type RemoteOpenAIBackend struct {
	url       string
	model     string
	apiKey    string
	client    *http.Client
	publisher telemetry.EventPublisher
}

// NewRemoteOpenAIBackend creates a new RemoteOpenAIBackend.
func NewRemoteOpenAIBackend(cfg config.BackendConfig, publisher telemetry.EventPublisher) *RemoteOpenAIBackend {
	// Clean and resolve API key
	apiKey := cfg.APIKey
	if strings.HasPrefix(apiKey, "$") {
		apiKey = os.Getenv(strings.TrimPrefix(apiKey, "$"))
	}

	url := cfg.URL
	if url == "" {
		url = "http://localhost:11434/v1" // Fallback to Ollama default
	}
	if !strings.HasSuffix(url, "/chat/completions") {
		url = strings.TrimSuffix(url, "/") + "/chat/completions"
	}

	model := cfg.Model
	if model == "" {
		model = "qwen3.5:latest"
	}

	return &RemoteOpenAIBackend{
		url:       url,
		model:     model,
		apiKey:    apiKey,
		client:    &http.Client{}, // No fixed timeout for inference calls (relies on ctx)
		publisher: publisher,
	}
}

// CallModel performs a structured JSON inference call.
func (b *RemoteOpenAIBackend) CallModel(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error) {
	type CompletionRequest struct {
		Model          string                 `json:"model"`
		Messages       []map[string]string    `json:"messages"`
		Temperature    float64                `json:"temperature"`
		ResponseFormat map[string]interface{} `json:"response_format,omitempty"`
	}

	reqBody := CompletionRequest{
		Model:       b.model,
		Messages:    MessagesToMaps(messages),
		Temperature: 1.0,
	}

	if jsonSchema != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(jsonSchema), &schemaObj) == nil {
			reqBody.ResponseFormat = map[string]interface{}{
				"type":   "json_object",
				"schema": schemaObj,
			}
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}

	startTime := time.Now()
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote model HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote model HTTP server returned status %s: %s", resp.Status, string(respBody))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode remote model HTTP response JSON: %w", err)
	}

	duration := time.Since(startTime).Seconds()
	promptTokens := 0
	completionTokens := 0
	if usage, ok := result["usage"].(map[string]interface{}); ok {
		if pt, ok := usage["prompt_tokens"].(float64); ok {
			promptTokens = int(pt)
		}
		if ct, ok := usage["completion_tokens"].(float64); ok {
			completionTokens = int(ct)
		}
	}

	speed := 0.0
	if duration > 0 && completionTokens > 0 {
		speed = float64(completionTokens) / duration
	}

	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					if tracker, ok := GetTokenTracker(ctx); ok {
						tracker.Record(false, promptTokens, completionTokens)
					}
					res := &InferenceResult{
						Content:          content,
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						DurationSeconds:  duration,
						TokensPerSecond:  speed,
					}
					b.getPublisher().PublishStream(stream.StreamChunk{
						Source:  "classifier",
						Type:    "done",
						Content: res.Content,
						Usage: stream.UsageInfo{
							PromptTokens:     res.PromptTokens,
							CompletionTokens: res.CompletionTokens,
						},
					})
					return res, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("invalid or empty response choice returned from remote model")
}

// CallModelStream performs a streaming inference call.
func (b *RemoteOpenAIBackend) CallModelStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error) {
	type StreamOptionsStruct struct {
		IncludeUsage bool `json:"include_usage"`
	}

	type CompletionRequest struct {
		Model          string                 `json:"model"`
		Messages       []map[string]string    `json:"messages"`
		Temperature    float64                `json:"temperature"`
		Stream         bool                   `json:"stream"`
		StreamOptions  *StreamOptionsStruct   `json:"stream_options,omitempty"`
		ResponseFormat map[string]interface{} `json:"response_format,omitempty"`
	}

	reqBody := CompletionRequest{
		Model:       b.model,
		Messages:    MessagesToMaps(messages),
		Temperature: 1.0,
		Stream:      true,
		StreamOptions: &StreamOptionsStruct{
			IncludeUsage: true,
		},
	}

	if jsonSchema != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(jsonSchema), &schemaObj) == nil {
			reqBody.ResponseFormat = map[string]interface{}{
				"type":   "json_object",
				"schema": schemaObj,
			}
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}

	startTime := time.Now()
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote model stream HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote model stream HTTP server returned status %s: %s", resp.Status, string(respBody))
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
			return nil, fmt.Errorf("failed to read stream line: %w", err)
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
				b.getPublisher().PublishStream(stream.StreamChunk{
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

	resContent := accumulatedContent.String()

	b.getPublisher().PublishStream(stream.StreamChunk{
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

	if tracker, ok := GetTokenTracker(ctx); ok {
		tracker.Record(false, promptTokens, completionTokens)
	}

	return &InferenceResult{
		Content:          resContent,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		DurationSeconds:  duration,
		TokensPerSecond:  speed,
	}, nil
}

// Status pings the models endpoint to verify readiness.
func (b *RemoteOpenAIBackend) Status() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Try checking standard models endpoint derived from completion URL
	modelsURL := b.url
	if strings.HasSuffix(modelsURL, "/chat/completions") {
		modelsURL = strings.Replace(modelsURL, "/chat/completions", "/models", 1)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		return "unavailable"
	}
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Fallback to checking connection to the base URL itself
		reqBase, err := http.NewRequestWithContext(ctx, "GET", b.url, nil)
		if err != nil {
			return "unavailable"
		}
		respBase, err := http.DefaultClient.Do(reqBase)
		if err != nil {
			return "unavailable"
		}
		respBase.Body.Close()
		return "active"
	}
	defer resp.Body.Close()

	return "active"
}

// Start is a no-op for remote backends.
func (b *RemoteOpenAIBackend) Start(ctx context.Context) error {
	return nil
}

// Stop is a no-op for remote backends.
func (b *RemoteOpenAIBackend) Stop() error {
	return nil
}

func (b *RemoteOpenAIBackend) getPublisher() telemetry.EventPublisher {
	if b.publisher != nil {
		return b.publisher
	}
	return telemetry.Default
}
