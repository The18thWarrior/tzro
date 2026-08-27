package comparison

import (
	"context"
	"fmt"
	"os"
)

// RejudgeResults re-runs the LLM judge on results that had judge failures
// (JudgeError set or QualityScore <= 0) while leaving successful results
// unchanged. Returns a new slice with the same length.
func RejudgeResults(ctx context.Context, results []ComparisonResult, opts JudgeOptions) ([]ComparisonResult, int, error) {
	return RejudgeResultsWithOptions(ctx, results, RejudgeOptions{
		JudgeOptions: opts,
	})
}

// RejudgeResultsWithOptions evaluates deterministic checks and optionally re-runs the LLM judge
// on benchmark results based on the provided RejudgeOptions.
func RejudgeResultsWithOptions(ctx context.Context, results []ComparisonResult, opts RejudgeOptions) ([]ComparisonResult, int, error) {
	out := make([]ComparisonResult, len(results))
	copy(out, results)

	detWeight := opts.DetWeight
	if detWeight <= 0 {
		detWeight = DefaultDeterministicWeight
	}

	rejudged := 0
	for i := range out {
		// Load the task's definition from the embedded task suite
		task, err := findTaskByID(out[i].TaskID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Rejudge] Cannot find task %q in embedded suite: %v\n", out[i].TaskID, err)
			continue
		}

		// Run deterministic evaluation on all results
		detScorecard := EvaluateDeterministic(&out[i], task)
		out[i].DeterministicChecks = detScorecard
		out[i].DeterministicScore = detScorecard.OverallScore

		// Skip results with execution errors or no output
		if out[i].OutputText == "" || out[i].Error != "" {
			continue
		}

		// Deterministic-only mode: calculate composite scores without LLM calls
		if opts.DeterministicOnly {
			existingLLMScore := out[i].LLMScore
			if existingLLMScore <= 0 && out[i].QualityScore > 0 && out[i].JudgeError == "" {
				// Legacy result: previous QualityScore was purely LLM judge
				existingLLMScore = out[i].QualityScore
				out[i].LLMScore = existingLLMScore
			}

			compositeScore, _ := CalculateCompositeScore(detScorecard, existingLLMScore, detWeight)
			out[i].QualityScore = compositeScore
			rejudged++
			continue
		}

		// Determine if LLM rejudge is needed for this entry
		needsLLM := opts.All || out[i].JudgeError != "" || out[i].QualityScore <= 0
		if !needsLLM {
			if out[i].LLMScore <= 0 {
				out[i].LLMScore = out[i].QualityScore
			}
			// Update composite score with the fresh deterministic evaluation
			compositeScore, _ := CalculateCompositeScore(detScorecard, out[i].LLMScore, detWeight)
			out[i].QualityScore = compositeScore
			continue
		}

		fmt.Fprintf(os.Stderr, "[Rejudge] Re-judging %s / %s (previous: score=%.2f, judgeError=%q, detScore=%.2f)\n",
			out[i].TaskID, out[i].Condition, out[i].QualityScore, out[i].JudgeError, detScorecard.OverallScore)

		judgeOpts := JudgeOptions{
			Endpoint: opts.Endpoint,
			Category: task.Category,
			Model:    opts.Model,
			Prompt:   task.Prompt,
		}

		judgeOutput := out[i].OutputText
		if task.Category == CategoryDatanal && task.ExpectedAnswer != "" {
			judgeOutput = fmt.Sprintf("## Model Output\n\n%s\n\n## Expected Correct Answer\n\n%s",
				out[i].OutputText, task.ExpectedAnswer)
		}

		resp, judgeErr := JudgeOutputDetailedWithRetry(ctx, judgeOutput, task.QualityRubric, judgeOpts)
		if judgeErr != nil {
			fmt.Fprintf(os.Stderr, "[Rejudge] Judge still failing for %s / %s: %v\n",
				out[i].TaskID, out[i].Condition, judgeErr)
			out[i].JudgeError = "ERR_JUDGE_UNAVAILABLE"
			out[i].QualityScore = -1
			out[i].LLMScore = -1
			continue
		}

		// Update with new scores
		out[i].LLMScore = resp.OverallScore
		compositeScore, _ := CalculateCompositeScore(detScorecard, resp.OverallScore, detWeight)
		out[i].QualityScore = compositeScore
		out[i].GoalAlignment = resp.GoalAlignment
		out[i].FactualGrounding = resp.FactualGrounding
		out[i].Coherence = resp.Coherence
		out[i].Completeness = resp.Completeness
		out[i].QualityNotes = resp.Summary
		out[i].JudgeError = "" // Clear the error
		rejudged++
	}

	return out, rejudged, nil
}

// findTaskByID searches all category task suites for a task matching the given ID.
func findTaskByID(taskID string) (*ComparisonTask, error) {
	categories := []string{CategoryDocgen, CategoryCodegen, CategoryDatanal, CategoryResearch}

	for _, cat := range categories {
		// Try development set
		tasks, err := LoadTasksByCategory(cat, 0)
		if err == nil {
			for _, t := range tasks {
				if t.ID == taskID {
					return &t, nil
				}
			}
		}
		// Try holdout set
		tasks, err = LoadHoldoutTasks(cat, 0)
		if err == nil {
			for _, t := range tasks {
				if t.ID == taskID {
					return &t, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("task %q not found in any category", taskID)
}
