package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"tzro/pkg/executor"
	"tzro/pkg/store"
)

func newExecuteCmd() *cobra.Command {
	var maxConcurrency int
	var outputFormat string
	var resultMode string

	cmd := &cobra.Command{
		Use:   "execute [graph.json | -]",
		Short: "Run multiple local steps in one call, with optional System 1 decisions",
		Long: `Run a multi-step workflow locally without a cloud round trip between steps.

The graph is a JSON object containing typed nodes (tool, decision, extract, group)
connected by explicit data dependencies via JSON pointers ($ref).
Tool-only graphs work without local models. Decision and extraction nodes use
configured local workers. Low-confidence decisions can yield to the caller.
Use --result selected to retain intermediate evidence locally and return only
requested results, terminal outputs, and failures.

Examples:
  tzro execute graph.json
  echo '{"version":"3.0","task_id":"t1","nodes":[...]}' | tzro execute -
  cat graph.json | tzro execute -`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if resultMode != "full" && resultMode != "selected" {
				return fmt.Errorf("--result must be full or selected")
			}
			// Read graph from file or stdin
			var graphData []byte
			var err error

			if len(args) == 0 || args[0] == "-" {
				graphData, err = io.ReadAll(cmd.InOrStdin())
			} else {
				graphData, err = os.ReadFile(args[0])
			}
			if err != nil {
				return fmt.Errorf("reading graph: %w", err)
			}

			// Parse the graph
			var g executor.Graph
			if err := json.Unmarshal(graphData, &g); err != nil {
				return fmt.Errorf("parsing graph JSON: %w", err)
			}

			// Set up the engine
			dbPath := getDBPath()
			s, err := store.OpenStore(dbPath)
			if err != nil {
				return fmt.Errorf("opening store: %w", err)
			}
			defer s.Close()

			workspaceRoot, _ := os.Getwd()
			engine, closeWorkers, err := configuredEngine(workspaceRoot, s, maxConcurrency)
			if err != nil {
				return err
			}
			defer closeWorkers()

			// Execute the graph
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			result, err := engine.Execute(ctx, &g)
			if err != nil {
				return fmt.Errorf("execution failed: %w", err)
			}
			recordActivity("graph", result.Status, 0)

			// Output the result
			enc := json.NewEncoder(cmd.OutOrStdout())
			if outputFormat == "pretty" {
				enc.SetIndent("", "  ")
			}
			if resultMode == "selected" {
				return enc.Encode(selectedGraphResult(&g, result, s, workspaceRoot))
			}

			// If status is yielded, also emit a yield envelope
			if result.Status == "yielded" {
				// Find the trigger node
				triggerNode := ""
				for id, out := range result.Outputs {
					if out.Status == "yielded" {
						triggerNode = id
						break
					}
				}

				envelope := executor.NewYieldEnvelope(
					result.TaskID,
					executor.YieldReasonCriteriaUnmet,
					triggerNode,
					"Execution yielded to host harness",
					result.Outputs,
				)
				return enc.Encode(envelope)
			}

			return enc.Encode(result)
		},
	}

	cmd.Flags().IntVar(&maxConcurrency, "concurrency", 4, "Maximum concurrent node executions")
	cmd.Flags().StringVar(&outputFormat, "format", "compact", "Output format: compact or pretty")
	cmd.Flags().StringVar(&resultMode, "result", "full", "Result content: full or selected (recoverable intermediate evidence)")

	return cmd
}
