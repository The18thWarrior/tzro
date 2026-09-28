package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func prepare(ctx context.Context, cfg Config, profile Profile, task Task, index int) (*prepared, error) {
	start := time.Now()
	root := filepath.Join(cfg.WorkDir, fmt.Sprintf("%03d-%s", index, profile))
	p := &prepared{result: Result{Profile: profile, Task: task.ID, Home: filepath.Join(root, "home"), Workspace: filepath.Join(root, "workspace"), Status: "setup_incomplete", Hooks: "absent", MCP: "unsupported by this Pi recipe"}}
	defer func() { p.result.SetupMS = time.Since(start).Milliseconds() }()
	p.result.PromptSHA256 = digest([]byte(task.Prompt))
	fixture, _ := json.Marshal(task.Files)
	p.result.FixtureSHA256 = digest(fixture)
	toolsDir := filepath.Join(root, "tools")
	for _, dir := range []string{p.result.Home, p.result.Workspace, toolsDir, filepath.Join(p.result.Home, "tmp"), filepath.Join(p.result.Home, ".pi", "agent")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return p, err
		}
	}
	// A controlled PATH prevents the host's tzro from leaking into Baseline.
	for _, name := range []string{"sh", "bash", "env", "node", "go", "git", "rg", "grep", "find", "ls", "cat", "sed", "awk", "sort", "head", "tail", "cut", "tr", "wc", "xargs", "uname", "mkdir", "mktemp", "dirname", "chmod", "cp", "mv", "rm", "curl", "sha256sum", "shasum", "which", "sleep", "tee", "touch", "pwd"} {
		if path, err := exec.LookPath(name); err == nil {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return p, err
			}
			if err := os.Symlink(absolute, filepath.Join(toolsDir, name)); err != nil {
				return p, err
			}
		}
	}
	if err := os.Symlink(cfg.PiBinary, filepath.Join(toolsDir, "pi")); err != nil {
		return p, err
	}
	p.env = []string{
		"HOME=" + p.result.Home, "PATH=" + toolsDir, "SHELL=/bin/sh", "LC_ALL=C",
		"TMPDIR=" + filepath.Join(p.result.Home, "tmp"), "GOROOT=" + runtime.GOROOT(),
		"GOCACHE=" + filepath.Join(p.result.Home, "go-cache"), "GOPATH=" + filepath.Join(p.result.Home, "go"),
		"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off",
		"XDG_CONFIG_HOME=" + filepath.Join(p.result.Home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(p.result.Home, ".cache"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(p.result.Home, ".pi", "agent"), "PI_OFFLINE=1", "PI_TELEMETRY=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
	}
	for name, data := range task.Files {
		clean := filepath.Clean(name)
		if filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return p, fmt.Errorf("task path escapes workspace: %q", name)
		}
		path := filepath.Join(p.result.Workspace, clean)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return p, err
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			return p, err
		}
	}
	if profile != Baseline {
		env := append(append([]string{}, p.env...), "TZRO_SOURCE_BIN="+cfg.TzroBinary, "TZRO_NO_MODIFY_PATH=1")
		cmd := command(ctx, env, p.result.Workspace, "/bin/sh", cfg.Installer)
		if out, err := cmd.CombinedOutput(); err != nil {
			return p, fmt.Errorf("standard installation failed: %w: %s", err, out)
		}
		p.binary = filepath.Join(p.result.Home, ".tzro", "bin", "tzro")
		p.env = append(p.env, "PATH="+filepath.Dir(p.binary)+":"+toolsDir)
		p.result.Hooks = "configured; invocation not yet observed"
		var err error
		p.result.SkillSHA256, err = fileDigest(filepath.Join(p.result.Home, ".pi", "agent", "skills", "tzro", "SKILL.md"))
		if err != nil {
			return p, err
		}
		p.result.HookSHA256, err = fileDigest(filepath.Join(p.result.Home, ".pi", "agent", "extensions", "tzro-hook.ts"))
		if err != nil {
			return p, err
		}
	}
	if err := writeModelConfig(p, cfg, cfg.BaseURL); err != nil {
		return p, err
	}
	p.result.Status = "configured"
	return p, nil
}

func writeModelConfig(p *prepared, cfg Config, baseURL string) error {
	provider := map[string]any{"baseUrl": baseURL, "api": "openai-completions", "apiKey": "TZRO_BENCH_API_KEY", "models": []any{map[string]any{"id": cfg.Model, "reasoning": false, "input": []string{"text"}, "contextWindow": 128000, "maxTokens": 8192}}}
	data, err := json.MarshalIndent(map[string]any{"providers": map[string]any{"tzro-workflow": provider}}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.result.Home, ".pi", "agent", "models.json"), data, 0600)
}
