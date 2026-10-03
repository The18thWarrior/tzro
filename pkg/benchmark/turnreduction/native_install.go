package turnreduction

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"tzro/pkg/hooks"
)

// Probe the actual client's configuration reader without a model prompt or key.
// An installed JSON file alone does not prove that this client can discover it.
func probeNativeInstallation(ctx context.Context, cfg EvaluationConfig) (string, error) {
	root, err := os.MkdirTemp("", "tzro-native-install-probe-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	home, workspace := filepath.Join(root, "home"), filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return "", err
	}
	if _, err := hooks.InstallAgents(hooks.InstallOptions{Home: home, Workspace: workspace, Binary: cfg.TzroPath, Targets: []string{"antigravity"}}); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.ClientPath, "mcp", "list")
	cleanup := ownProcessGroup(cmd)
	defer cleanup()
	cmd.Dir = workspace
	cmd.Env = nativeEnvironment(home, "")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("native MCP visibility check: %w", err)
	}
	if !regexp.MustCompile(`(?m)^tzro\s+stdio\s+enabled\s+`).Match(output) {
		return string(output), fmt.Errorf("native MCP visibility check: client did not list enabled Tzro server")
	}
	return string(output), nil
}
