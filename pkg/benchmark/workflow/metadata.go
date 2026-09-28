package workflow

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func treeDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return fileDigest(path)
	}
	files := map[string]string{}
	err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != path && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// Downloaded snapshots can use symlinks to regular weights.
		info, err := os.Stat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("model contains non-regular file %s", entry.Name())
		}
		hash, err := fileDigest(name)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(path, name)
		files[relative] = hash
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("model directory is empty")
	}
	data, _ := json.Marshal(files)
	return digest(data), nil
}

func metadata(ctx context.Context, cfg Config) map[string]any {
	m := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339), "model": cfg.Model, "provider_base_url": cfg.BaseURL,
		"os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "go_version": runtime.Version(),
		"recipe": RecipeVersion, "installation_source": "explicit local binary through install.sh",
		"cache_policy": "fresh client, workspace, Go cache and tzro store per cell; readiness workers stop before tasks; OS and provider caches uncontrolled",
		"prices":       cfg.Prices, "cost_source": "estimate from native client token usage and caller-supplied prices; incomplete usage is flagged",
		"cost_guard": "checked after each reported assistant message; in-flight usage can exceed the limit",
		"max_turns":  cfg.MaxTurns, "task_timeout_seconds": cfg.Timeout.Seconds(),
		"model_context_window": 128000, "model_max_output_tokens": 8192,
		"download_cost": "not measured; models must be provisioned before running this recipe",
	}
	if runtime.GOOS == "darwin" {
		for key, name := range map[string]string{"hardware_model": "hw.model", "memory_bytes": "hw.memsize", "cpu_model": "machdep.cpu.brand_string"} {
			if out, err := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", name).Output(); err == nil {
				m[key] = strings.TrimSpace(string(out))
			}
		}
	} else if runtime.GOOS == "linux" {
		for path, prefix := range map[string]string{"/proc/cpuinfo": "model name", "/proc/meminfo": "MemTotal"} {
			data, _ := os.ReadFile(path)
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, prefix) {
					m[prefix] = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					break
				}
			}
		}
	}
	for name, path := range map[string]string{"tzro_binary": cfg.TzroBinary, "installer": cfg.Installer, "client_binary": cfg.PiBinary} {
		hash, err := fileDigest(path)
		if err == nil {
			m[name+"_sha256"] = hash
		} else {
			m[name+"_identity_error"] = err.Error()
		}
	}
	if info, err := buildinfo.ReadFile(cfg.TzroBinary); err == nil {
		for _, setting := range info.Settings {
			if strings.HasPrefix(setting.Key, "vcs.") {
				m["binary_"+setting.Key] = setting.Value
			}
		}
	}
	repo := filepath.Dir(cfg.Installer)
	for key, args := range map[string][]string{
		"source_revision": {"rev-parse", "HEAD"}, "source_status": {"status", "--porcelain", "--untracked-files=normal"},
		"source_diff_sha256": {"diff", "--binary", "HEAD"},
	} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.Output(); err == nil {
			if key == "source_diff_sha256" {
				m[key] = digest(out)
			} else {
				m[key] = strings.TrimSpace(string(out))
			}
		}
	}
	return m
}

func runtimeIdentity(cfg Config) (map[string]any, error) {
	m := map[string]any{"decision_version": cfg.Full.DecisionVersion, "extractor_version": cfg.Full.ExtractorVersion}
	for key, path := range map[string]string{"decision_binary": cfg.Full.DecisionBin, "decision_model": cfg.Full.DecisionModel, "extractor_binary": cfg.Full.ExtractorBin, "extractor_model": cfg.Full.ExtractorModel} {
		hash, err := treeDigest(path)
		if err != nil {
			return nil, fmt.Errorf("%s identity: %w", key, err)
		}
		m[key+"_sha256"] = hash
	}
	scripts := map[string]string{}
	for _, arg := range cfg.Full.ExtractorArgs {
		if filepath.IsAbs(arg) {
			hash, err := fileDigest(arg)
			if err == nil {
				scripts[filepath.Base(arg)] = hash
			}
		}
	}
	m["extractor_argument_files_sha256"] = scripts
	args, _ := json.Marshal(cfg.Full.ExtractorArgs)
	m["extractor_arguments_sha256"] = digest(args)
	return m, nil
}
