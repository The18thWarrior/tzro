package workflow

import "tzro/pkg/benchmark/signaldensity"

// DefaultTasks reuse the diagnostic fixtures with native edit-in-place instructions.
// Every profile receives exactly the same task and grading files.
func DefaultTasks() []Task {
	cases, _ := signaldensity.LoadWorkflowCases()
	goals := map[string]string{
		"macro_1_cache_impl":      "Implement NewMemoryDriver so it satisfies the Driver interface.",
		"macro_2_rate_bugfix":     "Fix the rate limiter so every available burst token can be used.",
		"macro_3_schema_refactor": "Add a Tags slice of strings to Item while retaining existing behavior.",
		"macro_4_auth_diagnosis":  "Diagnose and fix the authentication failure for valid claims.",
	}
	var tasks []Task
	for _, c := range cases {
		tasks = append(tasks, Task{ID: c.ID, Prompt: goals[c.ID] + " Edit the files in this workspace and run the Go tests. Preserve the provided tests and go.mod.", Files: c.Scaffold})
	}
	return tasks
}
