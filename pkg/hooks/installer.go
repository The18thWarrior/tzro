package hooks

import (
	"os"
	"os/exec"
)

type HarnessType string

const (
	HarnessAntigravity HarnessType = "antigravity"
	HarnessClaude      HarnessType = "claude"
	HarnessHermes      HarnessType = "hermes"
	HarnessCopilot     HarnessType = "copilot"
	HarnessPiCoder     HarnessType = "pi-coder"
	HarnessCodex       HarnessType = "codex"
)

type InitResult struct {
	Harness      HarnessType         `json:"harness"`
	ConfigPath   string              `json:"configPath"`
	Updated      bool                `json:"updated"`
	Status       string              `json:"status"`
	Integrations []IntegrationResult `json:"integrations,omitempty"`
}

func installationOptions(targets []string, workspace bool) (InstallOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return InstallOptions{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return InstallOptions{}, err
	}
	binary, err := os.Executable()
	if err != nil {
		return InstallOptions{}, err
	}
	return InstallOptions{Home: home, Workspace: cwd, Binary: binary, Targets: targets, WorkspaceOnly: workspace, LookPath: exec.LookPath}, nil
}

// DetectAndInstallHooks retains the original CLI API and now configures all native integrations.
func DetectAndInstallHooks(targets []string, workspace bool) ([]InitResult, error) {
	opts, err := installationOptions(targets, workspace)
	if err != nil {
		return nil, err
	}
	return InstallAgents(opts)
}

// DetectAgents is read-only. Diagnostics must not initialize or rewrite configuration.
func DetectAgents() ([]HarnessType, error) {
	opts, err := installationOptions([]string{"auto"}, false)
	if err != nil {
		return nil, err
	}
	return installationTargets(opts)
}
