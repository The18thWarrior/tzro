package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HarnessType identifies the agent platform.
type HarnessType string

const (
	HarnessAntigravity HarnessType = "antigravity"
	HarnessClaude      HarnessType = "claude"
	HarnessHermes      HarnessType = "hermes"
	HarnessCopilot     HarnessType = "copilot"
	HarnessPiCoder     HarnessType = "pi-coder"
)

// InitResult reports the outcome of initializing hooks for a harness.
type InitResult struct {
	Harness HarnessType `json:"harness"`
	ConfigPath string   `json:"configPath"`
	Updated    bool     `json:"updated"`
	Status     string   `json:"status"`
}

// DetectAndInstallHooks detects installed environments and installs appropriate hooks.
func DetectAndInstallHooks(targets []string, isWorkspace bool) ([]InitResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	cwd, _ := os.Getwd()

	all := false
	selected := make(map[string]bool)
	for _, t := range targets {
		lower := strings.ToLower(strings.TrimSpace(t))
		if lower == "all" {
			all = true
			break
		}
		if lower == "auto" {
			// auto-detection mode
			break
		}
		selected[lower] = true
	}

	var results []InitResult

	// 1. Antigravity
	if all || selected[string(HarnessAntigravity)] || (len(selected) == 0 && isAntigravityPresent(home, cwd)) {
		res, err := SetupAntigravityHooks(home, cwd, isWorkspace)
		if err != nil {
			results = append(results, InitResult{Harness: HarnessAntigravity, Status: fmt.Sprintf("failed: %v", err)})
		} else {
			results = append(results, res)
		}
	}

	// 2. Claude Code
	if all || selected[string(HarnessClaude)] || (len(selected) == 0 && isClaudePresent(home, cwd)) {
		res, err := SetupClaudeHooks(home, cwd, isWorkspace)
		if err != nil {
			results = append(results, InitResult{Harness: HarnessClaude, Status: fmt.Sprintf("failed: %v", err)})
		} else {
			results = append(results, res)
		}
	}

	// 3. Hermes Agent
	if all || selected[string(HarnessHermes)] || (len(selected) == 0 && isHermesPresent(home, cwd)) {
		res, err := SetupHermesHooks(home, cwd, isWorkspace)
		if err != nil {
			results = append(results, InitResult{Harness: HarnessHermes, Status: fmt.Sprintf("failed: %v", err)})
		} else {
			results = append(results, res)
		}
	}

	// 4. GitHub Copilot
	if all || selected[string(HarnessCopilot)] || (len(selected) == 0 && isCopilotPresent(cwd)) {
		res, err := SetupCopilotHooks(cwd)
		if err != nil {
			results = append(results, InitResult{Harness: HarnessCopilot, Status: fmt.Sprintf("failed: %v", err)})
		} else {
			results = append(results, res)
		}
	}

	// 5. Pi-Coder
	if all || selected[string(HarnessPiCoder)] || (len(selected) == 0 && isPiCoderPresent(home, cwd)) {
		res, err := SetupPiCoderHooks(home, cwd, isWorkspace)
		if err != nil {
			results = append(results, InitResult{Harness: HarnessPiCoder, Status: fmt.Sprintf("failed: %v", err)})
		} else {
			results = append(results, res)
		}
	}

	return results, nil
}

func isAntigravityPresent(home, cwd string) bool {
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, ".agents")); err == nil {
		return true
	}
	return false
}

func isClaudePresent(home, cwd string) bool {
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude")); err == nil {
		return true
	}
	return false
}

func isHermesPresent(home, cwd string) bool {
	if _, err := os.Stat(filepath.Join(home, ".hermes")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, "hermes.json")); err == nil {
		return true
	}
	return false
}

func isCopilotPresent(cwd string) bool {
	if _, err := os.Stat(filepath.Join(cwd, ".github")); err == nil {
		return true
	}
	return false
}

func isPiCoderPresent(home, cwd string) bool {
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, ".pi")); err == nil {
		return true
	}
	return false
}

