package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleClaudeHooks(t *testing.T) {
	// PreTool: grep command
	preInput := `{"tool_name":"Bash","tool_input":{"command":"grep -rn 'func main' ."}}`
	var preOut bytes.Buffer
	if err := HandleClaudePreToolUse(strings.NewReader(preInput), &preOut, nil); err != nil {
		t.Fatalf("HandleClaudePreToolUse failed: %v", err)
	}

	var preResp ClaudePreToolUseOutput
	if err := json.Unmarshal(preOut.Bytes(), &preResp); err != nil {
		t.Fatalf("Unmarshal pre-tool failed: %v", err)
	}
	if preResp.Decision != "allow" {
		t.Errorf("expected decision allow, got %s", preResp.Decision)
	}
	if !strings.Contains(preResp.Reason, "tzro probe") {
		t.Errorf("expected tzro probe suggestion in reason, got %s", preResp.Reason)
	}

	// PostTool: compact output
	postInput := `{"tool_name":"Bash","tool_output":"panic: runtime error\nmain.go:10 +0x12\nruntime/panic.go:800 +0x23\n"}`
	var postOut bytes.Buffer
	if err := HandleClaudePostToolUse(strings.NewReader(postInput), &postOut, nil); err != nil {
		t.Fatalf("HandleClaudePostToolUse failed: %v", err)
	}

	var postResp ClaudePostToolUseOutput
	if err := json.Unmarshal(postOut.Bytes(), &postResp); err != nil {
		t.Fatalf("Unmarshal post-tool failed: %v", err)
	}
	outStr, ok := postResp.ToolOutput.(string)
	if !ok || !strings.Contains(outStr, "main.go:10") {
		t.Errorf("expected compacted output with user frame preserved, got %v", postResp.ToolOutput)
	}
}

func TestHandleHermesHooks(t *testing.T) {
	// PreTool
	preInput := `{"tool":"execute_command","parameters":{"command":"find . -name '*.go'"}}`
	var preOut bytes.Buffer
	if err := HandleHermesPreTool(strings.NewReader(preInput), &preOut, nil); err != nil {
		t.Fatalf("HandleHermesPreTool failed: %v", err)
	}

	var preResp HermesPreToolOutput
	if err := json.Unmarshal(preOut.Bytes(), &preResp); err != nil {
		t.Fatalf("Unmarshal pre-tool failed: %v", err)
	}
	if !preResp.Proceed {
		t.Errorf("expected proceed true")
	}
	if !strings.Contains(preResp.Reason, "tzro probe") {
		t.Errorf("expected probe suggestion, got %s", preResp.Reason)
	}

	// PostTool
	postInput := `{"tool":"execute_command","output":"panic: test err\nmain.go:5\nruntime/proc.go:10\n"}`
	var postOut bytes.Buffer
	if err := HandleHermesPostTool(strings.NewReader(postInput), &postOut, nil); err != nil {
		t.Fatalf("HandleHermesPostTool failed: %v", err)
	}

	var postResp HermesPostToolOutput
	if err := json.Unmarshal(postOut.Bytes(), &postResp); err != nil {
		t.Fatalf("Unmarshal post-tool failed: %v", err)
	}
	outStr, ok := postResp.Output.(string)
	if !ok || !strings.Contains(outStr, "main.go:5") {
		t.Errorf("expected compacted output, got %v", postResp.Output)
	}
}

func TestHandleCopilotHooks(t *testing.T) {
	preInput := `{"tool":"runInTerminal","input":{"command":"grep -E 'pattern' file.txt"}}`
	var preOut bytes.Buffer
	if err := HandleCopilotPreTool(strings.NewReader(preInput), &preOut, nil); err != nil {
		t.Fatalf("HandleCopilotPreTool failed: %v", err)
	}

	var preResp CopilotPreToolOutput
	if err := json.Unmarshal(preOut.Bytes(), &preResp); err != nil {
		t.Fatalf("Unmarshal pre-tool failed: %v", err)
	}
	if !preResp.Allow {
		t.Errorf("expected allow true")
	}

	postInput := `{"tool":"runInTerminal","output":"panic: copilot err\napp.go:12\nruntime/panic.go:10\n"}`
	var postOut bytes.Buffer
	if err := HandleCopilotPostTool(strings.NewReader(postInput), &postOut, nil); err != nil {
		t.Fatalf("HandleCopilotPostTool failed: %v", err)
	}

	var postResp CopilotPostToolOutput
	if err := json.Unmarshal(postOut.Bytes(), &postResp); err != nil {
		t.Fatalf("Unmarshal post-tool failed: %v", err)
	}
	outStr, ok := postResp.Output.(string)
	if !ok || !strings.Contains(outStr, "app.go:12") {
		t.Errorf("expected compacted output, got %v", postResp.Output)
	}
}

