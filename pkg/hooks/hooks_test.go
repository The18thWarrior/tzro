package hooks

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandlePreToolUse(t *testing.T) {
	inputJSON := `{
		"toolCall": {
			"name": "run_command",
			"args": {
				"CommandLine": "npm test"
			}
		},
		"stepIdx": 5,
		"conversationId": "test-123"
	}`

	var out bytes.Buffer
	err := HandlePreToolUse(strings.NewReader(inputJSON), &out, nil)
	if err != nil {
		t.Fatalf("HandlePreToolUse failed: %v", err)
	}

	var output PreToolUseOutput
	if err := json.Unmarshal(out.Bytes(), &output); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if output.Decision != "allow" {
		t.Errorf("expected allow, got %s", output.Decision)
	}
}

func TestHandleCompactOutput(t *testing.T) {
	rawLogs := `panic: runtime error
main.go:10 +0x12
runtime/panic.go:800 +0x23
runtime/proc.go:120 +0x45
`

	var out bytes.Buffer
	err := HandleCompactOutput(strings.NewReader(rawLogs), &out)
	if err != nil {
		t.Fatalf("HandleCompactOutput failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "main.go:10") {
		t.Errorf("expected user code frame preserved")
	}
	if !strings.Contains(res, "[2 framework/runtime frames elided]") {
		t.Errorf("expected elision marker, got:\n%s", res)
	}
}