// SetupAntigravityHooks configures hooks.json for Google Antigravity.
func SetupAntigravityHooks(home, cwd string, isWorkspace bool) (InitResult, error) {
	var targetFile string
	if isWorkspace {
		targetDir := filepath.Join(cwd, ".agents")
		_ = os.MkdirAll(targetDir, 0755)
		targetFile = filepath.Join(targetDir, "hooks.json")
	} else {
		targetDir := filepath.Join(home, ".gemini", "config")
		_ = os.MkdirAll(targetDir, 0755)
		targetFile = filepath.Join(targetDir, "hooks.json")
	}

	existing := make(map[string]any)
	if data, err := os.ReadFile(targetFile); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	hooksObj, ok := existing["hooks"].(map[string]any)
	if !ok {
		hooksObj = make(map[string]any)
	}

	hooksObj["PreToolUse"] = []map[string]string{
		{"matcher": ".*", "command": "tzro hook antigravity pre-tool"},
	}
	hooksObj["PostToolUse"] = []map[string]string{
		{"matcher": ".*", "command": "tzro hook antigravity post-tool"},
	}
	existing["hooks"] = hooksObj

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return InitResult{}, err
	}

	if err := os.WriteFile(targetFile, append(data, '\n'), 0644); err != nil {
		return InitResult{}, err
	}

	// Write tzro skill to the Antigravity skills directory.
	var skillsDir string
	if isWorkspace {
		skillsDir = filepath.Join(cwd, ".agents", "skills")
	} else {
		skillsDir = filepath.Join(home, ".gemini", "config", "skills")
	}
	_ = WriteTzroSkill(skillsDir)

	return InitResult{
		Harness:    HarnessAntigravity,
		ConfigPath: targetFile,
		Updated:    true,
		Status:     "configured",
	}, nil
}

// SetupClaudeHooks configures settings.json for Claude Code.
func SetupClaudeHooks(home, cwd string, isWorkspace bool) (InitResult, error) {
	var targetFile string
	if isWorkspace {
		targetDir := filepath.Join(cwd, ".claude")
		_ = os.MkdirAll(targetDir, 0755)
		targetFile = filepath.Join(targetDir, "settings.json")
	} else {
		targetDir := filepath.Join(home, ".claude")
		_ = os.MkdirAll(targetDir, 0755)
		targetFile = filepath.Join(targetDir, "settings.json")
	}

	existing := make(map[string]any)
	if data, err := os.ReadFile(targetFile); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	hooksObj, ok := existing["hooks"].(map[string]any)
	if !ok {
		hooksObj = make(map[string]any)
	}

	hooksObj["PreToolUse"] = []map[string]string{
		{"matcher": ".*", "command": "tzro hook claude pre-tool"},
	}
	hooksObj["PostToolUse"] = []map[string]string{
		{"matcher": ".*", "command": "tzro hook claude post-tool"},
	}
	existing["hooks"] = hooksObj

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return InitResult{}, err
	}

	if err := os.WriteFile(targetFile, append(data, '\n'), 0644); err != nil {
		return InitResult{}, err
	}

	// Write tzro skill to the Claude Code skills directory.
	var skillsDir string
	if isWorkspace {
		skillsDir = filepath.Join(cwd, ".claude", "skills")
	} else {
		skillsDir = filepath.Join(home, ".claude", "skills")
	}
	_ = WriteTzroSkill(skillsDir)

	return InitResult{
		Harness:    HarnessClaude,
		ConfigPath: targetFile,
		Updated:    true,
		Status:     "configured",
	}, nil
}

// SetupHermesHooks configures hooks for Hermes Agent.
func SetupHermesHooks(home, cwd string, isWorkspace bool) (InitResult, error) {
	var targetDir string
	if isWorkspace {
		targetDir = filepath.Join(cwd, ".hermes", "hooks")
	} else {
		targetDir = filepath.Join(home, ".hermes", "hooks")
	}
	_ = os.MkdirAll(targetDir, 0755)

	preScript := filepath.Join(targetDir, "pre_tool.sh")
	postScript := filepath.Join(targetDir, "post_tool.sh")

	_ = os.WriteFile(preScript, []byte("#!/bin/sh\nexec tzro hook hermes pre-tool\n"), 0755)
	_ = os.WriteFile(postScript, []byte("#!/bin/sh\nexec tzro hook hermes post-tool\n"), 0755)

	// Also update hermes config.json if present
	configFile := filepath.Join(filepath.Dir(targetDir), "config.json")
	existing := make(map[string]any)
	if data, err := os.ReadFile(configFile); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	existing["hooks"] = map[string]string{
		"pre_tool_call":  preScript,
		"post_tool_call": postScript,
	}
	if data, err := json.MarshalIndent(existing, "", "  "); err == nil {
		_ = os.WriteFile(configFile, append(data, '\n'), 0644)
	}

	// Write tzro skill to the shared .agents/skills/ directory (Hermes loads from here).
	var skillsDir string
	if isWorkspace {
		skillsDir = filepath.Join(cwd, ".agents", "skills")
	} else {
		skillsDir = filepath.Join(home, ".agents", "skills")
	}
	_ = WriteTzroSkill(skillsDir)

	return InitResult{
		Harness:    HarnessHermes,
		ConfigPath: targetDir,
		Updated:    true,
		Status:     "configured",
	}, nil
}