func TestHandlePiCoderHooks(t *testing.T) {
	// PreTool: grep command
	preInput := `{"tool_name":"bash","tool_input":{"command":"grep -rn 'func main' ."}}`
	var preOut bytes.Buffer
	if err := HandlePiCoderPreTool(strings.NewReader(preInput), &preOut, nil); err != nil {
		t.Fatalf("HandlePiCoderPreTool failed: %v", err)
	}

	var preResp PiCoderPreToolOutput
	if err := json.Unmarshal(preOut.Bytes(), &preResp); err != nil {
		t.Fatalf("Unmarshal pre-tool failed: %v", err)
	}
	if !preResp.Allow {
		t.Errorf("expected allow true")
	}
	if !strings.Contains(preResp.Reason, "tzro probe") {
		t.Errorf("expected probe suggestion, got %s", preResp.Reason)
	}

	// PostTool: compact output
	postInput := `{"tool_name":"bash","tool_output":"panic: runtime error\nmain.go:10 +0x12\nruntime/panic.go:800 +0x23\n"}`
	var postOut bytes.Buffer
	if err := HandlePiCoderPostTool(strings.NewReader(postInput), &postOut, nil); err != nil {
		t.Fatalf("HandlePiCoderPostTool failed: %v", err)
	}

	var postResp PiCoderPostToolOutput
	if err := json.Unmarshal(postOut.Bytes(), &postResp); err != nil {
		t.Fatalf("Unmarshal post-tool failed: %v", err)
	}
	outStr, ok := postResp.ToolOutput.(string)
	if !ok || !strings.Contains(outStr, "main.go:10") {
		t.Errorf("expected compacted output with user frame preserved, got %v", postResp.ToolOutput)
	}
}

func TestDetectAndInstallHooksWorkspace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tzro-hook-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer os.Chdir(origWd)

	results, err := DetectAndInstallHooks([]string{"all"}, true)
	if err != nil {
		t.Fatalf("DetectAndInstallHooks failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("expected 5 configured harnesses for 'all', got %d", len(results))
	}

	// Verify Claude settings.json exists
	claudeSettings := filepath.Join(tempDir, ".claude", "settings.json")
	if _, err := os.Stat(claudeSettings); err != nil {
		t.Errorf("expected .claude/settings.json created: %v", err)
	}

	// Verify Antigravity hooks.json exists
	antigravityHooks := filepath.Join(tempDir, ".agents", "hooks.json")
	if _, err := os.Stat(antigravityHooks); err != nil {
		t.Errorf("expected .agents/hooks.json created: %v", err)
	}

	// Verify Hermes hooks exist
	hermesPre := filepath.Join(tempDir, ".hermes", "hooks", "pre_tool.sh")
	if _, err := os.Stat(hermesPre); err != nil {
		t.Errorf("expected .hermes/hooks/pre_tool.sh created: %v", err)
	}

	// Verify Copilot hooks exist
	copilotPre := filepath.Join(tempDir, ".github", "hooks", "pre-tool.sh")
	if _, err := os.Stat(copilotPre); err != nil {
		t.Errorf("expected .github/hooks/pre-tool.sh created: %v", err)
	}

	// Verify Pi-Coder extension exists
	piCoderExt := filepath.Join(tempDir, ".pi", "extensions", "tzro-hook.ts")
	if _, err := os.Stat(piCoderExt); err != nil {
		t.Errorf("expected .pi/extensions/tzro-hook.ts created: %v", err)
	}

	// Verify tzro SKILL.md was created for each harness
	skillFiles := map[string]string{
		"Antigravity": filepath.Join(tempDir, ".agents", "skills", "tzro", "SKILL.md"),
		"Claude":      filepath.Join(tempDir, ".claude", "skills", "tzro", "SKILL.md"),
		"Copilot":     filepath.Join(tempDir, ".github", "skills", "tzro", "SKILL.md"),
		"Hermes":      filepath.Join(tempDir, ".agents", "skills", "tzro", "SKILL.md"),
		"Pi-Coder":    filepath.Join(tempDir, ".pi", "skills", "tzro", "SKILL.md"),
	}
	for harness, skillPath := range skillFiles {
		data, err := os.ReadFile(skillPath)
		if err != nil {
			t.Errorf("%s: expected SKILL.md at %s: %v", harness, skillPath, err)
			continue
		}
		if !strings.Contains(string(data), "name: tzro") {
			t.Errorf("%s: SKILL.md missing frontmatter", harness)
		}
	}
}
