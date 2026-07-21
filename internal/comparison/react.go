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
			Content          *string         `json:"content"`
			ReasoningContent *string         `json:"reasoning_content,omitempty"`
			ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

const reactSystemPrompt = `You are a documentation generator. You have access to filesystem tools to explore a Go codebase. Read the relevant source files, understand the code, and produce the requested documentation. Call tools as needed. When you have gathered enough information, output the final documentation as markdown.`

const reactDatanalSystemPrompt = `You are a data analyst. You have access to filesystem tools (read_file, list_dir, search_files) to read and analyze structured data files. Read the specified data file, parse it as CSV/tabular data, and answer the question precisely. When analyzing CSV data: pay attention to column headers in the first row, handle empty/blank values explicitly, and count/group/filter as requested. Show your work — state the total record count and intermediate calculations before giving your final answer.`

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

// buildLocalReActTools extends buildReActTools with a synthetic write_file
// tool that acts as an output sink. When the local model calls write_file,
// the loop captures the content argument as the final documentation output.
// This is needed because small local models instinctively try to "save" their
// results to a file rather than returning them as a text response.
func buildLocalReActTools() ([]reactToolDef, map[string]*tools.BaseAgentTool) {
	defs, dispatch := buildReActTools()

	// Add write_file as a synthetic output sink (not in dispatch — handled specially)
	writeFileDef := reactToolDef{Type: "function"}
	writeFileDef.Function.Name = "write_file"
	writeFileDef.Function.Description = "Write content to a file. Use this to save your final documentation output."
	writeFileDef.Function.Parameters = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "The file path to write to",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
	defs = append(defs, writeFileDef)

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

	// Select system prompt based on task category
	sysPrompt := reactSystemPrompt
	if task.Category == CategoryDatanal {
		sysPrompt = reactDatanalSystemPrompt
	}

	messages := []reactMessage{
		{Role: "system", Content: sysPrompt},
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

// ────────────────────────────────────────────────────────────────────────────
// Local ReAct — flat ReAct loop on the local model (DAG-free baseline)
// ────────────────────────────────────────────────────────────────────────────

const (
	maxLocalReActIterations = 100
	localReActTimeout       = 300 * time.Second // 5 minute timeout per API call
)

const localReActSystemPrompt = `You are a documentation generator running on a local model. You have access to filesystem tools to explore a Go codebase. Read the relevant source files, understand the code, and produce the requested documentation.

IMPORTANT instructions for tool usage:
- Always start by listing the project directory to understand the structure
- Read specific files that are relevant to the documentation task
- Use search_files to find specific patterns or function definitions
- When you have gathered enough information, output the final documentation as markdown
- Be thorough — explore multiple files and directories before writing
- Do NOT hallucinate file contents — always read files before documenting them`

// RunLocalReAct executes a ReAct loop against the local llama-server sidecar.
// This is the DAG-free baseline for comparing whether structured DAG execution
// provides quality benefits over a simple tool-calling loop.
//
// Key differences from RunReAct (cloud):
//   - Calls the local llama-server's OpenAI-compatible /v1/chat/completions endpoint
//   - 100-step budget (vs. cloud's 50) since local inference is free
//   - Tokens tracked as LocalTokens (not CloudTokens)
//   - Uses a more explicit system prompt to compensate for smaller model capacity
//   - Same middle-out compaction for context management
func RunLocalReAct(ctx context.Context, task ComparisonTask, pricing PricingTable, outputDir string) (ComparisonResult, error) {
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	// Resolve the local sidecar endpoint
	localEndpoint, err := resolveLocalEndpoint()
	if err != nil {
		return ComparisonResult{
			TaskID:    task.ID,
			TaskTier:  task.Tier,
			Condition: ConditionLocalReAct,
			Error:     fmt.Sprintf("failed to resolve local sidecar: %v", err),
		}, err
	}

	toolDefs, dispatch := buildLocalReActTools()

	messages := []reactMessage{
		{Role: "system", Content: localReActSystemPrompt},
		{Role: "user", Content: task.Prompt},
	}

	startTime := time.Now()
	toolCallCount := 0
	accumulatedTokens := 0
	var capturedOutput string // Captured from write_file sink calls

	for iteration := 0; iteration < maxLocalReActIterations; iteration++ {
		fmt.Fprintf(os.Stderr, "    [local_react] iteration %d, %d tool calls, %s elapsed...\r",
			iteration+1, toolCallCount, time.Since(startTime).Round(time.Second))

		resp, err := callLocalWithTools(ctx, messages, toolDefs, localEndpoint)
		if err != nil {
			fmt.Fprintln(os.Stderr) // clear \r line
			return ComparisonResult{
				TaskID:    task.ID,
				TaskTier:  task.Tier,
				Condition: ConditionLocalReAct,
				Error:     fmt.Sprintf("local API call failed at iteration %d: %v", iteration, err),
			}, err
		}

		// Track tokens as local (not cloud) — context window size for limits
		accumulatedTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
		duration := time.Since(startTime).Seconds()
		speed := 0.0
		if duration > 0 && resp.Usage.CompletionTokens > 0 {
			speed = float64(resp.Usage.CompletionTokens) / duration
		}
		tracker.Record(false, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, duration, speed) // false = local

		if len(resp.Choices) == 0 {
			return ComparisonResult{
				TaskID:    task.ID,
				TaskTier:  task.Tier,
				Condition: ConditionLocalReAct,
				Error:     "empty choices from local API",
			}, fmt.Errorf("empty choices from local API at iteration %d", iteration)
		}

		choice := resp.Choices[0]

		// If the model returned tool calls, execute them
		if len(choice.Message.ToolCalls) > 0 {
			// Append the assistant message with tool calls
			messages = append(messages, reactMessage{
				Role:      "assistant",
				ToolCalls: choice.Message.ToolCalls,
			})

			for _, tc := range choice.Message.ToolCalls {
				toolCallCount++

				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

				// Unwrap tool_arguments nesting: local models often wrap args as
				// {"tool_arguments": {"path": "..."}} instead of {"path": "..."}.
				if inner, ok := args["tool_arguments"].(map[string]interface{}); ok {
					args = inner
				}

				// Progress: show which tool is being called
				toolArg := ""
				if p, ok := args["path"].(string); ok {
					toolArg = p
				} else if q, ok := args["query"].(string); ok {
					toolArg = q
				}
				fmt.Fprintf(os.Stderr, "    [local_react] → %s(%s)%s\n",
					tc.Function.Name, toolArg, strings.Repeat(" ", 40))

				var toolOutput string
				if tc.Function.Name == "write_file" {
					// Output sink: capture the content and signal success.
					// The model is "writing" its final output to a file.
					if content, ok := args["content"].(string); ok && content != "" {
						capturedOutput = content
					}
					toolOutput = `{"status": "ok", "bytes_written": ` + fmt.Sprintf("%d", len(capturedOutput)) + `}`
				} else if tool, ok := dispatch[tc.Function.Name]; ok {
					result, err := tool.Call(ctx, args)
					if err != nil {
						toolOutput = fmt.Sprintf(`{"error": "%s"}`, err.Error())
					} else {
						toolOutput = result
					}
				} else {
					toolOutput = fmt.Sprintf(`{"error": "unknown tool: %s"}`, tc.Function.Name)
				}

				messages = append(messages, reactMessage{
					Role:       "tool",
					Content:    toolOutput,
					ToolCallID: tc.ID,
				})
			}

			// If write_file was called and we captured output, terminate the loop
			if capturedOutput != "" {
				localUsage, _ := tracker.GetUsage()
				return ComparisonResult{
					TaskID:        task.ID,
					TaskTier:      task.Tier,
					Condition:     ConditionLocalReAct,
					LocalTokens:   localUsage,
					WallClockMs:   time.Since(startTime).Milliseconds(),
					EstCostUSD:    0,
					ToolCallCount: toolCallCount,
					OutputText:    capturedOutput,
				}, nil
			}

			// Middle-out compression (same thresholds as cloud ReAct)
			if accumulatedTokens >= middleOutThreshold {
				messages = compressToolMessages(messages)
			}

			// Token safety limit
			if accumulatedTokens >= maxAccumulatedTokens {
				localUsage, _ := tracker.GetUsage()
				return ComparisonResult{
					TaskID:        task.ID,
					TaskTier:      task.Tier,
					Condition:     ConditionLocalReAct,
					LocalTokens:   localUsage,
					WallClockMs:   time.Since(startTime).Milliseconds(),
					EstCostUSD:    0, // local tokens are free
					ToolCallCount: toolCallCount,
					OutputText:    "[terminated: token limit exceeded]",
					Error:         fmt.Sprintf("accumulated token limit exceeded (%d >= %d)", accumulatedTokens, maxAccumulatedTokens),
				}, nil
			}

			continue
		}

		// Final text response — model is done exploring and produced output.
		// Handle thinking models: the actual content may be in reasoning_content
		// when content is empty or nil.
		outputText := ""
		if choice.Message.Content != nil && *choice.Message.Content != "" {
			outputText = *choice.Message.Content
		}

		// Fallback: if content is empty, check reasoning_content.
		// Thinking models (Qwen, MiniCPM) put their output here when the
		// chat template separates reasoning from response.
		if outputText == "" && choice.Message.ReasoningContent != nil && *choice.Message.ReasoningContent != "" {
			reasoning := *choice.Message.ReasoningContent

			// Check if reasoning contains a write_file tool call with content.
			// Extract the content as the output (the model was trying to save its result).
			if extracted := extractWriteFileContent(reasoning); extracted != "" {
				outputText = extracted
			} else if strings.Contains(reasoning, "<tool_call>") {
				// Model tried to use tools via XML — redirect to structured calls
				messages = append(messages, reactMessage{
					Role:    "assistant",
					Content: reasoning,
				})
				messages = append(messages, reactMessage{
					Role:    "user",
					Content: "Please use the tools directly by calling them, not by writing tool_call XML. Continue exploring the codebase and produce the final documentation.",
				})
				continue
			} else {
				// No embedded tool calls — use reasoning as the actual output
				outputText = reasoning
			}
		}

		localUsage, _ := tracker.GetUsage()
		return ComparisonResult{
			TaskID:        task.ID,
			TaskTier:      task.Tier,
			Condition:     ConditionLocalReAct,
			LocalTokens:   localUsage,
			WallClockMs:   time.Since(startTime).Milliseconds(),
			EstCostUSD:    0, // local tokens are free
			ToolCallCount: toolCallCount,
			OutputText:    outputText,
		}, nil
	}

	// Hit max iterations
	localUsage, _ := tracker.GetUsage()
	return ComparisonResult{
		TaskID:        task.ID,
		TaskTier:      task.Tier,
		Condition:     ConditionLocalReAct,
		LocalTokens:   localUsage,
		WallClockMs:   time.Since(startTime).Milliseconds(),
		EstCostUSD:    0,
		ToolCallCount: toolCallCount,
		OutputText:    "[terminated: max iterations reached]",
		Error:         fmt.Sprintf("max iterations reached (%d)", maxLocalReActIterations),
	}, nil
}

// resolveLocalEndpoint discovers the local llama-server sidecar port and returns
// the OpenAI-compatible chat completions endpoint URL. Auto-starts the sidecar
// if it's not running and waits for health.
func resolveLocalEndpoint() (string, error) {
	ctx := context.Background()

	status, activePort, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	if status == "Stopped" {
		if err := inference.GlobalLocalModel.Start(ctx); err != nil {
			return "", fmt.Errorf("sidecar auto-start failed: %w", err)
		}
		// Wait for healthy
		_, activePort, _, _, _ = inference.GlobalLocalModel.GetStatusInfo()
		fmt.Fprintf(os.Stderr, "[LocalReAct] Waiting for sidecar health on port %d...\n", activePort)
		for attempt := range 30 {
			healthURL := fmt.Sprintf("http://localhost:%d/health", activePort)
			resp, err := http.Get(healthURL)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				fmt.Fprintf(os.Stderr, "[LocalReAct] Sidecar healthy after %d attempts\n", attempt+1)
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(1 * time.Second)
		}
	}

	if activePort <= 0 {
		return "", fmt.Errorf("sidecar port not available (status=%s, port=%d)", status, activePort)
	}

	return fmt.Sprintf("http://localhost:%d/v1/chat/completions", activePort), nil
}

// callLocalWithTools sends a chat completion request with tools to the local
// llama-server's OpenAI-compatible endpoint. Structurally identical to
// callCloudWithTools but with local-appropriate timeouts and no auth.
func callLocalWithTools(ctx context.Context, messages []reactMessage, toolDefs []reactToolDef, endpoint string) (*reactCompletionResponse, error) {
	reqBody := reactCompletionRequest{
		Model:       "local",
		Messages:    messages,
		Tools:       toolDefs,
		Temperature: 0.1,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: localReActTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read local response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result reactCompletionResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to decode local response: %w", err)
	}

	return &result, nil
}

// extractWriteFileContent attempts to extract the content parameter from a
// write_file tool call embedded as XML in reasoning_content. Local models
// sometimes produce their final output this way:
//
//	<tool_call>
//	<function=write_file>
//	<parameter=tool_arguments>
//	{"path": "...", "content": "...the actual documentation..."}
//	</parameter>
//	</function>
//	</tool_call>
func extractWriteFileContent(reasoning string) string {
	if !strings.Contains(reasoning, "write_file") {
		return ""
	}

	// Try to find JSON with a "content" field in the reasoning text.
	// The model wraps it in various XML structures, but the JSON payload
	// always has "content": "..." with the documentation.
	//
	// Strategy: find the largest JSON object containing a "content" key.
	var bestContent string

	// Look for {"path": "...", "content": "..."} or {"tool_arguments": {"path": ..., "content": ...}}
	for i := 0; i < len(reasoning); i++ {
		if reasoning[i] != '{' {
			continue
		}
		// Find matching closing brace by counting
		depth := 0
		for j := i; j < len(reasoning); j++ {
			if reasoning[j] == '{' {
				depth++
			} else if reasoning[j] == '}' {
				depth--
				if depth == 0 {
					candidate := reasoning[i : j+1]
					var obj map[string]interface{}
					if json.Unmarshal([]byte(candidate), &obj) == nil {
						// Check for content at top level
						if c, ok := obj["content"].(string); ok && len(c) > len(bestContent) {
							bestContent = c
						}
						// Check nested in tool_arguments
						if ta, ok := obj["tool_arguments"].(map[string]interface{}); ok {
							if c, ok := ta["content"].(string); ok && len(c) > len(bestContent) {
								bestContent = c
							}
						}
					}
					break
				}
			}
		}
	}

	return bestContent
}
