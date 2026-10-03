package turnreduction

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const NativeModel = "gemini-3.8-flash-low"

type ReadinessReport struct {
	Schema            string            `json:"schema"`
	OfflineReady      bool              `json:"offline_ready"`
	APIKeyPresent     bool              `json:"api_key_present"`
	Model             string            `json:"model"`
	ModelAvailability string            `json:"model_availability"`
	ClientVersion     string            `json:"client_version"`
	Toolchain         map[string]string `json:"toolchain"`
	Fixtures          ValidationReport  `json:"fixture_validation"`
	Problems          []string          `json:"problems,omitempty"`
	NativeMCPList     string            `json:"native_mcp_list,omitempty"`
	CostPolicy        string            `json:"cost_policy"`
}

// PrepareEvaluation performs local checks without sending a model request.
func PrepareEvaluation(ctx context.Context, cfg EvaluationConfig) ReadinessReport {
	result := ReadinessReport{Schema: "tzro.native-readiness.v1", APIKeyPresent: cfg.APIKey != "", Model: NativeModel, ModelAvailability: "unobserved", Toolchain: map[string]string{}, CostPolicy: "launches are bounded; provider retries and actual charges are not bounded or observed"}
	if runtime.GOOS == "windows" {
		result.Problems = append(result.Problems, "native descendant cleanup is not supported on Windows")
	}
	if cfg.Model != "" && cfg.Model != NativeModel {
		result.Problems = append(result.Problems, "the agreed native model is "+NativeModel)
	}
	checks := map[string][]string{"go": {"go", "version"}, "python": {"python3", "--version"}, "node": {"node", "--version"}, "typescript": {"tsc", "--version"}}
	for name, argv := range checks {
		version, err := localVersion(ctx, argv)
		if err != nil {
			result.Problems = append(result.Problems, name+": "+err.Error())
		} else {
			result.Toolchain[name] = version
		}
	}
	if cfg.Offline {
		result.ClientVersion = "scripted offline client"
	} else {
		version, err := localVersion(ctx, []string{cfg.ClientPath, "--version"})
		if err != nil {
			result.Problems = append(result.Problems, "Antigravity: "+err.Error())
		} else {
			result.ClientVersion = version
		}
	}
	for name, path := range map[string]string{"production Tzro binary": cfg.TzroPath, "simple helper": cfg.HelperPath, "launch guard": cfg.GuardPath} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			result.Problems = append(result.Problems, name+" is unavailable")
		}
	}
	if cfg.TzroGuidancePath != "" {
		if data, err := os.ReadFile(cfg.TzroGuidancePath); err != nil || strings.TrimSpace(string(data)) == "" {
			result.Problems = append(result.Problems, "declared Tzro guidance is unavailable or empty")
		}
	}
	if cfg.InvocationRecorderPath != "" {
		if info, err := os.Stat(cfg.InvocationRecorderPath); err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
			result.Problems = append(result.Problems, "invocation recorder is unavailable or not executable")
		}
	}
	needsMCP := len(cfg.Conditions) == 0
	for _, condition := range cfg.Conditions {
		needsMCP = needsMCP || condition == ConditionTzro
	}
	if !cfg.Offline && needsMCP && len(result.Problems) == 0 {
		var err error
		result.NativeMCPList, err = probeNativeInstallation(ctx, cfg)
		if err != nil {
			result.Problems = append(result.Problems, err.Error())
		}
	}
	if len(result.Problems) == 0 {
		result.Fixtures = ValidateFixtures(ctx, cfg.Fixtures)
	}
	result.OfflineReady = len(result.Problems) == 0 && len(cfg.Fixtures) == 9 && result.Fixtures.Passed
	return result
}

func localVersion(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", fmt.Errorf("executable is missing")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "AGY_CLI_DISABLE_AUTO_UPDATE=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GeminiKey reads one credential as data. It never executes a shell or loads other keys.
func GeminiKey(envFile string) (string, error) {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key, nil
	}
	if envFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		if !ok || strings.TrimSpace(name) != "GEMINI_API_KEY" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		return value, nil
	}
	return "", nil
}
