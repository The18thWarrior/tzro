package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"tzro/internal/benchmark"

	"github.com/spf13/cobra"
)

var (
	benchmarkDataset   string
	benchmarkMode      string
	benchmarkModelMode string
	benchmarkReal      bool
	benchmarkLimit     int
	benchmarkVerbose   bool
)

var benchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Benchmark tzro execution engine against standard datasets",
	Long:  `Evaluate and analyze tzro's Planning and GBNF Parameter Accuracy against standard datasets (BFCL & ComplexFuncBench).`,
}

var benchmarkRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the full benchmark suite offline",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// Validate inputs
		benchmarkDataset = strings.ToLower(benchmarkDataset)
		if benchmarkDataset != "bfcl" && benchmarkDataset != "complexfuncbench" {
			fmt.Fprintf(os.Stderr, "Error: unsupported dataset %q. Choose 'bfcl' or 'complexfuncbench'.\n", benchmarkDataset)
			os.Exit(1)
		}

		benchmarkMode = strings.ToLower(benchmarkMode)
		if benchmarkMode != "consolidated" && benchmarkMode != "interactive" {
			fmt.Fprintf(os.Stderr, "Error: unsupported simulation mode %q. Choose 'consolidated' or 'interactive'.\n", benchmarkMode)
			os.Exit(1)
		}

		benchmarkModelMode = strings.ToLower(benchmarkModelMode)
		if benchmarkModelMode != "local" && benchmarkModelMode != "cooperative" && benchmarkModelMode != "cloud" {
			fmt.Fprintf(os.Stderr, "Error: unsupported model-mode %q. Choose 'local', 'cooperative', or 'cloud'.\n", benchmarkModelMode)
			os.Exit(1)
		}

		var out io.Writer
		if globalFlags.JSONOut {
			if benchmarkVerbose {
				out = os.Stderr
			}
		} else {
			out = os.Stdout
		}

		if out != nil {
			fmt.Fprintf(out, "=== STARTING TZRO OFFLINE BENCHMARK SUITE ===\n")
			fmt.Fprintf(out, "Dataset:     %s\n", strings.ToUpper(benchmarkDataset))
			fmt.Fprintf(out, "Mode:        %s\n", strings.ToUpper(benchmarkMode))
			fmt.Fprintf(out, "Model Tier:  %s\n", strings.ToUpper(benchmarkModelMode))
			fmt.Fprintf(out, "Time:        %s\n", time.Now().Format("2006-01-02 15:04:05"))
			fmt.Fprintf(out, "---------------------------------------------\n")
			if benchmarkReal {
				fmt.Fprintf(out, "Connecting to active model endpoints & registering tools...\n\n")
			} else {
				fmt.Fprintf(out, "Simulating LLM responses & registering tools...\n\n")
			}
		}

		results, err := benchmark.RunSuite(ctx, benchmarkDataset, benchmarkMode, benchmarkModelMode, benchmarkReal, benchmarkLimit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Benchmark execution failed: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, results)
			if !benchmarkVerbose {
				return
			}
		}

		// Display tabular results with color highlights
		headers := []string{"CASE ID", "PLAN MATCH", "PARAM MATCH", "DURATION", "TOKENS (L/C)", "STATUS"}
		var rows [][]string

		passedCount := 0
		planMatchCount := 0
		paramMatchCount := 0
		var totalDuration int64 = 0

		var totalLocalPrompt, totalLocalCompletion, totalLocalTotal int
		var totalCloudPrompt, totalCloudCompletion, totalCloudTotal int

		for _, r := range results {
			duration := fmt.Sprintf("%dms", r.ExecutionDurationMs)
			totalDuration += r.ExecutionDurationMs

			planStatus := "\u001b[31mFAIL\u001b[0m" // Red
			if r.PlanningMatch {
				planStatus = "\u001b[32mPASS\u001b[0m" // Green
				planMatchCount++
			}

			paramStatus := "\u001b[31mFAIL\u001b[0m"
			if r.ParameterMatch {
				if r.FuzzyMatchUsed {
					paramStatus = "\u001b[33mPASS (FUZZY)\u001b[0m"
				} else {
					paramStatus = "\u001b[32mPASS\u001b[0m"
				}
				paramMatchCount++
			}

			status := "\u001b[31mFAILED\u001b[0m"
			if r.Passed {
				status = "\u001b[32mPASSED\u001b[0m"
				passedCount++
			}

			tokensStr := fmt.Sprintf("%d / %d", r.LocalTokens.TotalTokens, r.CloudTokens.TotalTokens)

			totalLocalPrompt += r.LocalTokens.PromptTokens
			totalLocalCompletion += r.LocalTokens.CompletionTokens
			totalLocalTotal += r.LocalTokens.TotalTokens

			totalCloudPrompt += r.CloudTokens.PromptTokens
			totalCloudCompletion += r.CloudTokens.CompletionTokens
			totalCloudTotal += r.CloudTokens.TotalTokens

			rows = append(rows, []string{r.TestCaseID, planStatus, paramStatus, duration, tokensStr, status})
		}

		if out != nil {
			printBenchmarkTable(out, headers, rows)

			totalCases := len(results)
			passedPercent := 0.0
			planPercent := 0.0
			paramPercent := 0.0
			if totalCases > 0 {
				passedPercent = float64(passedCount) / float64(totalCases) * 100.0
				planPercent = float64(planMatchCount) / float64(totalCases) * 100.0
				paramPercent = float64(paramMatchCount) / float64(totalCases) * 100.0
			}

			fmt.Fprintf(out, "\n=== BENCHMARK ANALYTICS SUMMARY ===\n")
			fmt.Fprintf(out, "Total Evaluated Cases: %d\n", totalCases)
			fmt.Fprintf(out, "Successful Runs:       %d (%s%.1f%%\u001b[0m)\n", passedCount, getColor(passedPercent), passedPercent)
			fmt.Fprintf(out, "DAG Planning Accuracy: %d (%s%.1f%%\u001b[0m)\n", planMatchCount, getColor(planPercent), planPercent)
			fmt.Fprintf(out, "GBNF Parameter Acc:    %d (%s%.1f%%\u001b[0m)\n", paramMatchCount, getColor(paramPercent), paramPercent)
			fmt.Fprintf(out, "Total Token Usage:\n")
			fmt.Fprintf(out, "  Local:               %d tokens (%d prompt, %d completion)\n", totalLocalTotal, totalLocalPrompt, totalLocalCompletion)
			fmt.Fprintf(out, "  Cloud:               %d tokens (%d prompt, %d completion)\n", totalCloudTotal, totalCloudPrompt, totalCloudCompletion)
			fmt.Fprintf(out, "Total Elapsed Time:    %.2fs (Avg: %dms/case)\n", float64(totalDuration)/1000.0, int(totalDuration)/totalCases)
			fmt.Fprintf(out, "===================================\n")
		}
	},
}

