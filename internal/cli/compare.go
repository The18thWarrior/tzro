package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"tzro/internal/comparison"

	"github.com/spf13/cobra"
)

var (
	compareOutputDir   string
	compareTier        int
	compareCondition   string
	compareCategory    string
	compareTask        string
	comparePromptPrice float64
	compareComplPrice  float64
)

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Run comparison benchmark: Cloud ReAct vs tzro hybrid execution",
	Long:  `Measure cloud token consumption, dollar cost, wall-clock time, and output quality across execution conditions using documentation generation (docgen), code generation (codegen), and data analysis (datanal) tasks. By default runs all three task suites.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// Validate inputs
		if compareOutputDir == "" {
			fmt.Fprintf(os.Stderr, "Error: --output is required\n")
			os.Exit(1)
		}

		if compareTier < 0 || compareTier > 5 {
			fmt.Fprintf(os.Stderr, "Error: --tier must be 0-5 (0 = all)\n")
			os.Exit(1)
		}

		if compareCondition != "" {
			valid := false
			// Check against all category condition sets
			seen := make(map[string]bool)
			for _, c := range comparison.AllConditions() {
				seen[c] = true
			}
			for _, c := range comparison.CodegenConditions() {
				seen[c] = true
			}
			for _, c := range comparison.DatanalConditions() {
				seen[c] = true
			}
			if seen[compareCondition] {
				valid = true
			}
			if !valid {
				var all []string
				for c := range seen {
					all = append(all, c)
				}
				fmt.Fprintf(os.Stderr, "Error: --condition must be one of: %s\n",
					strings.Join(all, ", "))
				os.Exit(1)
			}
		}

		pricing := comparison.PricingTable{
			PromptPer1KTokens:     comparePromptPrice,
			CompletionPer1KTokens: compareComplPrice,
		}

		var out io.Writer = os.Stdout
		if globalFlags.JSONOut {
			out = os.Stderr
		}

		fmt.Fprintf(out, "=== COMPARISON BENCHMARK ===\n")
		fmt.Fprintf(out, "Output:     %s\n", compareOutputDir)
		if compareCategory != "" {
			fmt.Fprintf(out, "Category:   %s only\n", compareCategory)
		} else {
			fmt.Fprintf(out, "Category:   All (docgen + codegen + datanal)\n")
		}
		if compareTier > 0 {
			fmt.Fprintf(out, "Tier:       T%d only\n", compareTier)
		} else {
			fmt.Fprintf(out, "Tier:       All (T1-T5)\n")
		}
		if compareCondition != "" {
			fmt.Fprintf(out, "Condition:  %s only\n", compareCondition)
		} else {
			fmt.Fprintf(out, "Condition:  All conditions\n")
		}
		if compareTask != "" {
			fmt.Fprintf(out, "Task:       %s only\n", compareTask)
		}
		fmt.Fprintf(out, "Pricing:    $%.4f/1K prompt, $%.4f/1K completion\n", pricing.PromptPer1KTokens, pricing.CompletionPer1KTokens)
		fmt.Fprintf(out, "Time:       %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(out, "============================\n\n")

		callbacks := &comparison.SuiteCallbacks{
			OnTaskStart: func(taskID, conditionID string) {
				fmt.Fprintf(out, "[%s] Running %s / %s...\n", time.Now().Format("15:04:05"), taskID, conditionID)
			},
			OnTaskComplete: func(result comparison.ComparisonResult) {
				if result.Error != "" {
					fmt.Fprintf(out, "  ✗ Error: %s\n", result.Error)
				} else {
					fmt.Fprintf(out, "  ✓ %d cloud tokens, %d local tokens, $%.4f, %dms, %d tool calls\n",
						result.CloudTokens.TotalTokens, result.LocalTokens.TotalTokens,
						result.EstCostUSD, result.WallClockMs, result.ToolCallCount)
				}
			},
			OnJudgeStart: func(taskID, conditionID string) {
				fmt.Fprintf(out, "  Judging %s / %s...\n", taskID, conditionID)
			},
			OnJudgeComplete: func(taskID, conditionID string, score float64) {
				fmt.Fprintf(out, "  Quality: %.2f/5.0\n", score)
			},
		}

		opts := comparison.SuiteOptions{
			Category:  compareCategory,
			Tier:      compareTier,
			Condition: compareCondition,
			TaskID:    compareTask,
			OutputDir: compareOutputDir,
			Pricing:   pricing,
		}

		results, err := comparison.RunComparisonSuite(ctx, opts, callbacks)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Comparison benchmark failed: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, results)
			return
		}

		// Print summary
		fmt.Fprintf(out, "\n=== COMPARISON SUMMARY ===\n")
		fmt.Fprintf(out, "Total results: %d\n", len(results))

		// Calculate headline savings
		var reactTokens, coopTokens int
		var reactCost, coopCost float64
		for _, r := range results {
			switch r.Condition {
			case comparison.ConditionCloudReAct:
				reactTokens += r.CloudTokens.TotalTokens
				reactCost += r.EstCostUSD
			case comparison.ConditionCooperative:
				coopTokens += r.CloudTokens.TotalTokens
				coopCost += r.EstCostUSD
			}
		}

		if reactTokens > 0 && coopTokens > 0 {
			savings := (1.0 - float64(coopTokens)/float64(reactTokens)) * 100
			fmt.Fprintf(out, "Cloud token savings (ReAct → Cooperative): %.0f%%\n", savings)
			fmt.Fprintf(out, "Cost savings: $%.4f → $%.4f (saved $%.4f)\n", reactCost, coopCost, reactCost-coopCost)
		}

		fmt.Fprintf(out, "Report: %s/\n", compareOutputDir)
		fmt.Fprintf(out, "==========================\n")
	},
}

func init() {
	defaultPricing := comparison.DefaultPricing()

	compareCmd.Flags().StringVarP(&compareOutputDir, "output", "o", "", "Output directory for results (required)")
	compareCmd.Flags().StringVar(&compareCategory, "category", "", "Task category: docgen, codegen, datanal, or empty for all (default: all)")
	compareCmd.Flags().IntVarP(&compareTier, "tier", "t", 0, "Run a specific tier (1-5), or 0 for all")
	compareCmd.Flags().StringVarP(&compareCondition, "condition", "c", "", "Run a specific condition (cloud_react, local_react, cloud_dag, local_only, cooperative, tzro_code, cloud_code)")
	compareCmd.Flags().StringVar(&compareTask, "task", "", "Run a specific task ID")
	compareCmd.Flags().Float64Var(&comparePromptPrice, "prompt-price", defaultPricing.PromptPer1KTokens, "Price per 1K prompt tokens (USD)")
	compareCmd.Flags().Float64Var(&compareComplPrice, "completion-price", defaultPricing.CompletionPer1KTokens, "Price per 1K completion tokens (USD)")

	RootCmd.AddCommand(compareCmd)
}
