package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockReActInference allows simulating LLM turns with predefined tool calls or responses.
type mockReActInference struct {
	turns     []mockTurn
	turnIndex int
}

type mockTurn struct {
	content   string
	toolCalls []ReActToolCall
}

func (m *mockReActInference) Call(ctx context.Context, messages []ReActMessage, tools []ReActToolDef) (*ReActResponse, error) {
	if m.turnIndex >= len(m.turns) {
		return &ReActResponse{
			Content: "Default completion response",
		}, nil
	}
	t := m.turns[m.turnIndex]
	m.turnIndex++
	return &ReActResponse{
		Content:          t.content,
		ToolCalls:        t.toolCalls,
		PromptTokens:     100,
		CompletionTokens: 50,
	}, nil
}

func TestReActLoop_DirectConvergence(t *testing.T) {
	mock := &mockReActInference{
		turns: []mockTurn{
			{
				content: "I have directly evaluated the task and produced the answer.",
			},
		},
	}

	cfg := ReActConfig{
		Goal:         "Summarize the project scope",
		AllowedTools: []string{"read_file"},
		StepBudget:   5,
	}

	res, err := RunReActLoop(context.Background(), cfg, mock)
	if err != nil {
		t.Fatalf("RunReActLoop failed: %v", err)
	}

	if res.StepCount != 1 {
		t.Errorf("expected StepCount=1, got %d", res.StepCount)
	}
	if len(res.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(res.ToolCalls))
	}
	if res.FinalOutput != "I have directly evaluated the task and produced the answer." {
		t.Errorf("unexpected FinalOutput: %s", res.FinalOutput)
	}
}

func TestReActLoop_MultiStepToolExecution(t *testing.T) {
	mock := &mockReActInference{
		turns: []mockTurn{
			{
				content: "Let me check the directory contents.",
				toolCalls: []ReActToolCall{
					{
						ID:   "call_1",
						Name: "mock_list",
						Arguments: map[string]interface{}{
							"path": "internal/cache",
						},
					},
				},
			},
			{
				content: "Now let me read the cache.go file.",
				toolCalls: []ReActToolCall{
					{
						ID:   "call_2",
						Name: "mock_read",
						Arguments: map[string]interface{}{
							"file": "cache.go",
						},
					},
				},
			},
			{
				content: "Final synthesis: internal/cache contains Store and Item types.",
			},
		},
	}

	cfg := ReActConfig{
		Goal:         "Analyze internal/cache",
		AllowedTools: []string{"mock_list", "mock_read"},
		StepBudget:   5,
		ToolExecutor: func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
			if name == "mock_list" {
				return `["cache.go", "metrics.go"]`, nil
			}
			if name == "mock_read" {
				return "package cache\ntype Store struct{}\ntype Item struct{}", nil
			}
			return "", nil
		},
	}

	res, err := RunReActLoop(context.Background(), cfg, mock)
	if err != nil {
		t.Fatalf("RunReActLoop failed: %v", err)
	}

	if res.StepCount != 3 {
		t.Errorf("expected StepCount=3, got %d", res.StepCount)
	}
	if len(res.ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(res.ToolCalls))
	}
	if res.FinalOutput != "Final synthesis: internal/cache contains Store and Item types." {
		t.Errorf("unexpected FinalOutput: %s", res.FinalOutput)
	}
}

