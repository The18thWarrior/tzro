package turnreduction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tzro/pkg/hooks"
	"tzro/pkg/verification"
)

// RunAntigravity runs fresh native client processes and retains every outcome.
func RunAntigravity(ctx context.Context, cfg EvaluationConfig) (*EvaluationReport, error) {
	if !cfg.Offline && (!cfg.Authorized || !cfg.AcceptUnknownCost || cfg.APIKey == "" || cfg.RunID == "" || cfg.LedgerPath == "" || cfg.MaxLaunches < 1) {
		return nil, fmt.Errorf("live execution requires authorization, an explicit run ID, ledger and launch allowance, GEMINI_API_KEY, and acceptance of unknown charges")
	}
	if cfg.Offline {
		cfg.APIKey = "offline-invalid-key"
	}
	if cfg.ClientPath == "" || cfg.OutputDir == "" || len(cfg.Fixtures) == 0 {
		return nil, fmt.Errorf("client, output directory and fixtures are required")
	}
	if cfg.InvocationRecorderPath != "" && !filepath.IsAbs(cfg.InvocationRecorderPath) {
		return nil, fmt.Errorf("absolute invocation recorder path required")
	}
	if cfg.Model == "" {
		cfg.Model = NativeModel
	}
	if cfg.Model != NativeModel {
		return nil, fmt.Errorf("model substitution is not allowed: expected %s", NativeModel)
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 10 * time.Minute
	}
	if cfg.RunID == "" {
		cfg.RunID = "offline"
	}
	if cfg.MaxLaunches == 0 {
		cfg.MaxLaunches = 27
	}
	if cfg.GuardPath == "" {
		var err error
		cfg.GuardPath, err = DefaultGuardPath(cfg.Fixtures[0].SubjectDir)
		if err != nil {
			return nil, err
		}
	}
	if len(cfg.Conditions) == 0 {
		cfg.Conditions = []Condition{ConditionNative, ConditionSimple, ConditionTzro}
	}
	seenConditions := map[Condition]bool{}
	for _, condition := range cfg.Conditions {
		if seenConditions[condition] {
			return nil, fmt.Errorf("duplicate condition: %s", condition)
		}
		seenConditions[condition] = true
		if condition != ConditionNative && condition != ConditionTzro && condition != ConditionSimple {
			return nil, fmt.Errorf("native preparation for condition %s is not configured", condition)
		}
		if condition == ConditionTzro && !filepath.IsAbs(cfg.TzroPath) {
			return nil, fmt.Errorf("absolute production Tzro binary path required")
		}
		if condition == ConditionSimple && !filepath.IsAbs(cfg.HelperPath) {
			return nil, fmt.Errorf("absolute bulk helper path required")
		}
	}
	if cfg.GoCache == "" {
		cache, err := exec.CommandContext(ctx, "go", "env", "GOCACHE").Output()
		if err != nil {
			return nil, fmt.Errorf("resolve shared Go cache: %w", err)
		}
		cfg.GoCache = strings.TrimSpace(string(cache))
	}
	seenFixtures := map[string]bool{}
	for _, f := range cfg.Fixtures {
		if f.ID == "" || filepath.Base(f.ID) != f.ID || f.ID == "." || f.ID == ".." || seenFixtures[f.ID] {
			return nil, fmt.Errorf("invalid or duplicate fixture identity: %s", f.ID)
		}
		seenFixtures[f.ID] = true
	}
	var readiness *ReadinessReport
	if !cfg.Offline {
		checked := PrepareEvaluation(ctx, cfg)
		if !checked.OfflineReady {
			return nil, fmt.Errorf("native readiness failed: %+v", checked.Problems)
		}
		readiness = &checked
		cfg.ClientVersion = checked.ClientVersion
		cfg.Toolchain = checked.Toolchain
	}
	var err error
	cfg.OutputDir, err = filepath.Abs(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(cfg.OutputDir), 0755); err != nil {
		return nil, err
	}
	if err = os.Mkdir(cfg.OutputDir, 0700); err != nil {
		return nil, fmt.Errorf("new output directory required: %w", err)
	}
	if readiness != nil {
		if err = writeJSON(filepath.Join(cfg.OutputDir, "readiness.json"), readiness); err != nil {
			return nil, err
		}
	}
	if cfg.LedgerPath == "" {
		cfg.LedgerPath = filepath.Join(cfg.OutputDir, "launches.json")
	}
	contract, hash, err := freezeContract(cfg)
	if err != nil {
		return nil, err
	}
	cfg.contractHash = hash
	if err = writeJSON(filepath.Join(cfg.OutputDir, "contract.json"), contract); err != nil {
		return nil, err
	}
	report := &EvaluationReport{Version: "tzro.turn-reduction.v2", Started: time.Now().UTC(), Offline: cfg.Offline, Model: cfg.Model}
	report.ContractHash = hash
	report.TzroGuidanceSHA256 = contract.FileHashes["tzro_guidance"]
	report.InvocationRecorderSHA256 = contract.FileHashes["invocation_recorder"]
	report.Schedule = BalancedSchedule(cfg.Fixtures, cfg.Conditions)
	if err = writeJSON(filepath.Join(cfg.OutputDir, "report.json"), report); err != nil {
		return nil, err
	}
	fixtures := map[string]Fixture{}
	for _, fixture := range cfg.Fixtures {
		fixtures[fixture.ID] = fixture
	}
	for _, scheduled := range report.Schedule {
		_, currentHash, hashErr := freezeContract(cfg)
		if hashErr != nil {
			return report, hashErr
		}
		if currentHash != cfg.contractHash {
			return report, fmt.Errorf("frozen inputs changed; matrix stopped before another launch")
		}
		cell := runNativeCell(ctx, cfg, fixtures[scheduled.FixtureID], scheduled.Condition)
		report.Cells = append(report.Cells, cell)
		report.Summary = EvaluateMatrix(cfg.Fixtures, report.Cells, cfg.Offline)
		if err = writeJSON(filepath.Join(cfg.OutputDir, "report.json"), report); err != nil {
			return report, err
		}
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if cfg.InvocationRecorderPath != "" && (cell.Events.Invocations == nil || !cell.Events.Invocations.Complete || cell.Events.CloudDecisionRounds == nil) {
			return report, fmt.Errorf("native invocation evidence incomplete; matrix stopped with attempt retained: %s", cell.ArtifactDir)
		}
		if !cfg.Offline && (cell.State != "completed" || cell.Events.Model != cfg.Model || (cell.AgentExitCode != nil && *cell.AgentExitCode != 0 && len(cell.Events.ToolCalls) == 0)) {
			return report, fmt.Errorf("native client infrastructure failed; matrix stopped with attempt retained: %s", cell.ArtifactDir)
		}
	}
	return report, nil
}

