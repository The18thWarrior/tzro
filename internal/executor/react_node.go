package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tzro/internal/tools"
)

// ReActToolCall represents a single tool execution request from the model.
type ReActToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ReActMessage represents a single message in the ReAct conversation history.
type ReActMessage struct {
	Role       string          `json:"role"` // "system", "user", "assistant", "tool"
	Content    string          `json:"content"`
	ToolCalls  []ReActToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ReActToolDef represents a tool definition passed to the inference backend.
type ReActToolDef struct {
	Type     string           `json:"type"` // "function"
	Function ReActFunctionDef `json:"function"`
}

// ReActFunctionDef contains the function signature and JSON schema parameters.
type ReActFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ReActResponse holds the output of a single inference step.
type ReActResponse struct {
	Content          string          `json:"content"`
	ToolCalls        []ReActToolCall `json:"tool_calls,omitempty"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
}

// ReActToolCallRecord tracks executed tool calls for metrics and auditing.
type ReActToolCallRecord struct {
	ToolName  string                 `json:"toolName"`
	Arguments map[string]interface{} `json:"arguments"`
	Output    string                 `json:"output"`
	Error     string                 `json:"error,omitempty"`
}

// ReActConfig configures the ReAct loop execution.
type ReActConfig struct {
	Goal             string
	AllowedTools     []string
	StepBudget       int
	MaxContextTokens int
	SystemPrompt     string
	TaskContext      string
	UpstreamContext  string
	ToolExecutor     func(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

// ReActResult holds the outcome of a completed ReAct loop.
type ReActResult struct {
	FinalOutput           string                `json:"finalOutput"`
	StepCount             int                   `json:"stepCount"`
	ToolCalls             []ReActToolCallRecord `json:"toolCalls"`
	DurationMs            int64                 `json:"durationMs"`
	TotalPromptTokens     int                   `json:"totalPromptTokens"`
	TotalCompletionTokens int                   `json:"totalCompletionTokens"`
}

// ReActInference abstracts model calls for the ReAct loop.
type ReActInference interface {
	Call(ctx context.Context, messages []ReActMessage, tools []ReActToolDef) (*ReActResponse, error)
}

const defaultReActSystemPrompt = `You are an expert autonomous software engineering and research agent.
You have access to structured tools to explore the workspace environment, search repositories, read source code, inspect data, or conduct research.

GUIDELINES:
1. Plan your actions carefully. Call tools incrementally to discover directory layouts, read files, or query information.
2. When reading code or documents, inspect the essential files thoroughly to ensure factual completeness.
3. If searching or reading files, use the provided tools rather than assuming or guessing file contents.
4. When you have gathered sufficient information to address the user's objective, provide a comprehensive, well-structured final markdown response without calling any further tools.`

// buildToolDefinitions converts allowed tool names into OpenAI-compatible tool definitions.
func buildToolDefinitions(allowedTools []string) []ReActToolDef {
	var defs []ReActToolDef
	for _, toolName := range allowedTools {
		t := tools.GetTool(toolName)
		if t != nil {
			var params map[string]interface{}
			schemaStr, err := t.GetSchema()
			if err == nil && schemaStr != "" {
				_ = json.Unmarshal([]byte(schemaStr), &params)
			}
			if params != nil {
				// If wrapped in {"type": "object", "properties": {"tool_arguments": {...}}}, unwrap tool_arguments
				if props, ok := params["properties"].(map[string]interface{}); ok {
					if toolArgs, ok := props["tool_arguments"].(map[string]interface{}); ok {
						params = toolArgs
					}
				}
			}
			if params == nil {
				params = map[string]interface{}{
					"type": "object",
				}
			}
			defs = append(defs, ReActToolDef{
				Type: "function",
				Function: ReActFunctionDef{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  params,
				},
			})
		} else {
			// Fallback generic definition if not registered in tools.Registry
			defs = append(defs, ReActToolDef{
				Type: "function",
				Function: ReActFunctionDef{
					Name:        toolName,
					Description: fmt.Sprintf("Execute %s", toolName),
					Parameters: map[string]interface{}{
						"type": "object",
					},
				},
			})
		}
	}
	return defs
}

// RunReActLoop executes an autonomous ReAct loop until convergence or step exhaustion.
func RunReActLoop(ctx context.Context, cfg ReActConfig, inferenceBackend ReActInference) (*ReActResult, error) {
	startTime := time.Now()

	stepBudget := cfg.StepBudget
	if stepBudget <= 0 {
		stepBudget = 15
	}
	maxContextTokens := cfg.MaxContextTokens
	if maxContextTokens <= 0 {
		maxContextTokens = 12000
	}

	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = defaultReActSystemPrompt
	}

	var userPromptParts []string
	if cfg.Goal != "" {
		userPromptParts = append(userPromptParts, fmt.Sprintf("## Task Objective\n%s", cfg.Goal))
	}
	if cfg.UpstreamContext != "" {
		userPromptParts = append(userPromptParts, fmt.Sprintf("## Context from Prior Steps\n%s", cfg.UpstreamContext))
	}
	if cfg.TaskContext != "" {
		userPromptParts = append(userPromptParts, fmt.Sprintf("## Additional Context\n%s", cfg.TaskContext))
	}

	messages := []ReActMessage{
		{
			Role:    "system",
			Content: sysPrompt,
		},
		{
			Role:    "user",
			Content: strings.Join(userPromptParts, "\n\n"),
		},
	}

	toolDefs := buildToolDefinitions(cfg.AllowedTools)
	toolExecutor := cfg.ToolExecutor
	if toolExecutor == nil {
		toolExecutor = func(execCtx context.Context, name string, args map[string]interface{}) (string, error) {
			t := tools.GetTool(name)
			if t == nil {
				return "", fmt.Errorf("tool %q not found in registry", name)
			}
			return t.Call(execCtx, args)
		}
	}

	var toolRecords []ReActToolCallRecord
	var totalPromptToks int
	var totalComplToks int
	var finalOutput string
	stepCount := 0

	for step := 1; step <= stepBudget; step++ {
		stepCount = step

		// Check for context cancellation
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		resp, err := inferenceBackend.Call(ctx, messages, toolDefs)
		if err != nil {
			return nil, fmt.Errorf("react step %d inference failed: %w", step, err)
		}

		totalPromptToks += resp.PromptTokens
		totalComplToks += resp.CompletionTokens

		// If no tool calls, the model has naturally converged on its final answer
		if len(resp.ToolCalls) == 0 {
			finalOutput = resp.Content
			break
		}

		// Append assistant response with tool calls
		messages = append(messages, ReActMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute all requested tool calls
		for _, tc := range resp.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			callKey := fmt.Sprintf("%s:%s", tc.Name, string(argsJSON))
			
			// Repetition guard: check if this exact tool call was executed >= 2 times previously
			recentRepeatCount := 0
			for i := len(toolRecords) - 1; i >= 0 && i >= len(toolRecords)-4; i-- {
				prevJSON, _ := json.Marshal(toolRecords[i].Arguments)
				prevKey := fmt.Sprintf("%s:%s", toolRecords[i].ToolName, string(prevJSON))
				if prevKey == callKey {
					recentRepeatCount++
				}
			}

			var toolOutput string
			var toolErr error
			rec := ReActToolCallRecord{
				ToolName:  tc.Name,
				Arguments: tc.Arguments,
			}

			if recentRepeatCount >= 2 {
				// Intercept repetition
				toolOutput = fmt.Sprintf("Warning: Repeated tool call %q with identical arguments detected (%d times). Do not repeat this call; synthesize or use another approach.", tc.Name, recentRepeatCount+1)
				rec.Output = toolOutput
				rec.Error = "repetition_guard_intercepted"
			} else {
				toolOutput, toolErr = toolExecutor(ctx, tc.Name, tc.Arguments)
				rec.Output = toolOutput
				if toolErr != nil {
					rec.Error = toolErr.Error()
				}
			}

			var toolContent string
			if toolErr != nil {
				toolContent = fmt.Sprintf("Error: %v", toolErr)
			} else {
				toolContent = toolOutput
			}

			toolRecords = append(toolRecords, rec)

			messages = append(messages, ReActMessage{
				Role:       "tool",
				Content:    toolContent,
				ToolCallID: tc.ID,
			})
		}

		// Sliding window context pruning: if prompt tokens or character count exceeds limit,
		// prune oldest intermediate tool turn pairs (keeping System message[0] and Goal message[1])
		messages = pruneMessagesIfExceeded(messages, resp.PromptTokens, maxContextTokens)
	}

	// If step budget reached and no final output produced, force a final synthesis turn
	if finalOutput == "" {
		forcedPrompt := []ReActMessage{
			messages[0], // system
			messages[1], // goal
			{
				Role:    "user",
				Content: "Step budget reached. Synthesize all observations gathered into your comprehensive final response.",
			},
		}
		if len(messages) > 2 {
			forcedPrompt = append(forcedPrompt[:2], messages[2:]...)
			forcedPrompt = append(forcedPrompt, ReActMessage{
				Role:    "user",
				Content: "Step budget reached. Synthesize all observations gathered into your comprehensive final response.",
			})
		}

		synthResp, err := inferenceBackend.Call(ctx, forcedPrompt, nil)
		if err == nil && synthResp != nil {
			finalOutput = synthResp.Content
			totalPromptToks += synthResp.PromptTokens
			totalComplToks += synthResp.CompletionTokens
		}
	}

	return &ReActResult{
		FinalOutput:           finalOutput,
		StepCount:             stepCount,
		ToolCalls:             toolRecords,
		DurationMs:            time.Since(startTime).Milliseconds(),
		TotalPromptTokens:     totalPromptTokens(toolRecords, totalPromptToks),
		TotalCompletionTokens: totalComplToks,
	}, nil
}

func totalPromptTokens(records []ReActToolCallRecord, toks int) int {
	if toks > 0 {
		return toks
	}
	return len(records) * 50
}

// pruneMessagesIfExceeded drops oldest intermediate tool turns when prompt tokens or char length exceeds limit.
// Always strictly preserves System prompt (messages[0]) and Goal specification (messages[1]).
func pruneMessagesIfExceeded(messages []ReActMessage, promptTokens int, maxTokens int) []ReActMessage {
	if len(messages) <= 4 || maxTokens <= 0 {
		return messages
	}

	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
		for _, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			totalChars += len(tc.Name) + len(argsJSON)
		}
	}

	exceeded := promptTokens > maxTokens || totalChars > maxTokens*4
	if !exceeded {
		return messages
	}

	systemAndGoal := messages[:2]
	history := messages[2:]

	for len(history) > 2 && (promptTokens > maxTokens || totalChars > maxTokens*4) {
		dropCount := 1
		for dropCount < len(history) && history[dropCount].Role == "tool" {
			dropCount++
		}
		for i := 0; i < dropCount; i++ {
			totalChars -= len(history[i].Content)
		}
		history = history[dropCount:]
		promptTokens = totalChars / 4
	}

	res := make([]ReActMessage, 0, len(systemAndGoal)+len(history))
	res = append(res, systemAndGoal...)
	res = append(res, history...)
	return res
}
