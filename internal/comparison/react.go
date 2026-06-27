package comparison

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/tools"
)

const (
	maxReActIterations    = 50
	maxAccumulatedTokens  = 200000 // Max context window size (not cumulative throughput)
	middleOutThreshold    = 80000  // Trigger compression earlier for better quality preservation
	middleOutKeepLines    = 80     // Lines to keep from head and tail of each tool result
	recentToolCallsToKeep = 3      // Most recent tool results to leave uncompressed
)

// reactMessage represents a single message in the ReAct conversation.
type reactMessage struct {
	Role       string          `json:"role"`
	Content    interface{}     `json:"content,omitempty"`      // string for user/system/tool, nil for assistant with tool_calls
	ToolCalls  []reactToolCall `json:"tool_calls,omitempty"`   // only for assistant messages
	ToolCallID string          `json:"tool_call_id,omitempty"` // only for tool result messages
}

// reactToolCall represents a tool invocation from the model.
type reactToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// ExtraContent carries opaque metadata from the API (e.g. thought_signature
	// for Gemini thinking models). It MUST be echoed back verbatim in the
	// assistant message to maintain reasoning continuity.
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}

// reactToolDef defines a tool for the OpenAI tools parameter.
type reactToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Parameters  interface{} `json:"parameters"`
	} `json:"function"`
}

// reactCompletionRequest is the request body for the OpenAI-compatible chat API with tools.
type reactCompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []reactMessage `json:"messages"`
	Tools       []reactToolDef `json:"tools"`
	Temperature float64        `json:"temperature"`
}

// reactCompletionResponse is the response from the OpenAI-compatible chat API.
type reactCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content   *string         `json:"content"`
			ToolCalls []reactToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

const reactSystemPrompt = `You are a documentation generator. You have access to filesystem tools to explore a Go codebase. Read the relevant source files, understand the code, and produce the requested documentation. Call tools as needed. When you have gathered enough information, output the final documentation as markdown.`

// buildReActTools creates the OpenAI-compatible tool definitions and a dispatch map.
func buildReActTools() ([]reactToolDef, map[string]*tools.BaseAgentTool) {
	// Create a path validator rooted at the project directory
	cwd, _ := os.Getwd()
	validator := tools.NewPathValidator([]string{cwd})

	readFile := tools.NewReadFileTool(validator)
	listDir := tools.NewListDirTool(validator)
	searchFiles := tools.NewSearchFilesTool(validator)

	dispatch := map[string]*tools.BaseAgentTool{
		"read_file":    readFile,
		"list_dir":     listDir,
		"search_files": searchFiles,
	}

	var defs []reactToolDef
	for name, tool := range dispatch {
		schema, _ := tool.GetSchema()
		var params interface{}
		_ = json.Unmarshal([]byte(schema), &params)

		def := reactToolDef{Type: "function"}
		def.Function.Name = name
		def.Function.Description = getToolDescription(name)
		def.Function.Parameters = params
		defs = append(defs, def)
	}

	return defs, dispatch
}

func getToolDescription(name string) string {
	switch name {
	case "read_file":
		return "Read file content with optional line range. Returns raw content (max 200 lines per call)."
	case "list_dir":
		return "List the contents of a directory with file sizes and types."
	case "search_files":
		return "Search for a text pattern across files using ripgrep. Returns matching file paths and line content."
	default:
		return ""
	}
}

// RunReAct executes a ReAct loop for a single task, returning the result.
// The loop sends messages to the cloud API, executes filesystem tools on
// tool_call responses, appends raw uncompacted tool results, and repeats until
// the model produces a final text response or safety limits are hit.
func RunReAct(ctx context.Context, task ComparisonTask, pricing PricingTable) (ComparisonResult, error) {
	return RunReActWithEndpoint(ctx, task, pricing, "")
}

