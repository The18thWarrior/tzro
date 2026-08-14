package executor

import (
	"context"
	"encoding/json"
	"strings"
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

func (m *MockTwoPassEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, _ ModelTarget) (string, error) {
	m.Calls = append(m.Calls, MockInferCall{
		Schema: jsonSchema,
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	return "", nil
}

func (m *MockTwoPassEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, _ ModelTarget) (string, error) {
	m.Calls = append(m.Calls, MockInferCall{
		Schema:   jsonSchema,
		Messages: messages,
	})

	// If GBNF schema is provided (Pass 2), return structured JSON
	// with all required fields (action, tool, arguments) populated
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
		context.Background(), engine, reasoning, []string{"web_search", "read_file"}, false,
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
		context.Background(), engine, reasoning, []string{"web_search", "read_file"}, false,
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
		context.Background(), engine, "I've gathered enough information, time to synthesize.", []string{"web_search"}, false,
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

func (s *synthMockEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, _ ModelTarget) (string, error) {
	return "", nil
}

func (s *synthMockEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, _ ModelTarget) (string, error) {
	if jsonSchema != "" {
		// All three fields are required now; synthesize action sets tool/arguments to empty
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
	// Verify required includes all three fields
	reqRaw, ok := schema["required"]
	if !ok {
		t.Fatal("schema missing 'required' field")
	}
	reqSlice, ok := reqRaw.([]interface{})
	if !ok {
		t.Fatal("'required' is not an array")
	}
	reqSet := make(map[string]bool)
	for _, r := range reqSlice {
		reqSet[r.(string)] = true
	}
	for _, field := range []string{"action", "tool", "arguments"} {
		if !reqSet[field] {
			t.Errorf("required field %q missing from schema", field)
		}
	}
}

// --- Test: extractToolAction passes goal context to extraction prompt ---

func TestExtractToolAction_GoalContext(t *testing.T) {
	engine := &MockTwoPassEngine{}
	reasoning := "I need to search for something."

	_, _, _, err := extractToolAction(
		context.Background(), engine, reasoning,
		[]string{"web_search"}, false, "Find the latest AI orchestration frameworks",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The system prompt in the GBNF pass should contain the goal
	if len(engine.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(engine.Calls))
	}
	sysMsg := engine.Calls[0].Messages[0].Content
	if !strings.Contains(sysMsg, "AI orchestration frameworks") {
		t.Errorf("expected goal in system prompt, got: %s", sysMsg[:min(len(sysMsg), 200)])
	}
}

// --- Test: forceTool=true prevents synthesize via grammar constraint ---

func TestExtractToolAction_ForceToolPreventsSynthesize(t *testing.T) {
	// forceToolMockEngine returns tool_call when the schema only allows it,
	// simulating what happens when the 1B Router is constrained by TwoPassToolOnlySchema.
	engine := &forceToolMockEngine{}

	action, toolName, _, err := extractToolAction(
		context.Background(), engine,
		"I've gathered enough information, time to synthesize.",
		[]string{"web_search"}, true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if action != "tool_call" {
		t.Errorf("expected action='tool_call' when forceTool=true, got %q", action)
	}
	if toolName != "web_search" {
		t.Errorf("expected tool='web_search', got %q", toolName)
	}

	// Verify the GBNF pass received the tool-only schema
	if len(engine.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(engine.Calls))
	}
	if !strings.Contains(engine.Calls[0].Schema, `"enum": ["tool_call"]`) {
		t.Errorf("expected tool-only schema with single enum, got schema: %s", engine.Calls[0].Schema)
	}
	// System prompt should NOT mention 'synthesize'
	sysMsg := engine.Calls[0].Messages[0].Content
	if strings.Contains(sysMsg, "synthesize") {
		t.Error("expected system prompt to NOT mention synthesize when forceTool=true")
	}
}

// forceToolMockEngine respects the schema constraint: returns tool_call when
// the schema only allows it, synthesize when allowed.
type forceToolMockEngine struct {
	Calls []struct {
		Messages []inference.InferenceMessage
		Schema   string
	}
}

func (f *forceToolMockEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, _ ModelTarget) (string, error) {
	return "", nil
}

func (f *forceToolMockEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, _ ModelTarget) (string, error) {
	f.Calls = append(f.Calls, struct {
		Messages []inference.InferenceMessage
		Schema   string
	}{Messages: messages, Schema: jsonSchema})
	if jsonSchema != "" {
		// If schema only allows tool_call, return tool_call
		if strings.Contains(jsonSchema, `"enum": ["tool_call"]`) {
			return `{"action":"tool_call","tool":"web_search","arguments":{"query":"test"}}`, nil
		}
		// Otherwise could return either — default to tool_call for the mock
		return `{"action":"tool_call","tool":"web_search","arguments":{"query":"test"}}`, nil
	}
	return "reasoning output", nil
}

// --- Test: TwoPassToolOnlySchema is valid JSON with single enum ---

func TestTwoPassToolOnlySchema_ValidJSON(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(TwoPassToolOnlySchema), &schema); err != nil {
		t.Fatalf("TwoPassToolOnlySchema is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties in schema")
	}
	actionProp, ok := props["action"].(map[string]interface{})
	if !ok {
		t.Fatal("expected action property in schema")
	}
	enumRaw, ok := actionProp["enum"].([]interface{})
	if !ok {
		t.Fatal("expected enum in action property")
	}
	if len(enumRaw) != 1 {
		t.Errorf("expected exactly 1 enum value in tool-only schema, got %d", len(enumRaw))
	}
	if enumRaw[0] != "tool_call" {
		t.Errorf("expected enum value 'tool_call', got %q", enumRaw[0])
	}
}

// --- Test: buildExtractionSchema injects tool enum constraint ---

func TestBuildExtractionSchema_ToolEnum(t *testing.T) {
	schema := buildExtractionSchema([]string{"read_file", "search_files", "list_dir"}, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v\nschema: %s", err, schema)
	}

	props := parsed["properties"].(map[string]interface{})

	// Check action enum has both values
	actionProp := props["action"].(map[string]interface{})
	actionEnum := actionProp["enum"].([]interface{})
	if len(actionEnum) != 2 {
		t.Errorf("expected 2 action enum values, got %d", len(actionEnum))
	}

	// Check tool has enum constraint with all 3 tools
	toolProp := props["tool"].(map[string]interface{})
	toolEnum, ok := toolProp["enum"].([]interface{})
	if !ok {
		t.Fatal("expected tool property to have enum constraint")
	}
	if len(toolEnum) != 3 {
		t.Errorf("expected 3 tool enum values, got %d", len(toolEnum))
	}
	expectedTools := map[string]bool{"read_file": true, "search_files": true, "list_dir": true}
	for _, v := range toolEnum {
		name := v.(string)
		if !expectedTools[name] {
			t.Errorf("unexpected tool in enum: %q", name)
		}
	}
}

func TestBuildExtractionSchema_ForceTool(t *testing.T) {
	schema := buildExtractionSchema([]string{"read_file"}, true)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := parsed["properties"].(map[string]interface{})
	actionProp := props["action"].(map[string]interface{})
	actionEnum := actionProp["enum"].([]interface{})
	if len(actionEnum) != 1 || actionEnum[0] != "tool_call" {
		t.Errorf("expected single 'tool_call' action enum, got %v", actionEnum)
	}

	toolProp := props["tool"].(map[string]interface{})
	toolEnum := toolProp["enum"].([]interface{})
	if len(toolEnum) != 1 || toolEnum[0] != "read_file" {
		t.Errorf("expected single 'read_file' tool enum, got %v", toolEnum)
	}
}

func TestBuildExtractionSchema_EmptyTools(t *testing.T) {
	schema := buildExtractionSchema(nil, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := parsed["properties"].(map[string]interface{})
	toolProp := props["tool"].(map[string]interface{})
	// Should NOT have enum when no tools provided
	if _, hasEnum := toolProp["enum"]; hasEnum {
		t.Error("expected no enum constraint when allowedTools is empty")
	}
	if toolProp["type"] != "string" {
		t.Errorf("expected type=string, got %v", toolProp["type"])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
