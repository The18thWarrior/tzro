package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tzro/pkg/benchmark/turnreduction"
)

func newTurnReductionBenchCmd() *cobra.Command {
	var cfg turnreduction.EvaluationConfig
	var fixturesDir, client, offlineClient, envFile, conditions string
	var run bool
	cmd := &cobra.Command{
		Use:   "turn-reduction",
		Short: "Prepare or run the native Antigravity verification benchmark",
		Long: `By default, validate the nine fixture contracts and report local readiness without model requests.
With --run, launch the installed Antigravity client with its ordinary tools.
Native uses no Tzro integration; Simple uses bulk_update.py; Tzro uses the production MCP service.
Results retain every attempt, private grading, prescribed checks, native events, and a complete-suite timing gate.
One matrix is an observed screen, not a confirmed causal improvement.

Live execution requires --authorize-live, --accept-unknown-cost, --run-id, --ledger, and a launch allowance.
The allowance limits task launches; it cannot cap internal provider retries or dollar charges.
Use --offline-client only with a scripted test executable. Its results are labeled offline evidence.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fixturesDir == "" {
				guard, err := turnreduction.DefaultGuardPath(".")
				if err != nil {
					return err
				}
				fixturesDir = filepath.Join(filepath.Dir(filepath.Dir(guard)), "pkg", "benchmark", "turnreduction", "fixtures")
			}
			var err error
			fixturesDir, err = filepath.Abs(fixturesDir)
			if err != nil {
				return err
			}
			cfg.Fixtures, err = turnreduction.LoadFixtures(fixturesDir)
			if err != nil {
				return err
			}
			if cfg.GuardPath == "" {
				cfg.GuardPath, err = turnreduction.DefaultGuardPath(fixturesDir)
				if err != nil {
					return err
				}
			}
			if cfg.HelperPath == "" {
				cfg.HelperPath = filepath.Join(filepath.Dir(cfg.GuardPath), "bulk_update.py")
			}
			if cfg.TzroPath == "" {
				cfg.TzroPath, err = os.Executable()
				if err != nil {
					return err
				}
			}
			cfg.TzroPath, err = filepath.Abs(cfg.TzroPath)
			if err != nil {
				return err
			}
			if cfg.InvocationRecorderPath != "" {
				cfg.InvocationRecorderPath, err = filepath.Abs(cfg.InvocationRecorderPath)
				if err != nil {
					return err
				}
			}
			cfg.HelperPath, err = filepath.Abs(cfg.HelperPath)
			if err != nil {
				return err
			}
			cfg.Offline = offlineClient != ""
			if cfg.Offline {
				client = offlineClient
			} else {
				cfg.APIKey, err = turnreduction.GeminiKey(envFile)
				if err != nil {
					return err
				}
			}
			cfg.ClientPath, err = exec.LookPath(client)
			if err != nil {
				return fmt.Errorf("Antigravity executable unavailable: %w", err)
			}
			cfg.ClientPath, err = filepath.Abs(cfg.ClientPath)
			if err != nil {
				return err
			}
			cfg.Conditions = nil
			for _, condition := range strings.Split(conditions, ",") {
				cfg.Conditions = append(cfg.Conditions, turnreduction.Condition(strings.TrimSpace(condition)))
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if !run {
				return encoder.Encode(turnreduction.PrepareEvaluation(cmd.Context(), cfg))
			}
			if cfg.OutputDir == "" {
				return fmt.Errorf("--output must name a new evidence directory")
			}
			report, runErr := turnreduction.RunAntigravity(cmd.Context(), cfg)
			if report != nil {
				if err := encoder.Encode(report); err != nil {
					return err
				}
			}
			return runErr
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&run, "run", false, "Run the declared native matrix (default: local readiness only)")
	flags.StringVar(&fixturesDir, "fixtures", "", "Directory containing versioned fixture contracts")
	flags.StringVar(&client, "client", "agy", "Installed Antigravity executable")
	flags.StringVar(&offlineClient, "offline-client", "", "Scripted client executable; results cannot support a performance claim")
	flags.StringVar(&cfg.TzroPath, "tzro-binary", "", "Production Tzro binary (default: this executable)")
	flags.StringVar(&cfg.HelperPath, "helper", "", "Actual bulk_update.py path")
	flags.StringVar(&cfg.TzroGuidancePath, "tzro-guidance", "", "Optional instructions appended to AGENTS.md only for Tzro; hash-labeled as a guided experiment")
	flags.StringVar(&cfg.InvocationRecorderPath, "invocation-recorder", "", "Passive native model hook recorder installed identically in all conditions; incomplete evidence stops the matrix")
	flags.StringVar(&cfg.GuardPath, "guard", "", "Persistent launch guard script")
	flags.StringVar(&cfg.OutputDir, "output", "", "New directory for contract, traces, workspaces, and results")
	flags.StringVar(&cfg.RunID, "run-id", "", "Declared run identity")
	flags.StringVar(&cfg.LedgerPath, "ledger", "", "Persistent native launch ledger; existing entries remain intact")
	flags.IntVar(&cfg.MaxLaunches, "max-launches", 27, "Maximum authorized task launches across this ledger")
	flags.StringVar(&cfg.AmendsContract, "amends-contract", "", "Explicitly amend this prior ledger contract; preserve all prior launches")
	flags.StringVar(&cfg.AmendmentReason, "amendment-reason", "", "Reason for an explicitly authorized ledger contract amendment")
	flags.BoolVar(&cfg.Authorized, "authorize-live", false, "Authorize this declared live native matrix")
	flags.BoolVar(&cfg.AcceptUnknownCost, "accept-unknown-cost", false, "Accept launch limits without a dollar cap or observed billing")
	flags.StringVar(&cfg.Model, "model", turnreduction.NativeModel, "Exact agreed model; substitutions are rejected")
	flags.DurationVar(&cfg.TaskTimeout, "task-timeout", 10*time.Minute, "Whole-task deadline, including final checks and independent grading")
	flags.StringVar(&conditions, "conditions", "native,simple,tzro", "Conditions in the balanced schedule")
	flags.StringVar(&envFile, "env-file", "", "Read only GEMINI_API_KEY from this file when absent from the environment")
	return cmd
}