func runNativeCell(ctx context.Context, cfg EvaluationConfig, f Fixture, condition Condition) (cell Observation) {
	cell = Observation{FixtureID: f.ID, Condition: condition, State: "not_started", ArtifactDir: filepath.Join(cfg.OutputDir, f.ID+"-"+string(condition))}
	preparation := time.Now()
	if err := os.Mkdir(cell.ArtifactDir, 0700); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	defer func() {
		if err := writeJSON(filepath.Join(cell.ArtifactDir, "result.json"), cell); err != nil {
			cell.Errors = append(cell.Errors, err.Error())
			cell.Passed = false
			cell.VerifiedCompletionSeconds = nil
		}
	}()
	workspace, err := os.MkdirTemp("", "tzro-native-workspace-*")
	if err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	cell.ExecutionWorkspace = workspace
	defer os.RemoveAll(workspace)
	defer func() {
		if err := copyDirRecursive(workspace, filepath.Join(cell.ArtifactDir, "workspace")); err != nil {
			cell.Errors = append(cell.Errors, "retain final workspace: "+err.Error())
			cell.Passed = false
			cell.VerifiedCompletionSeconds = nil
		}
	}()
	if err := copyDirRecursive(f.SubjectDir, workspace); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	home, err := os.MkdirTemp("", "tzro-native-home-*")
	if err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	defer os.RemoveAll(home)
	settings := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	if err := writeJSON(settings, map[string]string{"modelProvider": "gemini"}); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	if condition == ConditionTzro {
		_, err := hooks.InstallAgents(hooks.InstallOptions{Home: home, Workspace: workspace, Binary: cfg.TzroPath, Targets: []string{"antigravity"}})
		if err != nil {
			cell.Errors = append(cell.Errors, "install production Tzro: "+err.Error())
			return
		}
		if cfg.TzroGuidancePath != "" {
			guidance, err := os.ReadFile(cfg.TzroGuidancePath)
			if err == nil {
				err = appendAgentGuidance(workspace, append([]byte("\n"), guidance...))
			}
			if err != nil {
				cell.Errors = append(cell.Errors, "install declared Tzro guidance: "+err.Error())
				return
			}
		}
	}
	if condition == ConditionSimple {
		if err := installSimpleHelper(cfg.HelperPath, workspace); err != nil {
			cell.Errors = append(cell.Errors, err.Error())
			return
		}
	}
	if cfg.InvocationRecorderPath != "" {
		if err := installInvocationObserver(cfg.InvocationRecorderPath, home, cell.ArtifactDir); err != nil {
			cell.Errors = append(cell.Errors, "install invocation observer: "+err.Error())
			return
		}
	}
	before, err := treeHashes(workspace)
	if err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	if err = writeJSON(filepath.Join(cell.ArtifactDir, "inputs.json"), before); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	if err = reserveCell(ctx, cfg, cell); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	defer func() {
		if err := completeCell(cfg, cell); err != nil {
			cell.Errors = append(cell.Errors, err.Error())
			cell.Passed = false
			cell.VerifiedCompletionSeconds = nil
		}
	}()
	cell.State = "running"
	if err = writeJSON(filepath.Join(cell.ArtifactDir, "result.json"), cell); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
		return
	}
	cell.PreparationSeconds = time.Since(preparation).Seconds()
	start := time.Now()
	if condition == ConditionTzro {
		if err = bindMCPDeadline(home, workspace, start.Add(cfg.TaskTimeout)); err != nil {
			cell.Errors = append(cell.Errors, err.Error())
			return
		}
	}
	if condition == ConditionTzro || cfg.InvocationRecorderPath != "" {
		if err = copyDirRecursive(filepath.Join(home, ".gemini", "config"), filepath.Join(cell.ArtifactDir, "client-config")); err != nil {
			cell.Errors = append(cell.Errors, err.Error())
			return
		}

	}
	taskCtx, cancel := context.WithDeadline(ctx, start.Add(cfg.TaskTimeout))
	defer cancel()
	args := []string{"-p", f.Prompt, "--model", cfg.Model, "--output-format", "stream-json", "--print-timeout", cfg.TaskTimeout.String(), "--dangerously-skip-permissions"}
	cell.Events, cell.AgentExitCode, cell.AgentSeconds, err = captureClient(taskCtx, cfg, workspace, home, cell.ArtifactDir, args)
	cell.State = "completed"
	if err != nil {
		cell.Errors = append(cell.Errors, "native client: "+err.Error())
	}
	if cfg.InvocationRecorderPath != "" {
		if err := observeInvocations(cell.ArtifactDir, &cell.Events, cell.AgentSeconds); err != nil {
			cell.Errors = append(cell.Errors, "native invocation evidence: "+err.Error())
		}
	}
	if cell.Events.Status != "SUCCESS" {
		cell.Errors = append(cell.Errors, "native terminal status: "+cell.Events.Status)
	}
	if cell.Events.Model != cfg.Model {
		cell.Errors = append(cell.Errors, "native model does not match the contract")
	}
	gradeStart := time.Now()
	after, hashErr := treeHashes(workspace)
	if hashErr != nil {
		cell.Errors = append(cell.Errors, hashErr.Error())
	} else {
		cell.Errors = append(cell.Errors, protectedChanges(before, after, f.EditableFiles)...)
		if err = writeJSON(filepath.Join(cell.ArtifactDir, "final-files.json"), after); err != nil {
			cell.Errors = append(cell.Errors, err.Error())
		}
	}
	cell.Checks, err = finalChecks(taskCtx, f, workspace)
	if err != nil {
		cell.Errors = append(cell.Errors, err.Error())
	}
	passed, log, gradeErr := GradeFixture(taskCtx, f, workspace)
	if gradeErr != nil {
		cell.Errors = append(cell.Errors, gradeErr.Error())
	}
	if err := os.WriteFile(filepath.Join(cell.ArtifactDir, "grade.log"), []byte(log), 0600); err != nil {
		cell.Errors = append(cell.Errors, err.Error())
	}
	cell.GradeSeconds = time.Since(gradeStart).Seconds()
	cell.ElapsedSeconds = time.Since(start).Seconds()
	if taskCtx.Err() != nil {
		cell.Errors = append(cell.Errors, taskCtx.Err().Error())
	}
	cell.Passed = passed && len(cell.Errors) == 0 && len(cell.Checks) > 0
	for _, check := range cell.Checks {
		cell.Passed = cell.Passed && check.Passed
	}
	if cell.Passed {
		seconds := cell.ElapsedSeconds
		cell.VerifiedCompletionSeconds = &seconds
	}
	return
}

