package hooks

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeClaudeOutputPreservesShape(t *testing.T) {
	input := map[string]any{"tool_name": "Bash", "tool_response": map[string]any{"stdout": strings.Repeat("INFO progress\n", 200) + "ERROR: main.go:42 broken\n", "stderr": "", "interrupted": false, "exitCode": 1}}
	raw, _ := json.Marshal(input)
	var out bytes.Buffer
	if err := HandleNativeHook("claude", "post-tool", bytes.NewReader(raw), &out, nil); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	updated := response["hookSpecificOutput"].(map[string]any)["updatedToolOutput"].(map[string]any)
	if updated["exitCode"] != float64(1) || updated["interrupted"] != false {
		t.Fatal("tool metadata lost")
	}
	if !strings.Contains(updated["stdout"].(string), "ERROR: main.go:42 broken") {
		t.Fatal("failure evidence lost")
	}
}

func TestNativeHooksRetainClientPermissionChecks(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, client := range []string{"antigravity", "claude", "codex", "copilot", "hermes"} {
		t.Run(client, func(t *testing.T) {
			var out bytes.Buffer
			if err := HandleNativeHook(client, "pre-tool", strings.NewReader(`{"toolCall":{"name":"run_command","args":{"CommandLine":"echo hello"}}}`), &out, nil); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if client == "antigravity" {
				if result["decision"] != "ask" {
					t.Fatalf("normal client approval bypassed: %s", out.String())
				}
			} else if len(result) != 0 {
				t.Fatalf("expected default client permission handling: %s", out.String())
			}
		})
	}
}

func TestNativeHooksMapDeniedPaths(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct{ client, payload, decision string }{
		{"claude", `{"tool_name":"Read","tool_input":{"file_path":".env"}}`, `"permissionDecision":"deny"`},
		{"copilot", `{"toolName":"view","toolArgs":"{\"path\":\".env\"}"}`, `"permissionDecision":"deny"`},
		{"hermes", `{"tool_name":"read_file","tool_input":{"path":".env"}}`, `"action":"block"`},
		{"codex", `{"tool_name":"read_file","tool_input":{"path":".env"}}`, `"permissionDecision":"deny"`},
		{"antigravity", `{"toolCall":{"name":"view_file","args":{"AbsolutePath":".env"}}}`, `"decision":"deny"`},
	} {
		t.Run(tc.client, func(t *testing.T) {
			var out bytes.Buffer
			if err := HandleNativeHook(tc.client, "pre-tool", strings.NewReader(tc.payload), &out, nil); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.decision) {
				t.Fatalf("privacy denial missing: %s", out.String())
			}
		})
	}
}
