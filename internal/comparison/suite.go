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
var docgenTaskDataFS embed.FS

//go:embed testdata/codegen_tasks.json
var codegenTaskDataFS embed.FS

//go:embed testdata/codegen_seeds/*
var codegenSeedsFS embed.FS

// LoadTasks loads the documentation generation task suite from embedded testdata.
// If tierFilter > 0, only tasks matching that tier are returned.
// Deprecated: Use LoadTasksByCategory for new code.
func LoadTasks(tierFilter int) ([]ComparisonTask, error) {
	return LoadTasksByCategory(CategoryDocgen, tierFilter)
}

// LoadTasksByCategory loads tasks for the given category ("docgen" or "codegen").
// If tierFilter > 0, only tasks matching that tier are returned.
func LoadTasksByCategory(category string, tierFilter int) ([]ComparisonTask, error) {
	var data []byte
	var err error

	switch category {
	case CategoryCodegen:
		data, err = codegenTaskDataFS.ReadFile("testdata/codegen_tasks.json")
	case CategoryDocgen, "":
		data, err = docgenTaskDataFS.ReadFile("testdata/docgen_tasks.json")
	default:
		return nil, fmt.Errorf("unknown task category: %q", category)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded task definitions for %s: %w", category, err)
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
		return nil, fmt.Errorf("no tasks found for category %s tier %d", category, tierFilter)
	}
	return filtered, nil
}

// ReadSeedFile reads a codegen seed file from embedded testdata.
// The name should match the seedFile field in a codegen task (e.g. "validate_struct.go").
func ReadSeedFile(name string) ([]byte, error) {
	data, err := codegenSeedsFS.ReadFile("testdata/codegen_seeds/" + name)
	if err != nil {
		return nil, fmt.Errorf("failed to read seed file %q: %w", name, err)
	}
	return data, nil
}



// SuiteOptions configures a comparison benchmark run.
type SuiteOptions struct {
	Category      string       // "" = both docgen and codegen; or "docgen" / "codegen" for single category
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

// conditionsForCategory returns the condition list for a given category,
// respecting an optional single-condition override.
func conditionsForCategory(category, conditionOverride string) []string {
	if conditionOverride != "" {
		return []string{conditionOverride}
	}
	if category == CategoryCodegen {
		return CodegenConditions()
	}
	return AllConditions()
}

// RunComparisonSuite runs the full comparison benchmark.
// It loads tasks, runs all conditions for each task sequentially,
// judges each output, and generates the final report.
//
// When Category is "" (CategoryAll), both docgen and codegen tasks are run,
// each with their appropriate condition set.
func RunComparisonSuite(ctx context.Context, opts SuiteOptions, callbacks *SuiteCallbacks) ([]ComparisonResult, error) {
	// Build a list of (category, tasks, conditions) groups to run.
	type categoryGroup struct {
		category   string
		tasks      []ComparisonTask
		conditions []string
	}

	var groups []categoryGroup

	switch opts.Category {
	case CategoryAll:
		// Run both categories with their respective condition sets
		for _, cat := range []string{CategoryDocgen, CategoryCodegen} {
			tasks, err := LoadTasksByCategory(cat, opts.Tier)
			if err != nil {
				// If a category has no tasks for this tier, skip it rather than failing
				fmt.Fprintf(os.Stderr, "[Comparison] Skipping %s: %v\n", cat, err)
				continue
			}
			conds := conditionsForCategory(cat, opts.Condition)
			groups = append(groups, categoryGroup{category: cat, tasks: tasks, conditions: conds})
		}
		if len(groups) == 0 {
			return nil, fmt.Errorf("no tasks found for any category at tier %d", opts.Tier)
		}
	default:
		tasks, err := LoadTasksByCategory(opts.Category, opts.Tier)
		if err != nil {
			return nil, fmt.Errorf("failed to load tasks: %w", err)
		}
		conds := conditionsForCategory(opts.Category, opts.Condition)
		groups = append(groups, categoryGroup{category: opts.Category, tasks: tasks, conditions: conds})
	}

	var allResults []ComparisonResult

	for _, g := range groups {
		for _, task := range g.tasks {
			var err error
			var taskResults []ComparisonResult

			for _, conditionID := range g.conditions {
				// For codegen tasks without a condition override, use tier-based
				// routing (all tiers use the same conditions since benchmark #8).
				if g.category == CategoryCodegen && opts.Condition == "" {
					tierConditions := CodegenConditionsForTier(task.Tier)
					found := false
					for _, tc := range tierConditions {
						if tc == conditionID {
							found = true
							break
						}
					}
					if !found {
						continue // skip conditions not applicable to this tier
					}
				}

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
				} else if conditionID == ConditionTzroCode {
					result, err = RunCodegenCondition(ctx, conditionID, "cooperative", task, opts.Pricing)
				} else if conditionID == ConditionCloudCode {
					result, err = RunCodegenCondition(ctx, conditionID, "cloud", task, opts.Pricing)
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

			// Judge each result for this task, using the task's category for prompt selection
			for i := range taskResults {
				if taskResults[i].Error != "" || taskResults[i].OutputText == "" {
					continue
				}

				if callbacks != nil && callbacks.OnJudgeStart != nil {
					callbacks.OnJudgeStart(taskResults[i].TaskID, taskResults[i].Condition)
				}

				var score float64
				var notes string

				score, notes, err = JudgeOutputWithOptions(ctx, taskResults[i].OutputText, task.QualityRubric, opts.JudgeEndpoint, g.category)

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
	}

	// Generate report
	if opts.OutputDir != "" {
		if err := GenerateReport(allResults, opts.OutputDir); err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] Report generation failed: %v\n", err)
		}
	}

	return allResults, nil
}
