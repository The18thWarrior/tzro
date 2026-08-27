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

	return InitResult{
		Harness:    HarnessCopilot,
		ConfigPath: targetDir,
		Updated:    true,
		Status:     "configured",
	}, nil
}