func TestReActLoop_ToolErrorRecovery(t *testing.T) {
	mock := &mockReActInference{
		turns: []mockTurn{
			{
				content: "Reading nonexistent file",
				toolCalls: []ReActToolCall{
					{
						ID:   "call_err",
						Name: "mock_read",
						Arguments: map[string]interface{}{
							"file": "missing.go",
						},
					},
				},
			},
			{
				content: "File was missing, reading correct file",
				toolCalls: []ReActToolCall{
					{
						ID:   "call_ok",
						Name: "mock_read",
						Arguments: map[string]interface{}{
							"file": "found.go",
						},
					},
				},
			},
			{
				content: "Successfully recovered and synthesized from found.go",
			},
		},
	}

	cfg := ReActConfig{
		Goal:         "Read found.go",
		AllowedTools: []string{"mock_read"},
		StepBudget:   5,
		ToolExecutor: func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
			if args["file"] == "missing.go" {
				return "", fmt.Errorf("file not found: missing.go")
			}
			if args["file"] == "found.go" {
				return "package found\nconst Ready = true", nil
			}
			return "", nil
		},
	}

	res, err := RunReActLoop(context.Background(), cfg, mock)
	if err != nil {
		t.Fatalf("RunReActLoop failed: %v", err)
	}

	if res.StepCount != 3 {
		t.Errorf("expected StepCount=3, got %d", res.StepCount)
	}
	if len(res.ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Error == "" {
		t.Errorf("expected tool call 0 to record error")
	}
	if res.FinalOutput != "Successfully recovered and synthesized from found.go" {
		t.Errorf("unexpected FinalOutput: %s", res.FinalOutput)
	}
}

func TestReActLoop_RepetitionGuard(t *testing.T) {
	toolExecCount := 0
	mock := &mockReActInference{
		turns: []mockTurn{
			{
				content: "Call 1",
				toolCalls: []ReActToolCall{
					{ID: "c1", Name: "mock_read", Arguments: map[string]interface{}{"file": "loop.go"}},
				},
			},
			{
				content: "Call 2 (repeat)",
				toolCalls: []ReActToolCall{
					{ID: "c2", Name: "mock_read", Arguments: map[string]interface{}{"file": "loop.go"}},
				},
			},
			{
				content: "Call 3 (repeat triggered guard)",
				toolCalls: []ReActToolCall{
					{ID: "c3", Name: "mock_read", Arguments: map[string]interface{}{"file": "loop.go"}},
				},
			},
			{
				content: "Breaking out of repetition loop with final synthesis",
			},
		},
	}

	cfg := ReActConfig{
		Goal:         "Avoid repetition",
		AllowedTools: []string{"mock_read"},
		StepBudget:   6,
		ToolExecutor: func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
			toolExecCount++
			return "content of loop.go", nil
		},
	}

	res, err := RunReActLoop(context.Background(), cfg, mock)
	if err != nil {
		t.Fatalf("RunReActLoop failed: %v", err)
	}

	// The tool should only execute twice; the 3rd identical call is intercepted by RepetitionGuard
	if toolExecCount != 2 {
		t.Errorf("expected tool to execute exactly 2 times before guard interception, got %d", toolExecCount)
	}
	if res.FinalOutput != "Breaking out of repetition loop with final synthesis" {
		t.Errorf("unexpected FinalOutput: %s", res.FinalOutput)
	}
}

func TestReActLoop_StepBudgetEnforcement(t *testing.T) {
	mock := &mockReActInference{
		turns: []mockTurn{
			{
				content: "Turn 1 tool call",
				toolCalls: []ReActToolCall{
					{ID: "c1", Name: "mock_read", Arguments: map[string]interface{}{"file": "f1.go"}},
				},
			},
			{
				content: "Turn 2 tool call",
				toolCalls: []ReActToolCall{
					{ID: "c2", Name: "mock_read", Arguments: map[string]interface{}{"file": "f2.go"}},
				},
			},
			{
				content: "Turn 3 tool call (hits budget of 3)",
				toolCalls: []ReActToolCall{
					{ID: "c3", Name: "mock_read", Arguments: map[string]interface{}{"file": "f3.go"}},
				},
			},
			{
				content: "Forced final synthesis delivered upon budget exhaustion",
			},
		},
	}

	cfg := ReActConfig{
		Goal:         "Read many files within budget",
		AllowedTools: []string{"mock_read"},
		StepBudget:   3,
		ToolExecutor: func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
			return "some file content", nil
		},
	}

	res, err := RunReActLoop(context.Background(), cfg, mock)
	if err != nil {
		t.Fatalf("RunReActLoop failed: %v", err)
	}

	if res.StepCount != 3 {
		t.Errorf("expected StepCount=3, got %d", res.StepCount)
	}
	if len(res.ToolCalls) != 3 {
		t.Errorf("expected 3 tool calls executed before budget exit, got %d", len(res.ToolCalls))
	}
	if res.FinalOutput != "Forced final synthesis delivered upon budget exhaustion" {
		t.Errorf("unexpected FinalOutput: %s", res.FinalOutput)
	}
}