// RunReActWithEndpoint is like RunReAct but allows overriding the API endpoint (for testing).
func RunReActWithEndpoint(ctx context.Context, task ComparisonTask, pricing PricingTable, endpoint string) (ComparisonResult, error) {
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	toolDefs, dispatch := buildReActTools()

	messages := []reactMessage{
		{Role: "system", Content: reactSystemPrompt},
		{Role: "user", Content: task.Prompt},
	}

	startTime := time.Now()
	toolCallCount := 0
	accumulatedTokens := 0 // Context window size (current, not cumulative)

	for iteration := 0; iteration < maxReActIterations; iteration++ {
		resp, err := callCloudWithTools(ctx, messages, toolDefs, endpoint)
		if err != nil {
			return ComparisonResult{
				TaskID:    task.ID,
				TaskTier:  task.Tier,
				Condition: ConditionCloudReAct,
				Error:     fmt.Sprintf("cloud API call failed at iteration %d: %v", iteration, err),
			}, err
		}

		// Track tokens — context window size (not cumulative) for limits.
		// The TokenTracker handles cumulative throughput for cost reporting.
		accumulatedTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
		duration := time.Since(startTime).Seconds()
		speed := 0.0
		if duration > 0 && resp.Usage.CompletionTokens > 0 {
			speed = float64(resp.Usage.CompletionTokens) / duration
		}
		tracker.Record(true, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, duration, speed)

		if len(resp.Choices) == 0 {
			return ComparisonResult{
				TaskID:    task.ID,
				TaskTier:  task.Tier,
				Condition: ConditionCloudReAct,
				Error:     "empty choices from cloud API",
			}, fmt.Errorf("empty choices from cloud API at iteration %d", iteration)
		}

		choice := resp.Choices[0]

		// If the model returned tool calls, execute them
		if len(choice.Message.ToolCalls) > 0 {
			// Append the assistant message with tool calls (no content)
			messages = append(messages, reactMessage{
				Role:      "assistant",
				ToolCalls: choice.Message.ToolCalls,
			})

			for _, tc := range choice.Message.ToolCalls {
				toolCallCount++

				// Parse arguments
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

				// Dispatch to the tool
				var toolOutput string
				if tool, ok := dispatch[tc.Function.Name]; ok {
					result, err := tool.Call(ctx, args)
					if err != nil {
						toolOutput = fmt.Sprintf(`{"error": "%s"}`, err.Error())
					} else {
						toolOutput = result
					}
				} else {
					toolOutput = fmt.Sprintf(`{"error": "unknown tool: %s"}`, tc.Function.Name)
				}

				// Append raw, uncompacted tool result
				messages = append(messages, reactMessage{
					Role:       "tool",
					Content:    toolOutput,
					ToolCallID: tc.ID,
				})
			}

			// Middle-out compression: truncate older tool results when context grows too large
			if accumulatedTokens >= middleOutThreshold {
				messages = compressToolMessages(messages)
			}

			// Check token accumulation safety limit
			if accumulatedTokens >= maxAccumulatedTokens {
				_, cloudUsage := tracker.GetUsage()
				return ComparisonResult{
					TaskID:        task.ID,
					TaskTier:      task.Tier,
					Condition:     ConditionCloudReAct,
					CloudTokens:   cloudUsage,
					WallClockMs:   time.Since(startTime).Milliseconds(),
					EstCostUSD:    EstimateCost(cloudUsage, inference.TokenUsage{}, pricing),
					ToolCallCount: toolCallCount,
					OutputText:    "[terminated: token limit exceeded]",
					Error:         fmt.Sprintf("accumulated token limit exceeded (%d >= %d)", accumulatedTokens, maxAccumulatedTokens),
				}, nil
			}

			continue
		}

		// Final text response
		outputText := ""
		if choice.Message.Content != nil {
			outputText = *choice.Message.Content
		}

		_, cloudUsage := tracker.GetUsage()
		return ComparisonResult{
			TaskID:        task.ID,
			TaskTier:      task.Tier,
			Condition:     ConditionCloudReAct,
			CloudTokens:   cloudUsage,
			WallClockMs:   time.Since(startTime).Milliseconds(),
			EstCostUSD:    EstimateCost(cloudUsage, inference.TokenUsage{}, pricing),
			ToolCallCount: toolCallCount,
			OutputText:    outputText,
		}, nil
	}

	// Hit max iterations without final response
	_, cloudUsage := tracker.GetUsage()
	return ComparisonResult{
		TaskID:        task.ID,
		TaskTier:      task.Tier,
		Condition:     ConditionCloudReAct,
		CloudTokens:   cloudUsage,
		WallClockMs:   time.Since(startTime).Milliseconds(),
		EstCostUSD:    EstimateCost(cloudUsage, inference.TokenUsage{}, pricing),
		ToolCallCount: toolCallCount,
		OutputText:    "[terminated: max iterations reached]",
		Error:         fmt.Sprintf("max iterations reached (%d)", maxReActIterations),
	}, nil
}

