package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestInstallClientProfiles(t *testing.T) {
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "HERMES_HOME", "COPILOT_HOME", "PI_CODING_AGENT_DIR", "CODEX_HOME"} {
		t.Setenv(env, "")
	}
	for _, client := range []string{"claude", "antigravity", "hermes", "copilot", "pi-coder"} {
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			opts := InstallOptions{Home: home, Workspace: t.TempDir(), Binary: "/opt/tzro path/bin/tzro", Targets: []string{client}}
			results, err := InstallAgents(opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || len(results[0].Integrations) < 3 {
				t.Fatalf("missing integration results: %+v", results)
			}
			for _, r := range results[0].Integrations {
				if r.Status == "unsupported" {
					if client != "pi-coder" || r.Name != "MCP" {
						t.Fatalf("unexpected unsupported capability: %+v", r)
					}
					continue
				}
				if _, err := os.Stat(r.Path); err != nil {
					t.Fatal(err)
				}
			}
			second, err := InstallAgents(opts)
			if err != nil {
				t.Fatal(err)
			}
			if second[0].Updated {
				t.Fatal("repeat initialization rewrote generated files")
			}
		})
	}
}

func TestInstallPreservesMalformedAndUserEntries(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	opts := InstallOptions{Home: home, Workspace: t.TempDir(), Binary: "/opt/tzro", Targets: []string{"claude"}}
	if err := os.WriteFile(path, []byte("{malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := InstallAgents(opts)
	if err == nil || results[0].Status != "failed" {
		t.Fatalf("failure hidden: %+v %v", results, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{malformed" {
		t.Fatal("malformed config replaced")
	}
	existing := `{"theme":"custom","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"user-check"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgents(opts); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	pre := doc["hooks"].(map[string]any)["PreToolUse"].([]any)
	if doc["theme"] != "custom" || len(pre) != 2 || !strings.Contains(fmt.Sprint(pre[0]), "user-check") {
		t.Fatalf("user data lost: %s", data)
	}
}

func TestInstallCodexPreservesConfiguration(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	original := "# user comment\nmodel = \"chosen-model\"\n[mcp_servers.existing]\ncommand = \"other-server\"\n"
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	opts := InstallOptions{Home: home, Workspace: t.TempDir(), Binary: "/opt/with space/tzro", Targets: []string{"codex"}}
	results, err := InstallAgents(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "awaiting client approval" {
		t.Fatalf("activation not disclosed: %+v", results)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), original) {
		t.Fatal("existing config or comment changed")
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcp_servers"].(map[string]any)
	if servers["tzro"].(map[string]any)["command"] != opts.Binary {
		t.Fatalf("wrong executable: %v", servers)
	}
	hooksData, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hooksCfg map[string]any
	if err := json.Unmarshal(hooksData, &hooksCfg); err != nil {
		t.Fatal(err)
	}
	pre := hooksCfg["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("wrong hooks: %v", pre)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "tzro", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgents(opts); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("reinstall changed config")
	}
	after, _ = os.ReadFile(filepath.Join(dir, "hooks.json"))
	if string(after) != string(hooksData) {
		t.Fatal("reinstall duplicated hooks")
	}
}

func TestInstallDetectionAndModifiedSkill(t *testing.T) {
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "HERMES_HOME", "COPILOT_HOME", "PI_CODING_AGENT_DIR", "CODEX_HOME"} {
		t.Setenv(env, "")
	}
	home, work := t.TempDir(), t.TempDir()
	for _, dir := range []string{filepath.Join(work, ".agents"), filepath.Join(work, ".github")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	opts := InstallOptions{Home: home, Workspace: work, Binary: "/opt/tzro", Targets: []string{"auto"}, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	results, err := InstallAgents(opts)
	if err != nil || len(results) != 0 {
		t.Fatalf("generic workspace falsely detected: %+v %v", results, err)
	}
	opts.Targets = []string{"codex"}
	if _, err := InstallAgents(opts); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(home, ".agents", "skills", "tzro", "SKILL.md")
	if err := os.WriteFile(skill, []byte("user-customized skill"), 0644); err != nil {
		t.Fatal(err)
	}
	results, err = InstallAgents(opts)
	if err == nil || results[0].Status != "failed" {
		t.Fatal("modified skill silently replaced")
	}
	data, _ := os.ReadFile(skill)
	if string(data) != "user-customized skill" {
		t.Fatal("custom skill lost")
	}
}

func TestInstallPreservesModifiedHook(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	opts := InstallOptions{Home: t.TempDir(), Workspace: t.TempDir(), Binary: "/opt/tzro", Targets: []string{"codex"}}
	if _, err := InstallAgents(opts); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opts.Home, ".codex", "hooks.json")
	data, _ := os.ReadFile(path)
	modified := strings.Replace(string(data), `"timeout": 10`, `"timeout": 20`, 1)
	if err := os.WriteFile(path, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgents(opts); err == nil {
		t.Fatal("modified hook accepted or duplicated")
	}
	after, _ := os.ReadFile(path)
	if string(after) != modified {
		t.Fatal("modified hook overwritten")
	}
}

func TestInstallPreservesInlineTOMLServers(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	opts := InstallOptions{Home: t.TempDir(), Workspace: t.TempDir(), Binary: "/opt/tzro", Targets: []string{"codex"}}
	dir := filepath.Join(opts.Home, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	original := []byte("mcp_servers = { existing = { command = 'user-server' } }\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgents(opts); err == nil {
		t.Fatal("inline table extension must fail without corrupting configuration")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatalf("existing inline table changed: %s %v", after, err)
	}
}
