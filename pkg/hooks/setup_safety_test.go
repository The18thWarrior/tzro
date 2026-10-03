package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPreservesInvalidHermesYAML(t *testing.T) {
	t.Setenv("HERMES_HOME", "")
	for _, original := range []string{"model: [", "model: one\nmodel: two\n", "model: one\n---\nmodel: two\n"} {
		t.Run(original, func(t *testing.T) {
			opts := InstallOptions{Home: t.TempDir(), Workspace: t.TempDir(), Binary: "/opt/tzro", Targets: []string{"hermes"}}
			dir := filepath.Join(opts.Home, ".hermes")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := InstallAgents(opts); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatalf("invalid configuration overwritten: %s %v", after, err)
			}
		})
	}
}

func TestInstallReportsWriteFailureAndContinues(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	opts := InstallOptions{Home: t.TempDir(), Workspace: t.TempDir(), Binary: "/opt/tzro", Targets: []string{"claude", "codex"}}
	// A file in place of the configuration directory fails even when tests run as root.
	if err := os.WriteFile(filepath.Join(opts.Home, ".claude"), []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := InstallAgents(opts)
	if err == nil || len(results) != 2 || results[0].Status != "failed" || results[1].Status == "failed" {
		t.Fatalf("partial failure not reported: %+v %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(opts.Home, ".codex", "config.toml")); err != nil {
		t.Fatal("second client was skipped:", err)
	}
}

func TestInstallDetectsCustomClientHomes(t *testing.T) {
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "HERMES_HOME", "COPILOT_HOME", "PI_CODING_AGENT_DIR", "CODEX_HOME"} {
		t.Setenv(env, "")
	}
	for _, tc := range []struct{ client, env string }{{"claude", "CLAUDE_CONFIG_DIR"}, {"hermes", "HERMES_HOME"}, {"copilot", "COPILOT_HOME"}, {"pi-coder", "PI_CODING_AGENT_DIR"}, {"codex", "CODEX_HOME"}} {
		t.Run(tc.client, func(t *testing.T) {
			custom := t.TempDir()
			t.Setenv(tc.env, custom)
			opts := InstallOptions{Home: t.TempDir(), Workspace: t.TempDir(), Binary: "/opt/tzro", Targets: []string{"auto"}, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
			results, err := InstallAgents(opts)
			if err != nil || len(results) != 1 || string(results[0].Harness) != tc.client {
				t.Fatalf("custom location not detected: %+v %v", results, err)
			}
			for _, integration := range results[0].Integrations {
				if integration.Path == "" || (tc.client == "codex" && integration.Name == "skill") {
					continue
				}
				if !strings.HasPrefix(integration.Path, custom+string(os.PathSeparator)) {
					t.Fatalf("custom root ignored: %+v", integration)
				}
			}
		})
	}
}

func TestDetectAntigravityClientDirectories(t *testing.T) {
	for _, variant := range []string{"antigravity", "antigravity-ide", "antigravity-cli"} {
		t.Run(variant, func(t *testing.T) {
			opts := InstallOptions{Home: t.TempDir(), Workspace: t.TempDir(), LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
			if err := os.MkdirAll(filepath.Join(opts.Home, ".gemini", variant), 0755); err != nil {
				t.Fatal(err)
			}
			if !agentPresent(opts, HarnessAntigravity) {
				t.Fatal("client directory not detected")
			}
		})
	}
}
