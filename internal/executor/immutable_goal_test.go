package executor

import (
	"strings"
	"testing"

	"tzro/internal/compiler"
)

func TestImmutableGoalPrompt_Formatting(t *testing.T) {
	prompt := "Research Go CVEs in 2024-2025. Round all percentages to 1 decimal place. Save to cves.md."
	formatted := FormatGoalPromptContext(prompt)

	if !strings.Contains(formatted, "## Primary User Specification (Authoritative Task Goal)") {
		t.Errorf("expected header '## Primary User Specification', got: %s", formatted)
	}
	if !strings.Contains(formatted, "Round all percentages to 1 decimal place") {
		t.Errorf("expected verbatim prompt constraints preserved, got: %s", formatted)
	}

	empty := FormatGoalPromptContext("")
	if empty != "" {
		t.Errorf("expected empty string for empty goal prompt, got: %q", empty)
	}
}

func TestImmutableGoalPrompt_InjectedIntoUserPrompt(t *testing.T) {
	goal := "Analyze LeadSuccess.csv and calculate conversion rate grouped by country."
	accCtx := "--- node_1 (read_file) [completed] ---\ncountry,leads\nUS,100"
	instruction := "Synthesize the conversion rate."

	prompt := buildContextAwareUserPromptWithGoal(goal, accCtx, "", instruction)

	if !strings.Contains(prompt, "## Primary User Specification (Authoritative Task Goal)") {
		t.Errorf("expected Primary User Specification section in user prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, goal) {
		t.Errorf("expected verbatim goal %q in user prompt, got: %s", goal, prompt)
	}
	if !strings.Contains(prompt, "## Accumulated Context from Prior Steps") {
		t.Errorf("expected Accumulated Context section in user prompt, got: %s", prompt)
	}
}

func TestImmutableGoalPrompt_SynthesisStrategyIntegration(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID:     "task_immutable_goal_test",
		GoalPrompt: "Generate a comprehensive README.md with CLI quickstart and API reference.",
		Nodes: []compiler.GraphNode{
			{
				ID:           "synthesis",
				Type:         "synthesis",
				Instructions: "Compile all prior findings into markdown.",
			},
		},
	}

	prompt := buildSynthesisPrompt(graph, "synthesis", "Accumulated context body")
	if !strings.Contains(prompt, "## Primary User Specification (Authoritative Task Goal)") {
		t.Errorf("expected synthesis prompt to contain Primary User Specification, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Generate a comprehensive README.md with CLI quickstart") {
		t.Errorf("expected verbatim goal prompt in synthesis prompt, got:\n%s", prompt)
	}
}