// callCloudWithTools sends a chat completion request with tools to the cloud API.
func callCloudWithTools(ctx context.Context, messages []reactMessage, toolDefs []reactToolDef, endpoint string) (*reactCompletionResponse, error) {
	if endpoint == "" {
		cfg := config.Get()
		switch cfg.CloudProvider {
		case "google":
			endpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
		case "openai":
			endpoint = "https://api.openai.com/v1/chat/completions"
		default:
			return nil, fmt.Errorf("unsupported cloud provider '%s'", cfg.CloudProvider)
		}
	}

	apiKey := config.GetCloudAPIKey()

	modelName := config.GetCloudModel()

	reqBody := reactCompletionRequest{
		Model:       modelName,
		Messages:    messages,
		Tools:       toolDefs,
		Temperature: 0.1,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	// For Google provider, use ?key= query param (canonical Gemini API auth).
	// For other providers, use Bearer token in Authorization header.
	provider := config.Get().CloudProvider
	// The /openai/ compatibility endpoint on generativelanguage.googleapis.com
	// requires Bearer auth, same as the standard OpenAI API.
	// The ?key= query param only works for the native Gemini REST endpoints.
	if provider == "google" && apiKey != "" && !strings.Contains(endpoint, "/openai/") {
		if strings.Contains(endpoint, "?") {
			endpoint += "&key=" + apiKey
		} else {
			endpoint += "?key=" + apiKey
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" && (provider != "google" || strings.Contains(endpoint, "/openai/")) {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cloud API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result reactCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode cloud response: %w", err)
	}

	return &result, nil
}

// compressToolMessages applies middle-out compression to older tool result messages.
// Keeps the first and last middleOutKeepLines lines of each tool result,
// replacing the middle with a "[... N lines omitted ...]" marker.
// Only compresses tool results older than the most recent recentToolCallsToKeep.
func compressToolMessages(messages []reactMessage) []reactMessage {
	// Find indices of tool-result messages
	var toolIndices []int
	for i, m := range messages {
		if m.Role == "tool" {
			toolIndices = append(toolIndices, i)
		}
	}

	// Only compress if there are more than recentToolCallsToKeep tool results
	if len(toolIndices) <= recentToolCallsToKeep {
		return messages
	}

	// Compress all but the most recent recentToolCallsToKeep
	cutoff := len(toolIndices) - recentToolCallsToKeep
	for _, idx := range toolIndices[:cutoff] {
		content, ok := messages[idx].Content.(string)
		if !ok || len(content) < 500 {
			continue // Skip small results
		}
		messages[idx].Content = middleOutTruncate(content, middleOutKeepLines)
	}
	return messages
}

// middleOutTruncate keeps the first and last N lines, replacing the middle
// with a "[... N lines omitted ...]" marker.
func middleOutTruncate(content string, keepLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= keepLines*2 {
		return content
	}
	head := strings.Join(lines[:keepLines], "\n")
	tail := strings.Join(lines[len(lines)-keepLines:], "\n")
	omitted := len(lines) - keepLines*2
	return head + "\n[... " + strconv.Itoa(omitted) + " lines omitted ...]\n" + tail
}
