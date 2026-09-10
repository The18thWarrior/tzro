package kvlock

import (
	"encoding/json"
	"testing"
)

func TestLockGuard_NormalizeAnthropic(t *testing.T) {
	g := NewLockGuard()

	payloadTurn1 := `{
		"model": "claude-3-5-sonnet",
		"system": "You are a coding assistant.   \n",
		"tools": [
			{"name": "zebra_tool"},
			{"name": "alpha_tool"}
		],
		"messages": [
			{"role": "user", "content": "Turn 1 request"}
		]
	}`

	norm1, hash1, err := g.NormalizeAnthropic([]byte(payloadTurn1))
	if err != nil {
		t.Fatalf("NormalizeAnthropic failed: %v", err)
	}

	payloadTurn2 := `{
		"model": "claude-3-5-sonnet",
		"system": "You are a coding assistant.",
		"tools": [
			{"name": "alpha_tool"},
			{"name": "zebra_tool"}
		],
		"messages": [
			{"role": "user", "content": "Turn 1 request"},
			{"role": "assistant", "content": "Turn 1 answer"},
			{"role": "user", "content": "Turn 2 request"}
		]
	}`

	_, hash2, err := g.NormalizeAnthropic([]byte(payloadTurn2))
	if err != nil {
		t.Fatalf("NormalizeAnthropic failed: %v", err)
	}

	// Verify that the prefix hashes are identical between turn 1 and turn 2!
	if hash1 != hash2 {
		t.Errorf("expected identical prefix hash, got turn 1=%s, turn 2=%s", hash1, hash2)
	}

	// Verify tools were sorted in alphabetical order in normalized output
	var parsed AnthropicPayload
	if err := json.Unmarshal(norm1, &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	firstTool := parsed.Tools[0].(map[string]any)
	if firstTool["name"] != "alpha_tool" {
		t.Errorf("expected first tool to be alpha_tool, got %v", firstTool["name"])
	}
}

func TestLockGuard_UnknownFieldsAndFidelity(t *testing.T) {
	g := NewLockGuard()

	// OpenAI test with reasoning_effort, tool_call_id, cache_control, custom unknown top-level and nested fields
	rawOpenAI := `{
		"model": "gpt-4o",
		"reasoning_effort": "high",
		"response_format": {"type": "json_schema", "json_schema": {"name": "test", "strict": true}},
		"unknown_custom_flag": 42,
		"messages": [
			{
				"role": "user",
				"content": "Hello",
				"cache_control": {"type": "ephemeral"},
				"nested_unknown": {"foo": "bar"}
			},
			{
				"role": "assistant",
				"content": null,
				"tool_calls": [
					{
						"id": "call_12345abc",
						"type": "function",
						"function": {"name": "custom_fn", "arguments": "{\"x\":1}"}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_12345abc",
				"content": "Result 1"
			}
		],
		"tools": [
			{"type": "function", "function": {"name": "zeta_fn"}},
			{"type": "function", "function": {"name": "alpha_fn"}}
		]
	}`

	normOpenAI, _, err := g.NormalizeOpenAI([]byte(rawOpenAI))
	if err != nil {
		t.Fatalf("NormalizeOpenAI failed: %v", err)
	}

	var parsedOpenAI map[string]any
	if err := json.Unmarshal(normOpenAI, &parsedOpenAI); err != nil {
		t.Fatalf("Failed to unmarshal normalized OpenAI payload: %v", err)
	}

	if parsedOpenAI["reasoning_effort"] != "high" {
		t.Errorf("expected reasoning_effort 'high', got %v", parsedOpenAI["reasoning_effort"])
	}
	if parsedOpenAI["unknown_custom_flag"] != float64(42) {
		t.Errorf("expected unknown_custom_flag 42, got %v", parsedOpenAI["unknown_custom_flag"])
	}

	msgs := parsedOpenAI["messages"].([]any)
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["tool_call_id"] != "call_12345abc" {
		t.Errorf("expected tool_call_id 'call_12345abc', got %v", toolMsg["tool_call_id"])
	}

	userMsg := msgs[0].(map[string]any)
	cc, ok := userMsg["cache_control"].(map[string]any)
	if !ok || cc["type"] != "ephemeral" {
		t.Errorf("expected cache_control type ephemeral, got %v", userMsg["cache_control"])
	}

	// Verify fail-open on malformed JSON
	badJSON := []byte(`{ broken json`)
	normBad, _, errBad := g.NormalizeOpenAI(badJSON)
	if errBad == nil {
		t.Errorf("expected error on malformed JSON")
	}
	if string(normBad) != string(badJSON) {
		t.Errorf("expected fail-open raw payload return, got %s", string(normBad))
	}
}
