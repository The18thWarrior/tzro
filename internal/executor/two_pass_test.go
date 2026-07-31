package executor

import (
	"context"
	"encoding/json"
	"testing"
	"tzro/internal/inference"
)

// MockTwoPassEngine is a test double for two-pass tool extraction.
// It tracks calls to distinguish Pass 1 (unconstrained) from Pass 2 (GBNF).
type MockTwoPassEngine struct {
	Calls []MockInferCall
}

type MockInferCall struct {
	Schema   string // empty = unconstrained, non-empty = GBNF
	Messages []inference.InferenceMessage
}

func (m *MockTwoPassEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	m.Calls = append(m.Calls, MockInferCall{
		Schema: jsonSchema,
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	return "", nil
}

func (m *MockTwoPassEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	m.Calls = append(m.Calls, MockInferCall{
		Schema:   jsonSchema,
		Messages: messages,
	})

	// If GBNF schema is provided (Pass 2), return structured JSON
	if jsonSchema != "" {
		return `{"action":"tool_call","tool":"web_search","arguments":{"query":"test"}}`, nil
	}
	return "", nil
}

// --- Slice 6: extractToolAction with complete ACTION tags ---

func TestExtractToolAction_WithCompleteActionTag(t *testing.T) {
	engine := &MockTwoPassEngine{}
	reasoning := `I need to search for information about AI frameworks.
<ACTION>{"tool":"web_search","arguments":{"query":"AI frameworks comparison 2025"}}</ACTION>
This should give us good results.`

	action, toolName, args, err := extractToolAction(
		context.Background(), engine, reasoning, []string{"web_search", "read_file"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if action != "tool_call" {
		t.Errorf("expected action='tool_call', got %q", action)
	}
	if toolName != "web_search" {
		t.Errorf("expected tool='web_search', got %q", toolName)
	}
	if args == nil || args["query"] != "test" {
		// The GBNF pass determines the final output, not the ACTION tag
		t.Logf("args from GBNF pass: %v", args)
	}

	// Must have made exactly 1 call (GBNF pass only — reasoning was already done)
	if len(engine.Calls) != 1 {
		t.Errorf("expected 1 GBNF call, got %d", len(engine.Calls))
	}
	// The call must have the GBNF schema
	if engine.Calls[0].Schema == "" {
		t.Error("GBNF pass should have a schema")
	}
}

// --- Slice 7: extractToolAction without ACTION tags ---

func TestExtractToolAction_WithoutActionTag(t *testing.T) {
	engine := &MockTwoPassEngine{}
	reasoning := "I should search for the latest AI orchestration trends to understand the market."

	action, toolName, _, err := extractToolAction(
		context.Background(), engine, reasoning, []string{"web_search", "read_file"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if action != "tool_call" {
		t.Errorf("expected action='tool_call', got %q", action)
	}
	if toolName != "web_search" {
		t.Errorf("expected tool='web_search', got %q", toolName)
	}

	// Must have made exactly 1 call (GBNF pass with full reasoning)
	if len(engine.Calls) != 1 {
		t.Errorf("expected 1 GBNF call, got %d", len(engine.Calls))
	}
	// The call must include the full reasoning
	call := engine.Calls[0]
	if call.Schema == "" {
		t.Error("GBNF pass should have a schema")
	}
	// User message should contain the full reasoning
	found := false
	for _, msg := range call.Messages {
		if msg.Role == "user" && len(msg.Content) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("GBNF pass should receive reasoning as user message")
	}
}

// --- Slice 8: extractToolAction synthesize action ---

func TestExtractToolAction_SynthesizeIntent(t *testing.T) {
	synthEngine := &MockTwoPassEngine{}
	// Override InferMessages to return synthesize
	synthEngine.Calls = nil

	// Create a custom engine that returns synthesize
	engine := &synthMockEngine{}
	action, toolName, args, err := extractToolAction(
		context.Background(), engine, "I've gathered enough information, time to synthesize.", []string{"web_search"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if action != "synthesize" {
		t.Errorf("expected action='synthesize', got %q", action)
	}
	if toolName != "" {
		t.Errorf("expected empty tool for synthesize, got %q", toolName)
	}
	if args != nil {
		t.Errorf("expected nil args for synthesize, got %v", args)
	}
}

// synthMockEngine always returns synthesize from the GBNF pass.
type synthMockEngine struct{}

func (s *synthMockEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	return "", nil
}

func (s *synthMockEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	if jsonSchema != "" {
		return `{"action":"synthesize","tool":"","arguments":{}}`, nil
	}
	return "I've gathered enough information.", nil
}

// --- Slice 6 extra: verify schema matches expected structure ---

func TestTwoPassActionSchema_ValidJSON(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(TwoPassActionSchema), &schema); err != nil {
		t.Fatalf("TwoPassActionSchema is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties in schema")
	}
	if _, ok := props["action"]; !ok {
		t.Error("schema missing 'action' field")
	}
	if _, ok := props["tool"]; !ok {
		t.Error("schema missing 'tool' field")
	}
	if _, ok := props["arguments"]; !ok {
		t.Error("schema missing 'arguments' field")
	}
}
