package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"tzro/pkg/executor"
	"tzro/pkg/store"
)

func newExecuteCmd() *cobra.Command {
	var maxConcurrency int
	var outputFormat string

	cmd := &cobra.Command{
		Use:   "execute [graph.json | -]",
		Short: "Execute a System 1 Graph Call from a JSON file or stdin",
		Long: `Execute a declarative System 1 Graph Call DAG.

The graph is a JSON object containing typed nodes (tool, decision, extract, group)
connected by explicit data dependencies via JSON pointers ($ref).

Examples:
  tzro execute graph.json
  echo '{"version":"3.0","task_id":"t1","nodes":[...]}' | tzro execute -
  cat graph.json | tzro execute -`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read graph from file or stdin
			var graphData []byte
			var err error

			if len(args) == 0 || args[0] == "-" {
				graphData, err = io.ReadAll(os.Stdin)
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
			toolDisp := executor.NewBuiltinDispatcher(workspaceRoot, s)

			engine := executor.NewEngine(
				executor.WithMaxConcurrency(maxConcurrency),
				executor.WithToolDispatcher(toolDisp),
			)

			// Execute the graph
			result, err := engine.Execute(context.Background(), &g)
			if err != nil {
				return fmt.Errorf("execution failed: %w", err)
			}

			// Output the result
			enc := json.NewEncoder(os.Stdout)
			if outputFormat == "pretty" {
				enc.SetIndent("", "  ")
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

	return cmd
}
