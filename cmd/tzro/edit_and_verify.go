package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"tzro/pkg/store"
	"tzro/pkg/verification"
)

// newEditAndVerifyCmd creates the 'tzro edit-and-verify' CLI command.
func newEditAndVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit-and-verify",
		Short: "Apply edits and run verification checks",
		Long:  "Applies a batch of literal text edits and executes the repository verification preset.",
		RunE:  runEditAndVerify,
	}
	cmd.Flags().String("request", "-", "Path to request JSON file, or - for stdin")
	return cmd
}

func runEditAndVerify(cmd *cobra.Command, args []string) error {
	requestPath, _ := cmd.Flags().GetString("request")

	// Read request from stdin or file.
	var data []byte
	var err error
	if requestPath == "-" {
		data, err = os.ReadFile("/dev/stdin")
	} else {
		data, err = os.ReadFile(requestPath)
	}
	if err != nil {
		return fmt.Errorf("cannot read request: %w", err)
	}

	var req verification.Request
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("invalid request JSON: %w", err)
	}

	// Determine workspace.
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine workspace: %w", err)
	}

	// Create service dependencies.
	fa, err := verification.NewWorkspaceFileAccess(workspace)
	if err != nil {
		return fmt.Errorf("cannot create file access: %w", err)
	}
	runner := verification.NewExactCommandRunner(workspace)

	// Open store for evidence retention.
	dbPath := filepath.Join(workspace, ".tzro", "store.db")
	var evidenceStore verification.EvidenceStore
	if s, err := store.OpenStore(dbPath); err == nil {
		evidenceStore = verification.NewProductionEvidenceStore(s, workspace)
	}

	svc := verification.NewService(workspace, verification.Dependencies{
		FileAccess:    fa,
		CommandRunner: runner,
		EvidenceStore: evidenceStore,
	})

	summary := svc.ApplyAndVerify(cmd.Context(), req, verification.Options{})

	// Output as JSON.
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary.ToMap())
}
