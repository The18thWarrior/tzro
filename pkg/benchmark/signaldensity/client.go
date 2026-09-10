package signaldensity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// API completion structures.
type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Tools       []chatTool    `json:"tools,omitempty"`
}

type chatUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost,omitempty"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

// CostTracker maintains cumulative spend and enforces the circuit breaker.
type CostTracker struct {
	mu       sync.Mutex
	MaxCost  float64
	SpentUSD float64
	Exceeded bool
}

// NewCostTracker creates a cost tracker with a defined hard limit.
func NewCostTracker(maxCost float64) *CostTracker {
	if maxCost <= 0 {
		maxCost = 2.00 // default $2.00
	}
	return &CostTracker{
		MaxCost: maxCost,
	}
}

// AddCost registers turn cost and returns false if the limit is exceeded.
func (ct *CostTracker) AddCost(cost float64) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.SpentUSD += cost
	if ct.SpentUSD >= ct.MaxCost {
		if !ct.Exceeded {
			ct.Exceeded = true
			fmt.Fprintf(os.Stderr, "\n[!] Max cost limit ($%.2f) reached. Halting benchmark safely.\n", ct.MaxCost)
		}
		return false
	}
	return true
}

// IsExceeded returns whether spending has hit the ceiling.
func (ct *CostTracker) IsExceeded() bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.Exceeded
}

// CurrentSpend returns current total spend in USD.
func (ct *CostTracker) CurrentSpend() float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.SpentUSD
}

// Client executes completions against OpenRouter or an OpenAI-compatible API endpoint.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	NoCache    bool
}

// NewClient instantiates an LLM API client.
func NewClient(baseURL, apiKey string, timeout time.Duration, noCache bool) *Client {
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
	}

	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
		NoCache: noCache,
	}
}

// CompletionResult stores the result of an API call.
type CompletionResult struct {
	Content          string
	ToolCalls        []chatToolCall
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	RawMessage       chatMessage
}

// Complete queries the model with the given prompt.
func (c *Client) Complete(ctx context.Context, model, prompt string, pricing ModelPricing) (*CompletionResult, error) {
	return c.CompleteChat(ctx, model, []chatMessage{
		{Role: "user", Content: prompt},
	}, nil, pricing)
}

// CompleteChat queries the model with a list of messages and optional tool definitions.
func (c *Client) CompleteChat(ctx context.Context, model string, messages []chatMessage, tools []chatTool, pricing ModelPricing) (*CompletionResult, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"

	reqBody := chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.0,
		Tools:       tools,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request error: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
	req.Header.Set("X-Title", "tzro-signal-density-benchmark")

	if c.NoCache {
		req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		req.Header.Set("X-Tzro-Bust-Cache", fmt.Sprintf("%d", time.Now().UnixNano()))
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var parsed chatResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal response error: %w", err)
	}

	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("api response error: %s", parsed.Error.Message)
	}

	content := ""
	var toolCalls []chatToolCall
	var rawMsg chatMessage
	if len(parsed.Choices) > 0 {
		rawMsg = parsed.Choices[0].Message
		content = rawMsg.Content
		toolCalls = rawMsg.ToolCalls
	}

	cost := parsed.Usage.Cost
	if cost == 0 {
		// Calculate cost via pricing table
		cost = (float64(parsed.Usage.PromptTokens)*pricing.PromptPerM +
			float64(parsed.Usage.CompletionTokens)*pricing.CompletionPerM) / 1_000_000.0
	}

	return &CompletionResult{
		Content:          content,
		ToolCalls:        toolCalls,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		CostUSD:          cost,
		RawMessage:       rawMsg,
	}, nil
}
