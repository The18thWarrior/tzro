package workflow

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Run defaults to local preflight, without any provider request.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if cfg.SetupTimeout <= 0 {
		cfg.SetupTimeout = 60 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 20
	}
	if cfg.Run && (cfg.Prices == nil || cfg.MaxCost <= 0 || cfg.APIKey == "") {
		return nil, fmt.Errorf("execution requires explicit token prices, positive max cost, and an API key")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || !strings.HasSuffix(strings.TrimSuffix(base.Path, "/"), "/v1") {
		return nil, fmt.Errorf("base URL must be an HTTP(S) endpoint ending in /v1, without credentials, query, or fragment")
	}
	if cfg.Model == "" || cfg.TzroBinary == "" || cfg.PiBinary == "" || cfg.Installer == "" {
		return nil, fmt.Errorf("model, client, binary, and installer are required")
	}
	if math.IsNaN(cfg.MaxCost) || math.IsInf(cfg.MaxCost, 0) {
		return nil, fmt.Errorf("max cost must be finite")
	}
	if cfg.Prices != nil {
		for _, price := range []float64{cfg.Prices.Input, cfg.Prices.Output, cfg.Prices.CacheRead, cfg.Prices.CacheWrite} {
			if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				return nil, fmt.Errorf("prices must be finite and nonnegative")
			}
		}
	}
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = []Profile{Baseline, Standard, Full}
	}
	if len(cfg.Tasks) == 0 {
		return nil, fmt.Errorf("at least one task is required")
	}
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("an empty work directory is required")
	}
	for _, path := range []*string{&cfg.WorkDir, &cfg.PiBinary, &cfg.TzroBinary, &cfg.Installer} {
		*path, err = filepath.Abs(*path)
		if err != nil {
			return nil, err
		}
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(cfg.WorkDir))
	if err != nil {
		return nil, err
	}
	for ; ; parent = filepath.Dir(parent) {
		for _, marker := range []string{".git", ".pi", ".agents", "AGENTS.md"} {
			if _, err := os.Stat(filepath.Join(parent, marker)); err == nil {
				return nil, fmt.Errorf("work directory has ancestor agent/project resources at %s; use a new directory under the system temporary directory", parent)
			}
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	seen := map[Profile]bool{}
	for _, p := range cfg.Profiles {
		if p != Baseline && p != Standard && p != Full {
			return nil, fmt.Errorf("unknown profile %q", p)
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate profile %q", p)
		}
		seen[p] = true
	}
	if err := os.Mkdir(cfg.WorkDir, 0700); err != nil {
		return nil, err
	}
	report := &Report{Schema: RecipeVersion, Ready: true}
	report.Metadata = metadata(ctx, cfg)
	var cells []*prepared
	var tasks []Task
	for _, task := range cfg.Tasks {
		for _, profile := range cfg.Profiles {
			setupCtx, cancel := context.WithTimeout(ctx, cfg.SetupTimeout)
			p, err := prepare(setupCtx, cfg, profile, task, len(report.Results))
			if err == nil && report.ClientVersion == "" {
				out, versionErr := command(setupCtx, p.env, p.result.Workspace, cfg.PiBinary, "--version").Output()
				if versionErr != nil {
					err = versionErr
				} else {
					report.ClientVersion = strings.TrimSpace(string(out))
				}
			}
			if err == nil {
				start := time.Now()
				if profile == Full {
					err = configureFull(cfg, p)
					if err == nil && report.Metadata["runtimes"] == nil {
						report.Metadata["runtimes"], err = runtimeIdentity(cfg)
					}
					if err == nil {
						err = checkRuntimes(setupCtx, p)
					}
					if err == nil {
						_, stop, proxyErr := startProxy(setupCtx, cfg, p)
						stop()
						err = proxyErr
					}
				}
				if err == nil {
					p.result.SkillLoaded, err = discoverSkill(setupCtx, cfg, p)
				}
				if err == nil && profile == Full {
					// Readiness uses a separate process; remove its store before task timing.
					for _, suffix := range []string{"", "-shm", "-wal"} {
						removeErr := os.Remove(filepath.Join(p.result.Home, ".tzro", "token_shield.db") + suffix)
						if removeErr != nil && !os.IsNotExist(removeErr) {
							err = removeErr
							break
						}
					}
				}
				p.result.PreflightMS = time.Since(start).Milliseconds()
				if err == nil && p.result.SkillLoaded != (profile != Baseline) {
					err = fmt.Errorf("installed skill discovery does not match %s", profile)
				}
			}
			cancel()
			if err != nil {
				p.result.Error = cleanText(cfg, err.Error())
				p.result.Status = "setup_incomplete"
				report.Ready = false
			} else {
				p.result.Status = "ready"
			}
			report.Results = append(report.Results, p.result)
			cells = append(cells, p)
			tasks = append(tasks, task)
		}
	}
	// Every selected profile must pass preflight before any paid request.
	if cfg.Run && report.Ready {
		spent := 0.0
		usageUnknown := false
		for i, p := range cells {
			if usageUnknown || spent >= cfg.MaxCost {
				p.result.Status = "not_run"
				p.result.Error = "suite cost limit reached"
				if usageUnknown {
					p.result.Error = "prior cell has incomplete usage; remaining cost unknown"
				}
			} else {
				cellCfg := cfg
				cellCfg.MaxCost -= spent
				runTask(ctx, cellCfg, tasks[i], p)
				usageUnknown = !p.result.Usage.Complete
				if p.result.Usage.EstimatedCostUSD != nil {
					spent += *p.result.Usage.EstimatedCostUSD
				}
			}
			report.Results[i] = p.result
		}
	} else if cfg.Run {
		for i := range report.Results {
			if report.Results[i].Status == "ready" {
				report.Results[i].Status, report.Results[i].Error = "not_run", "another selected profile failed preflight"
			}
		}
	}
	return report, nil
}