// SetupCopilotHooks configures GitHub Copilot hooks directory.
func SetupCopilotHooks(cwd string) (InitResult, error) {
	targetDir := filepath.Join(cwd, ".github", "hooks")
	_ = os.MkdirAll(targetDir, 0755)

	preScript := filepath.Join(targetDir, "pre-tool.sh")
	postScript := filepath.Join(targetDir, "post-tool.sh")

	_ = os.WriteFile(preScript, []byte("#!/bin/sh\nexec tzro hook copilot pre-tool\n"), 0755)
	_ = os.WriteFile(postScript, []byte("#!/bin/sh\nexec tzro hook copilot post-tool\n"), 0755)

	hooksJSON := filepath.Join(targetDir, "hooks.json")
	cfg := map[string]any{
		"preTool":  preScript,
		"postTool": postScript,
	}
	if data, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(hooksJSON, append(data, '\n'), 0644)
	}

	// Write tzro skill to the Copilot skills directory.
	copilotSkills := filepath.Join(cwd, ".github", "skills")
	_ = WriteTzroSkill(copilotSkills)

	return InitResult{
		Harness:    HarnessCopilot,
		ConfigPath: targetDir,
		Updated:    true,
		Status:     "configured",
	}, nil
}

// piCoderExtensionTS is the TypeScript extension template for Pi-Coder.
// It spawns tzro as a child process for pre-tool and post-tool lifecycle events.
const piCoderExtensionTS = `// tzro-hook.ts — Tzro Token Shield extension for Pi-Coder
// Auto-generated by tzro init. Do not edit manually.
import { execSync } from "child_process";

function callTzro(event: string, payload: unknown): unknown {
  try {
    const input = JSON.stringify(payload);
    const result = execSync(` + "`tzro hook pi-coder ${event}`" + `, {
      input,
      encoding: "utf-8",
      timeout: 5000,
    });
    return JSON.parse(result.trim());
  } catch {
    // Fail-open: if tzro is unavailable, allow the tool call
    return event === "pre-tool" ? { allow: true } : {};
  }
}

export default function hook(pi: any): void {
  pi.on("tool_call", async (event: any, ctx: any) => {
    const payload = {
      session_id: ctx?.sessionId ?? "",
      tool_name: event.toolName ?? event.tool ?? "",
      tool_input: event.toolInput ?? event.input ?? {},
    };
    const result: any = callTzro("pre-tool", payload);
    if (result && result.allow === false) {
      ctx.block(result.reason ?? "Blocked by Tzro Token Shield");
    }
  });

  pi.on("tool_result", async (event: any) => {
    const payload = {
      tool_name: event.toolName ?? event.tool ?? "",
      tool_input: event.toolInput ?? event.input ?? {},
      tool_output: event.toolOutput ?? event.output ?? "",
    };
    const result: any = callTzro("post-tool", payload);
    if (result && result.tool_output !== undefined) {
      event.toolOutput = result.tool_output;
    }
  });
}
`

// SetupPiCoderHooks configures a TypeScript extension for Pi-Coder.
func SetupPiCoderHooks(home, cwd string, isWorkspace bool) (InitResult, error) {
	var targetDir string
	if isWorkspace {
		targetDir = filepath.Join(cwd, ".pi", "extensions")
	} else {
		targetDir = filepath.Join(home, ".pi", "agent", "extensions")
	}
	_ = os.MkdirAll(targetDir, 0755)

	extFile := filepath.Join(targetDir, "tzro-hook.ts")
	if err := os.WriteFile(extFile, []byte(piCoderExtensionTS), 0644); err != nil {
		return InitResult{}, err
	}

	// Write tzro skill to the Pi-Coder skills directory.
	var skillsDir string
	if isWorkspace {
		skillsDir = filepath.Join(cwd, ".pi", "skills")
	} else {
		skillsDir = filepath.Join(home, ".pi", "agent", "skills")
	}
	_ = WriteTzroSkill(skillsDir)

	return InitResult{
		Harness:    HarnessPiCoder,
		ConfigPath: extFile,
		Updated:    true,
		Status:     "configured",
	}, nil
}