func TestReActLoop_SlidingWindowContextPruning(t *testing.T) {
	var capturedMessages [][]ReActMessage

	inspectingMock := &inspectingReActInference{
		onCall: func(msgs []ReActMessage) (*ReActResponse, error) {
			// Save copy of messages for inspection
			copied := make([]ReActMessage, len(msgs))
			copy(copied, msgs)
			capturedMessages = append(capturedMessages, copied)

			if len(capturedMessages) == 1 {
				return &ReActResponse{
					Content: "Reading huge file 1",
					ToolCalls: []ReActToolCall{
						{ID: "c1", Name: "mock_read", Arguments: map[string]interface{}{"file": "huge1.go"}},
					},
					PromptTokens: 200,
				}, nil
			}
			if len(capturedMessages) == 2 {
				return &ReActResponse{
					Content: "Reading huge file 2",
					ToolCalls: []ReActToolCall{
						{ID: "c2", Name: "mock_read", Arguments: map[string]interface{}{"file": "huge2.go"}},
					},
					PromptTokens: 800,
				}, nil
			}
			return &ReActResponse{
				Content:      "Final output after pruned history",
				PromptTokens: 300,
			}, nil
		},
	}

	cfg := ReActConfig{
		Goal:             "Prune older context when tokens exceed threshold",
		AllowedTools:     []string{"mock_read"},
		StepBudget:       5,
		MaxContextTokens: 600, // Trigger pruning on step 2
		ToolExecutor: func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
			// Return 2000 chars of dummy content to inflate context
			return strings.Repeat("Extracted code observation data... ", 100), nil
		},
	}

	res, err := RunReActLoop(context.Background(), cfg, inspectingMock)
	if err != nil {
		t.Fatalf("RunReActLoop failed: %v", err)
	}

	if res.FinalOutput != "Final output after pruned history" {
		t.Errorf("unexpected FinalOutput: %s", res.FinalOutput)
	}

	// Verify that on step 3, message[0] (System) and message[1] (Goal) are preserved,
	// but the oldest intermediate tool turn (huge1.go) was pruned from context.
	lastCallMsgs := capturedMessages[len(capturedMessages)-1]
	if lastCallMsgs[0].Role != "system" {
		t.Errorf("expected msg[0] to be system, got %s", lastCallMsgs[0].Role)
	}
	if lastCallMsgs[1].Role != "user" {
		t.Errorf("expected msg[1] to be user, got %s", lastCallMsgs[1].Role)
	}
	
	hasHuge1 := false
	for _, m := range lastCallMsgs {
		if strings.Contains(m.Content, "huge1") {
			hasHuge1 = true
		}
		for _, tc := range m.ToolCalls {
			if strings.Contains(fmt.Sprintf("%v", tc.Arguments), "huge1") {
				hasHuge1 = true
			}
		}
	}
	if hasHuge1 {
		t.Errorf("expected huge1 to be pruned from message history due to token limit")
	}
}

type inspectingReActInference struct {
	onCall func(messages []ReActMessage) (*ReActResponse, error)
}

func (i *inspectingReActInference) Call(ctx context.Context, messages []ReActMessage, tools []ReActToolDef) (*ReActResponse, error) {
	return i.onCall(messages)
}


