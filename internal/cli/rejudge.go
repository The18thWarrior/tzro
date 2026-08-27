package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"tzro/internal/comparison"

	"github.com/spf13/cobra"
)

var (
	rejudgeInputFile         string
	rejudgeOutputDir         string
	rejudgeJudgeModel        string
	rejudgeAll               bool
	rejudgeDeterministicOnly bool
	rejudgeDetWeight         float64
)

var rejudgeCmd = &cobra.Command{
	Use:   "rejudge",
	Short: "Re-evaluate benchmark results with deterministic scoring and optional LLM judge",
	Long: `Load an existing comparison results JSON file and re-score outputs.
Evaluates deterministic checks (tool call analysis, files opened/used, code AST validation,
symbol grounding, ground truth matching, research citations) and optionally re-runs the LLM judge.
Supports --deterministic-only for fast offline scoring without API token costs.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		if rejudgeInputFile == "" {
			fmt.Fprintf(os.Stderr, "Error: --input is required\n")
			os.Exit(1)
		}
		if rejudgeOutputDir == "" {
			fmt.Fprintf(os.Stderr, "Error: --output is required\n")
			os.Exit(1)
		}

		var out io.Writer = os.Stdout
		if globalFlags.JSONOut {
			out = os.Stderr
		}

		// Load existing results
		data, err := os.ReadFile(rejudgeInputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			os.Exit(1)
		}

		var results []comparison.ComparisonResult
		if err := json.Unmarshal(data, &results); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing input JSON: %v\n", err)
			os.Exit(1)
		}

		// Count entries needing rejudge
		needsRejudge := 0
		for _, r := range results {
			if rejudgeAll || rejudgeDeterministicOnly {
				if r.OutputText != "" || r.Error != "" {
					needsRejudge++
				}
			} else if (r.JudgeError != "" || r.QualityScore <= 0 || r.LLMScore <= 0) && r.OutputText != "" && r.Error == "" {
				needsRejudge++
			}
		}

		fmt.Fprintf(out, "=== REJUDGE ===\n")
		fmt.Fprintf(out, "Input:       %s\n", rejudgeInputFile)
		fmt.Fprintf(out, "Output:      %s\n", rejudgeOutputDir)
		fmt.Fprintf(out, "Total:       %d results\n", len(results))
		fmt.Fprintf(out, "Targeting:   %d entries\n", needsRejudge)
		fmt.Fprintf(out, "Det Weight:  %.2f\n", rejudgeDetWeight)
		if rejudgeDeterministicOnly {
			fmt.Fprintf(out, "Mode:        Deterministic-Only (offline, 0 LLM calls)\n")
		} else if rejudgeAll {
			fmt.Fprintf(out, "Mode:        Full Rejudge (all entries)\n")
			if rejudgeJudgeModel != "" {
				fmt.Fprintf(out, "Judge:       %s (via OpenRouter)\n", rejudgeJudgeModel)
			} else {
				fmt.Fprintf(out, "Judge:       Default (Gemini)\n")
			}
		} else {
			fmt.Fprintf(out, "Mode:        Failed Entries Only\n")
			if rejudgeJudgeModel != "" {
				fmt.Fprintf(out, "Judge:       %s (via OpenRouter)\n", rejudgeJudgeModel)
			} else {
				fmt.Fprintf(out, "Judge:       Default (Gemini)\n")
			}
		}
		fmt.Fprintf(out, "Time:        %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(out, "===============\n\n")

		if needsRejudge == 0 && !rejudgeDeterministicOnly {
			fmt.Fprintf(out, "No entries need re-judging. All results have valid scores.\n")
			return
		}

		opts := comparison.RejudgeOptions{
			JudgeOptions: comparison.JudgeOptions{
				Model: rejudgeJudgeModel,
			},
			All:               rejudgeAll,
			DeterministicOnly: rejudgeDeterministicOnly,
			DetWeight:         rejudgeDetWeight,
		}

		updated, rejudged, err := comparison.RejudgeResultsWithOptions(ctx, results, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Rejudge failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(out, "Re-judged %d/%d entries successfully\n", rejudged, len(results))

		// Calculate average deterministic and overall scores
		var totalDet, totalOverall float64
		var scoredCount int
		for _, r := range updated {
			if r.QualityScore > 0 {
				totalOverall += r.QualityScore
				totalDet += r.DeterministicScore
				scoredCount++
			}
		}

		if scoredCount > 0 {
			fmt.Fprintf(out, "Average Deterministic Score: %.2f / 5.0\n", totalDet/float64(scoredCount))
			fmt.Fprintf(out, "Average Composite Quality:   %.2f / 5.0\n", totalOverall/float64(scoredCount))
		}

		// Generate updated report
		if err := comparison.GenerateReport(updated, rejudgeOutputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Report generation failed: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, updated)
			return
		}

		fmt.Fprintf(out, "\nReport: %s/\n", rejudgeOutputDir)
		fmt.Fprintf(out, "===============\n")
	},
}

func init() {
	rejudgeCmd.Flags().StringVarP(&rejudgeInputFile, "input", "i", "", "Input comparison results JSON file (required)")
	rejudgeCmd.Flags().StringVarP(&rejudgeOutputDir, "output", "o", "", "Output directory for updated results (required)")
	rejudgeCmd.Flags().StringVar(&rejudgeJudgeModel, "judge-model", "", "OpenRouter model ID for judging (e.g. anthropic/claude-sonnet-4)")
	rejudgeCmd.Flags().BoolVarP(&rejudgeAll, "all", "a", false, "Re-judge all entries in the input file, not just failed ones")
	rejudgeCmd.Flags().BoolVarP(&rejudgeDeterministicOnly, "deterministic-only", "d", false, "Run only deterministic checks without making LLM calls")
	rejudgeCmd.Flags().Float64VarP(&rejudgeDetWeight, "det-weight", "w", comparison.DefaultDeterministicWeight, "Deterministic score weight (0.0 to 1.0, default 0.5)")

	compareCmd.AddCommand(rejudgeCmd)
}