func finalChecks(ctx context.Context, f Fixture, workspace string) ([]CheckReceipt, error) {
	preset, err := verification.LoadPreset(filepath.Join(f.SubjectDir, ".tzro", "verification.yaml"))
	if err != nil {
		return nil, err
	}
	gradingCopy, err := os.MkdirTemp("", "tzro-final-checks-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(gradingCopy)
	if err = copyDirContext(ctx, workspace, gradingCopy); err != nil {
		return nil, err
	}
	runner := verification.NewExactCommandRunner(gradingCopy)
	var receipts []CheckReceipt
	for _, check := range preset.Checks {
		result := runner.Run(ctx, verification.Command{ID: check.ID, Argv: check.Argv, Cwd: check.Cwd})
		receipts = append(receipts, CheckReceipt{ID: check.ID, Argv: check.Argv, Passed: result.ExitCode != nil && *result.ExitCode == 0 && result.StartupErr == nil && !result.Cancelled && !result.TimedOut, ExitCode: result.ExitCode, DurationSeconds: result.Duration.Seconds(), Log: string(result.Output)})
	}
	return receipts, nil
}

func nativeEnvironment(home, apiKey string) []string {
	var result []string
	for _, key := range []string{"PATH", "TMPDIR", "GOCACHE", "GOMODCACHE", "GOROOT", "GOPATH", "PYENV_ROOT", "LANG", "LC_ALL"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	if apiKey != "" {
		result = append(result, "GEMINI_API_KEY="+apiKey)
	}
	return append(result, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "TZ=UTC", "AGY_CLI_DISABLE_AUTO_UPDATE=true")
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty evidence path")
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