func getColor(percent float64) string {
	if percent >= 90.0 {
		return "\u001b[32m" // Green
	}
	if percent >= 75.0 {
		return "\u001b[33m" // Yellow
	}
	return "\u001b[31m" // Red
}

func printBenchmarkTable(out io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, val := range row {
			// Strip ANSI escapes for length calculation
			cleaned := val
			cleaned = strings.ReplaceAll(cleaned, "\u001b[32m", "")
			cleaned = strings.ReplaceAll(cleaned, "\u001b[31m", "")
			cleaned = strings.ReplaceAll(cleaned, "\u001b[33m", "")
			cleaned = strings.ReplaceAll(cleaned, "\u001b[0m", "")
			if len(cleaned) > widths[i] {
				widths[i] = len(cleaned)
			}
		}
	}

	printBenchmarkDivider(out, widths)
	fmt.Fprint(out, "|")
	for i, h := range headers {
		fmt.Fprintf(out, " %-*s |", widths[i], h)
	}
	fmt.Fprintln(out)
	printBenchmarkDivider(out, widths)

	for _, row := range rows {
		fmt.Fprint(out, "|")
		for i, val := range row {
			// Calculate padding manually to account for ANSI escapes
			cleaned := val
			cleaned = strings.ReplaceAll(cleaned, "\u001b[32m", "")
			cleaned = strings.ReplaceAll(cleaned, "\u001b[31m", "")
			cleaned = strings.ReplaceAll(cleaned, "\u001b[33m", "")
			cleaned = strings.ReplaceAll(cleaned, "\u001b[0m", "")
			padding := widths[i] - len(cleaned)
			fmt.Fprintf(out, " %s%s |", val, strings.Repeat(" ", padding))
		}
		fmt.Fprintln(out)
	}
	printBenchmarkDivider(out, widths)
}

func printBenchmarkDivider(out io.Writer, widths []int) {
	fmt.Fprint(out, "+")
	for _, w := range widths {
		fmt.Fprint(out, strings.Repeat("-", w+2) + "+")
	}
	fmt.Fprintln(out)
}

func init() {
	benchmarkRunCmd.Flags().StringVarP(&benchmarkDataset, "dataset", "d", "bfcl", "Benchmark target dataset ('bfcl' or 'complexfuncbench')")
	benchmarkRunCmd.Flags().StringVarP(&benchmarkMode, "mode", "m", "consolidated", "Simulation evaluation mode ('consolidated' or 'interactive')")
	benchmarkRunCmd.Flags().StringVarP(&benchmarkModelMode, "model-mode", "t", "local", "Model routing execution tier ('local', 'cooperative', or 'cloud')")
	benchmarkRunCmd.Flags().BoolVarP(&benchmarkReal, "real", "r", false, "Run evaluation against actual LLM model endpoints without ground-truth mock completions")
	benchmarkRunCmd.Flags().IntVarP(&benchmarkLimit, "limit", "l", 0, "Limit the number of benchmark test cases to evaluate (0 for all)")
	benchmarkRunCmd.Flags().BoolVarP(&benchmarkVerbose, "verbose", "v", false, "Enable verbose output to stderr when JSON mode is on")

	benchmarkCmd.AddCommand(benchmarkRunCmd)
	RootCmd.AddCommand(benchmarkCmd)
}
