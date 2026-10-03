package turnreduction

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type EvaluationContract struct {
	ClientUpdatePolicy string            `json:"client_update_policy"`
	GoCache            string            `json:"go_cache"`
	Schema             string            `json:"schema"`
	Model              string            `json:"model"`
	Provider           string            `json:"provider"`
	PermissionMode     string            `json:"permission_mode"`
	TaskTimeoutSeconds float64           `json:"task_timeout_s"`
	Offline            bool              `json:"offline"`
	MaxLaunches        int               `json:"max_launches"`
	Schedule           []ScheduledCell   `json:"schedule"`
	FileHashes         map[string]string `json:"file_hashes"`
	ClientVersion      string            `json:"client_version"`
	Toolchain          map[string]string `json:"toolchain"`
}

func freezeContract(cfg EvaluationConfig) (EvaluationContract, string, error) {
	contract := EvaluationContract{Schema: "tzro.native-contract.v1", Model: cfg.Model, Provider: "gemini", PermissionMode: "always-proceed", TaskTimeoutSeconds: cfg.TaskTimeout.Seconds(), Offline: cfg.Offline, MaxLaunches: cfg.MaxLaunches, Schedule: BalancedSchedule(cfg.Fixtures, cfg.Conditions), FileHashes: map[string]string{}}
	contract.ClientUpdatePolicy = "AGY_CLI_DISABLE_AUTO_UPDATE=true; executable SHA-256 checked before every task"
	contract.GoCache = cfg.GoCache
	contract.ClientVersion = cfg.ClientVersion
	contract.Toolchain = cfg.Toolchain
	for name, path := range map[string]string{"client": cfg.ClientPath, "tzro": cfg.TzroPath, "simple_helper": cfg.HelperPath, "launch_guard": cfg.GuardPath, "tzro_guidance": cfg.TzroGuidancePath, "invocation_recorder": cfg.InvocationRecorderPath} {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return contract, "", err
		}
		contract.FileHashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	for _, f := range cfg.Fixtures {
		data, err := json.Marshal(f)
		if err != nil {
			return contract, "", err
		}
		contract.FileHashes[f.ID+"/contract"] = fmt.Sprintf("%x", sha256.Sum256(data))
		for name, path := range map[string]string{"subject": f.SubjectDir, "grading": f.GradingDir} {
			hashes, err := treeHashes(path)
			if err != nil {
				return contract, "", err
			}
			for rel, hash := range hashes {
				contract.FileHashes[filepath.ToSlash(filepath.Join(f.ID, name, rel))] = hash
			}
		}
	}
	data, err := json.Marshal(contract)
	if err != nil {
		return contract, "", err
	}
	return contract, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
