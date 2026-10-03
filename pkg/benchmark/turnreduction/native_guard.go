package turnreduction

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func guardCommand(ctx context.Context, cfg EvaluationConfig, cell Observation, action string, extra ...string) error {
	args := []string{cfg.GuardPath, action, "--ledger", cfg.LedgerPath, "--run-id", cfg.RunID, "--cell-id", cell.FixtureID + "-" + string(cell.Condition)}
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, "python3", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("native launch guard %s: %w: %s", action, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func reserveCell(ctx context.Context, cfg EvaluationConfig, cell Observation) error {
	extra := []string{"--model", cfg.Model, "--contract-hash", cfg.contractHash, "--max-launches", strconv.Itoa(cfg.MaxLaunches)}
	if cfg.AmendsContract != "" {
		extra = append(extra, "--amends-contract", cfg.AmendsContract, "--amendment-reason", cfg.AmendmentReason)
	}
	if cfg.Offline {
		extra = append(extra, "--fake")
	} else if cfg.Authorized {
		extra = append(extra, "--authorized")
	}
	return guardCommand(ctx, cfg, cell, "native-reserve", extra...)
}

func completeCell(cfg EvaluationConfig, cell Observation) error {
	receipt := filepath.Join(cell.ArtifactDir, "launch-receipt.json")
	if err := writeJSON(receipt, map[string]any{"exit_code": cell.AgentExitCode, "usage": cell.Events.Usage}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return guardCommand(ctx, cfg, cell, "native-complete", "--receipt", receipt)
}

// DefaultGuardPath locates the repository guard without consulting user settings.
func DefaultGuardPath(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, "scripts", "run_workflow_validation.py")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("native launch guard not found; supply its path")
		}
		dir = parent
	}
}
