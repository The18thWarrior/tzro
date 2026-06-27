package comparison

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

//go:embed testdata/docgen_tasks.json
var taskDataFS embed.FS

// LoadTasks loads the documentation generation task suite from embedded testdata.
// If tierFilter > 0, only tasks matching that tier are returned.
func LoadTasks(tierFilter int) ([]ComparisonTask, error) {
	data, err := taskDataFS.ReadFile("testdata/docgen_tasks.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded task definitions: %w", err)
	}

	var tasks []ComparisonTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("failed to parse task definitions: %w", err)
	}

	if tierFilter <= 0 {
		return tasks, nil
	}

	var filtered []ComparisonTask
	for _, t := range tasks {
		if t.Tier == tierFilter {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no tasks found for tier %d", tierFilter)
	}
	return filtered, nil
}

// SuiteOptions configures a comparison benchmark run.
type SuiteOptions struct {
	Tier          int          // 0 = all tiers, 1-5 = specific tier
	Condition     string       // "" = all conditions, or specific condition ID
	OutputDir     string       // Directory to write results
	Pricing       PricingTable // Cloud model pricing
	JudgeEndpoint string       // Override judge API endpoint (for testing)
	ReactEndpoint string       // Override ReAct API endpoint (for testing)
}

// SuiteCallbacks provides optional progress callbacks.
type SuiteCallbacks struct {
	OnTaskStart     func(taskID, conditionID string)
	OnTaskComplete  func(result ComparisonResult)
	OnJudgeStart    func(taskID, conditionID string)
	OnJudgeComplete func(taskID, conditionID string, score float64)
}

// RunComparisonSuite runs the full comparison benchmark.
// It loads tasks, runs all conditions for each task sequentially,
// judges each output, and generates the final report.
func RunComparisonSuite(ctx context.Context, opts SuiteOptions, callbacks *SuiteCallbacks) ([]ComparisonResult, error) {
	tasks, err := LoadTasks(opts.Tier)
	if err != nil {
		return nil, fmt.Errorf("failed to load tasks: %w", err)
	}

	// Determine which conditions to run
	conditions := AllConditions()
	if opts.Condition != "" {
		conditions = []string{opts.Condition}
	}

	var allResults []ComparisonResult

	for _, task := range tasks {
		var taskResults []ComparisonResult

		for _, conditionID := range conditions {
			if callbacks != nil && callbacks.OnTaskStart != nil {
				callbacks.OnTaskStart(task.ID, conditionID)
			}

			var result ComparisonResult

			// Capture stderr during condition execution for log recording.
			// Uses os.Pipe + TeeReader so logs still appear on the terminal in real time.
			origStderr := os.Stderr
			pr, pw, pipeErr := os.Pipe()
			var logBuf bytes.Buffer
			var logWg sync.WaitGroup

			if pipeErr == nil {
				os.Stderr = pw
				logWg.Add(1)
				go func() {
					defer logWg.Done()
					_, _ = io.Copy(io.MultiWriter(origStderr, &logBuf), pr)
				}()
			}

			if conditionID == ConditionCloudReAct {
				if opts.ReactEndpoint != "" {
					result, err = RunReActWithEndpoint(ctx, task, opts.Pricing, opts.ReactEndpoint)
				} else {
					result, err = RunReAct(ctx, task, opts.Pricing)
				}
			} else {
				result, err = RunDAGCondition(ctx, conditionID, task, opts.Pricing)
			}

			// Restore stderr and collect captured logs
			if pipeErr == nil {
				pw.Close()
				logWg.Wait()
				pr.Close()
				os.Stderr = origStderr
			}

			capturedLogs := logBuf.String()

			if err != nil {
				// Record the error but continue with other conditions
				result = ComparisonResult{
					TaskID:    task.ID,
					TaskTier:  task.Tier,
					Condition: conditionID,
					Error:     fmt.Sprintf("execution failed: %v", err),
					Logs:      capturedLogs,
				}
			} else {
				result.Logs = capturedLogs
			}

			taskResults = append(taskResults, result)

			if callbacks != nil && callbacks.OnTaskComplete != nil {
				callbacks.OnTaskComplete(result)
			}
		}

		// Judge each result for this task
		for i := range taskResults {
			if taskResults[i].Error != "" || taskResults[i].OutputText == "" {
				continue
			}

			if callbacks != nil && callbacks.OnJudgeStart != nil {
				callbacks.OnJudgeStart(taskResults[i].TaskID, taskResults[i].Condition)
			}

			var score float64
			var notes string

			if opts.JudgeEndpoint != "" {
				score, notes, err = JudgeOutputWithEndpoint(ctx, taskResults[i].OutputText, task.QualityRubric, opts.JudgeEndpoint)
			} else {
				score, notes, err = JudgeOutput(ctx, taskResults[i].OutputText, task.QualityRubric)
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Judge error for %s/%s: %v\n",
					taskResults[i].TaskID, taskResults[i].Condition, err)
			} else {
				taskResults[i].QualityScore = score
				taskResults[i].QualityNotes = notes
			}

			if callbacks != nil && callbacks.OnJudgeComplete != nil {
				callbacks.OnJudgeComplete(taskResults[i].TaskID, taskResults[i].Condition, score)
			}
		}

		allResults = append(allResults, taskResults...)
	}

	// Generate report
	if opts.OutputDir != "" {
		if err := GenerateReport(allResults, opts.OutputDir); err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] Report generation failed: %v\n", err)
		}
	}

	return allResults, nil
}
