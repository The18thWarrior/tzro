package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"tzro/pkg/benchmark/workflow"
)

func newWorkflowBenchCmd() *cobra.Command {
	var cfg workflow.Config
	var profiles, taskIDs []string
	var output string
	var prices workflow.Prices
	cmd := &cobra.Command{
		Use: "workflows", Short: "Compare Baseline, Standard, and Full using installed Pi-Coder",
		Long:         "Prepare isolated installation profiles and verify native resources. By default no model requests run. Use --run with explicit model pricing to execute matched coding tasks.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if cfg.PiBinary == "" {
				cfg.PiBinary, err = exec.LookPath("pi")
				if err != nil {
					return fmt.Errorf("Pi-Coder is required: %w", err)
				}
			}
			if cfg.TzroBinary == "" {
				cfg.TzroBinary, err = os.Executable()
				if err != nil {
					return err
				}
			}
			if cfg.WorkDir == "" {
				dir, err := os.MkdirTemp("", "tzro-workflows-")
				if err != nil {
					return err
				}
				cfg.WorkDir = filepath.Join(dir, "profiles")
			}
			if cfg.Run {
				for _, flag := range []string{"input-price", "output-price", "cache-read-price", "cache-write-price"} {
					if !cmd.Flags().Changed(flag) {
						return fmt.Errorf("--run requires explicit --%s in USD per million tokens", flag)
					}
				}
				cfg.Prices = &prices
				cfg.APIKey = os.Getenv("TZRO_BENCH_API_KEY")
			}
			for _, p := range profiles {
				cfg.Profiles = append(cfg.Profiles, workflow.Profile(p))
			}
			all := workflow.DefaultTasks()
			selected := map[string]bool{}
			for _, id := range taskIDs {
				selected[id] = true
			}
			for _, task := range all {
				if len(taskIDs) == 0 || selected[task.ID] {
					cfg.Tasks = append(cfg.Tasks, task)
					delete(selected, task.ID)
				}
			}
			if len(selected) > 0 {
				return fmt.Errorf("unknown task IDs: %v", selected)
			}
			report, err := workflow.Run(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if output == "" {
				output = filepath.Join(cfg.WorkDir, "report.json")
			}
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, writeErr := file.Write(append(data, '\n'))
			closeErr := file.Close()
			if writeErr != nil {
				return writeErr
			}
			if err := closeErr; err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Profile  Task  Status  Input  Output  Cache-read  Agent-ms")
			failed := !report.Ready
			for _, r := range report.Results {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %d  %d  %d  %d\n", r.Profile, r.Task, r.Status, r.Usage.Input, r.Usage.Output, r.Usage.CacheRead, r.AgentMS)
				if r.Error != "" {
					fmt.Fprintln(cmd.OutOrStdout(), "  "+r.Error)
				}
				if cfg.Run && r.Status != "completed" {
					failed = true
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Report:", output)
			if failed {
				return fmt.Errorf("workflow comparison incomplete; see saved report")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&cfg.Run, "run", false, "Execute model requests after all profiles pass preflight")
	f.StringVar(&cfg.Model, "model", "", "Exact model ID, shared by every profile")
	_ = cmd.MarkFlagRequired("model")
	f.StringVar(&cfg.BaseURL, "base-url", "https://openrouter.ai/api/v1", "OpenAI-compatible provider base URL ending in /v1")
	f.StringVar(&cfg.PiBinary, "pi", "", "Pi-Coder executable (default: PATH lookup)")
	f.StringVar(&cfg.TzroBinary, "tzro-binary", "", "Binary installed by Standard and Full (default: current executable)")
	f.StringVar(&cfg.Installer, "installer", "install.sh", "Installer recipe file")
	f.StringVar(&cfg.WorkDir, "work-dir", "", "New directory outside the source checkout for isolated profiles")
	f.StringVar(&output, "output", "", "New JSON report path (default: work-dir/report.json)")
	f.StringSliceVar(&profiles, "profiles", []string{"baseline", "standard", "full"}, "Installation profiles to compare")
	f.StringSliceVar(&taskIDs, "tasks", nil, "Optional task IDs; default: all four coding fixtures")
	f.DurationVar(&cfg.SetupTimeout, "setup-timeout", 2*time.Minute, "Per-profile installation and readiness limit")
	f.DurationVar(&cfg.Timeout, "timeout", 3*time.Minute, "Per-task native client limit; grading has a separate equal limit")
	f.IntVar(&cfg.MaxTurns, "max-turns", 20, "Stop after this many reported assistant turns")
	f.Float64Var(&cfg.MaxCost, "max-cost", 2, "Suite USD guard, checked after reported usage; in-flight cost may exceed it")
	f.Float64Var(&prices.Input, "input-price", 0, "Uncached input USD per million tokens")
	f.Float64Var(&prices.Output, "output-price", 0, "Output USD per million tokens")
	f.Float64Var(&prices.CacheRead, "cache-read-price", 0, "Cached input USD per million tokens")
	f.Float64Var(&prices.CacheWrite, "cache-write-price", 0, "Cache-write USD per million tokens")
	f.StringVar(&cfg.Full.DecisionBin, "decision-bin", "", "Full: absolute path to jev-score")
	f.StringVar(&cfg.Full.DecisionModel, "decision-model", "", "Full: absolute path to JEV GGUF weights")
	f.StringVar(&cfg.Full.DecisionVersion, "decision-version", "", "Full: scorer/libllama build identifier")
	f.StringVar(&cfg.Full.ExtractorBin, "extractor-bin", "", "Full: absolute worker executable or Python interpreter")
	f.StringArrayVar(&cfg.Full.ExtractorArgs, "extractor-arg", nil, "Full: worker argument; repeat for each argument")
	f.StringVar(&cfg.Full.ExtractorModel, "extractor-model", "", "Full: absolute path to local GLiNER checkpoint directory")
	f.StringVar(&cfg.Full.ExtractorVersion, "extractor-version", "", "Full: worker/dependency version identifier")
	return cmd
}
