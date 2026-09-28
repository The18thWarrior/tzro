package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScript(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tzro-install-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	realBinPath := filepath.Join(tempDir, "tzro_bin")
	buildCmd := exec.Command("go", "build", "-o", realBinPath, "./cmd/tzro")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build real tzro binary: %v", err)
	}

	fakeHome := filepath.Join(tempDir, "fakehome")
	_ = os.MkdirAll(fakeHome, 0o755)
	if err := os.MkdirAll(filepath.Join(fakeHome, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}

	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", installer)
	cmd.Dir = fakeHome
	cmd.Env = append(os.Environ(),
		"TZRO_INSTALL_DIR="+tempDir,
		"TZRO_SOURCE_BIN="+realBinPath,
		"HOME="+fakeHome,
		"CODEX_HOME="+filepath.Join(fakeHome, ".codex"),
		"CLAUDE_CONFIG_DIR=", "HERMES_HOME=", "COPILOT_HOME=", "PI_CODING_AGENT_DIR=", "TZRO_NO_MODIFY_PATH=1",
	)

	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)
	if err != nil {
		t.Fatalf("install.sh failed with error: %v\nOutput:\n%s", err, output)
	}

	// Verify binary was installed
	installedBin := filepath.Join(tempDir, "bin", "tzro")
	info, err := os.Stat(installedBin)
	if err != nil {
		t.Fatalf("expected installed tzro binary at %s, got error: %v", installedBin, err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("expected tzro binary to be executable")
	}

	if !strings.Contains(output, "TZRO v2 INSTALLATION COMPLETE") {
		t.Errorf("expected completion message in output, got:\n%s", output)
	}
	if !strings.Contains(output, "awaiting client approval") {
		t.Fatal("Codex activation requirement missing")
	}
	if err := os.WriteFile(filepath.Join(fakeHome, "example.go"), []byte("package example\nfunc InstallProbe() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	probe := exec.Command(installedBin, "probe", "InstallProbe")
	probe.Env = cmd.Env
	probe.Dir = fakeHome
	if out, err := probe.CombinedOutput(); err != nil || !strings.Contains(string(out), "InstallProbe") {
		t.Fatalf("installed probe: %v\n%s", err, out)
	}
	mcp := exec.Command(installedBin, "mcp")
	mcp.Env = cmd.Env
	mcp.Dir = fakeHome
	mcp.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"clientInfo\":{\"name\":\"install-test\",\"version\":\"1\"}}}\n")
	stdout, err := mcp.Output()
	if err != nil {
		t.Fatalf("MCP without optional runtimes: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &response); err != nil {
		t.Fatalf("invalid MCP response: %s (%v)", stdout, err)
	}
	if response["result"] == nil || response["error"] != nil {
		t.Fatalf("MCP initialization failed: %s", stdout)
	}
	if err := os.WriteFile(filepath.Join(fakeHome, ".codex", "config.toml"), []byte("invalid = ["), 0600); err != nil {
		t.Fatal(err)
	}
	bad := exec.Command(installedBin, "init", "--hooks", "codex")
	bad.Env = cmd.Env
	bad.Dir = fakeHome
	if out, err := bad.CombinedOutput(); err == nil || !strings.Contains(string(out), "incomplete") {
		t.Fatalf("setup failure not propagated: %v\n%s", err, out)
	}
}
