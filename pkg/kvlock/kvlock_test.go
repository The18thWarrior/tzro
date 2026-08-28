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
