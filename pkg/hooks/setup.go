package hooks

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InstallOptions supplies installation boundaries; no process working directory changes are needed.
type InstallOptions struct {
	Home, Workspace, Binary string
	Targets                 []string
	WorkspaceOnly           bool
	LookPath                func(string) (string, error)
}

type IntegrationResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

type agentSetup struct {
	opts   InstallOptions
	result InitResult
	errors []error
}

func (a *agentSetup) step(name, path, pending string, apply func() (bool, error)) {
	changed, err := apply()
	r := IntegrationResult{Name: name, Path: path, Status: "already configured"}
	if changed {
		r.Status = "configured"
		a.result.Updated = true
	}
	if pending != "" {
		if changed {
			r.Status = "awaiting client approval"
		}
		r.Message = pending
	}
	if err != nil {
		r.Status = "failed"
		r.Message = err.Error()
		a.errors = append(a.errors, fmt.Errorf("%s %s: %w", a.result.Harness, name, err))
	}
	if a.result.ConfigPath == "" {
		a.result.ConfigPath = path
	}
	a.result.Integrations = append(a.result.Integrations, r)
}
func (a *agentSetup) skill(dir string) {
	path := filepath.Join(dir, "tzro", "SKILL.md")
	refPath := filepath.Join(dir, "tzro", "REFERENCE.md")
	a.step("skill", path, "", func() (bool, error) {
		refChanged, err := writeOwnedFile(refPath, []byte(tzroReferenceMD), 0644)
		if err != nil {
			return false, err
		}
		skillChanged, err := writeOwnedFile(path, []byte(tzroSkillMD), 0644)
		if err != nil {
			return false, err
		}
		return refChanged || skillChanged, nil
	})
}
func (a *agentSetup) hookCommand(event string) string {
	return shellQuote(a.opts.Binary) + " hook " + string(a.result.Harness) + " native-" + event
}
func (a *agentSetup) nestedHooks(path, pending string) {
	a.step("hooks", path, pending, func() (bool, error) {
		return updateJSON(path, func(doc map[string]any) error {
			h, err := object(doc, "hooks")
			if err != nil {
				return err
			}
			for _, event := range []string{"PreToolUse", "PostToolUse"} {
				phase := "pre-tool"
				if event == "PostToolUse" {
					phase = "post-tool"
				}
				entry := map[string]any{"matcher": ".*", "hooks": []any{map[string]any{"type": "command", "command": a.hookCommand(phase), "timeout": 10}}}
				if err := mergeHook(h, event, entry, string(a.result.Harness)); err != nil {
					return err
				}
			}
			return nil
		})
	})
}
func (a *agentSetup) finish() (InitResult, error) {
	a.result.Status = "already configured"
	if a.result.Updated {
		a.result.Status = "configured"
	}
	for _, r := range a.result.Integrations {
		if r.Status == "awaiting client approval" {
			a.result.Status = r.Status
		}
	}
	if len(a.errors) > 0 {
		a.result.Status = "failed"
	}
	return a.result, errors.Join(a.errors...)
}

func configDir(opts InstallOptions, envName, relative string) string {
	if opts.WorkspaceOnly {
		return filepath.Join(opts.Workspace, relative)
	}
	if dir := os.Getenv(envName); envName != "" && dir != "" {
		return dir
	}
	return filepath.Join(opts.Home, relative)
}

// InstallAgents configures each requested client independently and reports partial failures.
func InstallAgents(opts InstallOptions) ([]InitResult, error) {
	if opts.Home == "" || opts.Workspace == "" || !filepath.IsAbs(opts.Binary) {
		return nil, fmt.Errorf("home, workspace and absolute binary path are required")
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	targets, err := installationTargets(opts)
	if err != nil {
		return nil, err
	}
	var results []InitResult
	var failures []error
	for _, target := range targets {
		a := agentSetup{opts: opts, result: InitResult{Harness: target}}
		switch target {
		case HarnessClaude:
			a.claude()
		case HarnessAntigravity:
			a.antigravity()
		case HarnessHermes:
			a.hermes()
		case HarnessCopilot:
			a.copilot()
		case HarnessPiCoder:
			a.piCoder()
		case HarnessCodex:
			dir := configDir(opts, "CODEX_HOME", ".codex")
			skillDir := filepath.Join(opts.Home, ".agents", "skills")
			if opts.WorkspaceOnly {
				skillDir = filepath.Join(opts.Workspace, ".agents", "skills")
			}
			a.skill(skillDir)
			path := filepath.Join(dir, "config.toml")
			a.step("MCP", path, "", func() (bool, error) { return addTOMLServer(path, opts.Binary) })
			a.nestedHooks(filepath.Join(dir, "hooks.json"), "Restart Codex and review tzro hooks with /hooks; activation is not verified.")
		default:
			a.errors = append(a.errors, fmt.Errorf("unsupported agent: %s", target))
		}
		result, err := a.finish()
		results = append(results, result)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return results, errors.Join(failures...)
}

func installationTargets(opts InstallOptions) ([]HarnessType, error) {
	known := []HarnessType{HarnessAntigravity, HarnessClaude, HarnessHermes, HarnessCopilot, HarnessPiCoder, HarnessCodex}
	selected := map[HarnessType]bool{}
	targets := opts.Targets
	if len(targets) == 0 {
		targets = []string{"auto"}
	}
	for _, target := range targets {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "all" {
			for _, h := range known {
				selected[h] = true
			}
			continue
		}
		if target == "auto" {
			for _, h := range known {
				if agentPresent(opts, h) {
					selected[h] = true
				}
			}
			continue
		}
		valid := false
		for _, h := range known {
			if string(h) == target {
				selected[h] = true
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("unknown agent %q", target)
		}
	}
	var result []HarnessType
	for _, h := range known {
		if selected[h] {
			result = append(result, h)
		}
	}
	return result, nil
}
func agentPresent(opts InstallOptions, h HarnessType) bool {
	if h == HarnessAntigravity && !opts.WorkspaceOnly {
		for _, variant := range []string{"antigravity-ide", "antigravity-cli"} {
			if info, err := os.Stat(filepath.Join(opts.Home, ".gemini", variant)); err == nil && info.IsDir() {
				return true
			}
		}
	}
	dirs := map[HarnessType]string{HarnessAntigravity: ".gemini/antigravity", HarnessClaude: ".claude", HarnessHermes: ".hermes", HarnessCopilot: ".copilot", HarnessPiCoder: ".pi/agent", "codex": ".codex"}
	envs := map[HarnessType]string{HarnessClaude: "CLAUDE_CONFIG_DIR", HarnessHermes: "HERMES_HOME", HarnessCopilot: "COPILOT_HOME", HarnessPiCoder: "PI_CODING_AGENT_DIR", "codex": "CODEX_HOME"}
	dir := configDir(opts, envs[h], dirs[h])
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return true
	}
	commands := map[HarnessType]string{HarnessAntigravity: "antigravity", HarnessClaude: "claude", HarnessHermes: "hermes", HarnessCopilot: "copilot", HarnessPiCoder: "pi", "codex": "codex"}
	_, err := opts.LookPath(commands[h])
	return err == nil
}
