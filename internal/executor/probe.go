package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// ProbeInferenceEngine abstracts the inference call for testability.
// In production, this wraps InferenceBackend.CallModel. In tests,
// a mock returns canned GBNF-constrained JSON responses.
type ProbeInferenceEngine interface {
	Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error)
}

// DefaultProbeInference wraps the global inference backend for production use.
type DefaultProbeInference struct{}

func (d *DefaultProbeInference) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	backend := inference.ActiveBackend
	if backend == nil {
		return "", fmt.Errorf("no active inference backend")
	}
	result, err := backend.CallModel(ctx, systemPrompt, userPrompt, jsonSchema)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// ThoughtChainStep is the GBNF-constrained JSON output schema for each
// Thought Chain inference step. The Local Model must produce output
// conforming to this schema on every call.
type ThoughtChainStep struct {
	Action      string                 `json:"action"`              // "tool_call" | "synthesize"
	Tool        string                 `json:"tool,omitempty"`      // tool name (when action == "tool_call")
	Arguments   map[string]interface{} `json:"arguments,omitempty"` // tool arguments JSON map
	NextThought string                 `json:"nextThought"`         // reasoning for the next step
	Confidence  float64                `json:"confidence"`          // 0.0 - 1.0 convergence signal
	Synthesis   string                 `json:"synthesis,omitempty"` // final output (when action == "synthesize")
}

// UnmarshalJSON implements custom unmarshaling to handle arguments as either a JSON string or a JSON object.
func (t *ThoughtChainStep) UnmarshalJSON(data []byte) error {
	type Alias ThoughtChainStep
	aux := struct {
		Arguments interface{} `json:"arguments"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Arguments != nil {
		switch v := aux.Arguments.(type) {
		case map[string]interface{}:
			t.Arguments = v
		case string:
			if v != "" {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(v), &parsed); err != nil {
					t.Arguments = map[string]interface{}{"query": v}
				} else {
					t.Arguments = parsed
				}
			}
		}
	}
	return nil
}

// ThoughtChainStepSchema is the JSON schema that constrains Local Model
// output for each Thought Chain step via GBNF grammar.
const ThoughtChainStepSchema = `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["tool_call", "synthesize"]
		},
		"tool": { "type": "string" },
		"arguments": { "type": "object" },
		"nextThought": { "type": "string" },
		"confidence": { "type": "number" },
		"synthesis": { "type": "string" }
	},
	"required": ["action", "nextThought", "confidence"]
}`

// RunProbe executes a Probe Node's Thought Chain loop.
//
// Each step:
//  1. Build prompt from goal + latest summary + recent thoughts + tool output
//  2. Call Local Model with GBNF-constrained schema → ThoughtChainStep
//  3. If action == "tool_call": call tool, persist step with output
//  4. If action == "synthesize" && confidence >= 0.9: return synthesis
//  5. Every N steps: rolling compaction summary
//  6. At budget exhaustion: forced synthesis
//
// All steps are persisted to SQLite for durability.
func RunProbe(
	ctx context.Context,
	taskID string,
	probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
) (string, error) {
	// Defaults
	stepBudget := config.StepBudget
	if stepBudget <= 0 {
		stepBudget = 10
	}
	compactEvery := config.CompactEvery
	if compactEvery <= 0 {
		compactEvery = 3
	}

	// Build allowed tools set for validation
	allowedToolSet := make(map[string]bool)
	for _, t := range config.AllowedTools {
		allowedToolSet[t] = true
	}

	// Build dynamic GBNF schema for thought step
	var toolEnum string
	if len(config.AllowedTools) > 0 {
		var toolNames []string
		for _, t := range config.AllowedTools {
			toolNames = append(toolNames, fmt.Sprintf("%q", t))
		}
		toolEnum = fmt.Sprintf(`"type": "string", "enum": [%s]`, strings.Join(toolNames, ", "))
	} else {
		toolEnum = `"type": "string"`
	}

	stepSchema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["tool_call", "synthesize"]
			},
			"tool": { %s },
			"arguments": { "type": "object" },
			"nextThought": { "type": "string" },
			"confidence": { "type": "number" },
			"synthesis": { "type": "string" }
		},
		"required": ["action", "nextThought", "confidence"]
	}`, toolEnum)

	var lastToolOutput string

	for step := 1; step <= stepBudget; step++ {
		// 1. Build the prompt context
		systemPrompt := buildProbeSystemPrompt(config.Goal, config.AllowedTools)
		userPrompt, err := buildProbeUserPrompt(probeID, step, lastToolOutput)
		if err != nil {
			return "", fmt.Errorf("failed to build probe prompt at step %d: %w", step, err)
		}

		// 2. Call Local Model with GBNF constraint
		rawResponse, err := engine.Infer(ctx, systemPrompt, userPrompt, stepSchema)
		if err != nil {
			return "", fmt.Errorf("probe inference failed at step %d: %w", step, err)
		}

		// 3. Parse the structured response
		var chainStep ThoughtChainStep
		if err := json.Unmarshal([]byte(rawResponse), &chainStep); err != nil {
			return "", fmt.Errorf("failed to parse probe step %d response: %w", step, err)
		}

		// 4. Execute tool call if requested
		toolOutput := ""
		var toolArgsStr string
		if chainStep.Action == "tool_call" && chainStep.Tool != "" {
			// Validate tool is in allowed set
			if !allowedToolSet[chainStep.Tool] {
				toolOutput = fmt.Sprintf("Error: tool '%s' is not in the allowed tools set", chainStep.Tool)
			} else {
				// Parse arguments and call
				args := chainStep.Arguments
				if args == nil {
					args = map[string]interface{}{}
				}
				result, err := tools.Call(ctx, chainStep.Tool, args)
				if err != nil {
					toolOutput = fmt.Sprintf("Error: %v", err)
				} else {
					toolOutput = result
				}
				if bytes, err := json.Marshal(args); err == nil {
					toolArgsStr = string(bytes)
				}
			}
			lastToolOutput = toolOutput
		}

		// 5. Persist the thought step
		thoughtStep := memory.ThoughtStep{
			ID:         fmt.Sprintf("%s_step_%d", probeID, step),
			ProbeID:    probeID,
			TaskID:     taskID,
			StepIndex:  step,
			Thought:    chainStep.NextThought,
			ToolName:   chainStep.Tool,
			ToolArgs:   toolArgsStr,
			ToolOutput: toolOutput,
			CreatedAt:  time.Now().Unix(),
		}
		if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
			fmt.Fprintf(os.Stderr, "[Probe] Warning: failed to persist step %d: %v\n", step, err)
		}

		// 6. Check for convergence
		if chainStep.Action == "synthesize" && chainStep.Confidence >= 0.9 {
			fmt.Fprintf(os.Stderr, "[Probe] Node %s converged at step %d (confidence: %.2f)\n", probeID, step, chainStep.Confidence)
			return chainStep.Synthesis, nil
		}

		// 7. Rolling compaction every N steps
		if step%compactEvery == 0 {
			if err := compactThoughtChain(ctx, probeID, taskID, step, compactEvery, engine); err != nil {
				fmt.Fprintf(os.Stderr, "[Probe] Warning: compaction failed at step %d: %v\n", step, err)
			}
		}
	}

	// Budget exhaustion: force synthesis
	fmt.Fprintf(os.Stderr, "[Probe] Node %s budget exhausted (%d steps). Forcing synthesis.\n", probeID, stepBudget)
	return forceSynthesis(ctx, probeID, taskID, engine)
}

// buildProbeSystemPrompt constructs the system prompt for the probe's Local Model call.
func buildProbeSystemPrompt(goal string, allowedTools []string) string {
	toolList := ""
	for i, t := range allowedTools {
		if i > 0 {
			toolList += ", "
		}
		toolList += t
	}

	return fmt.Sprintf(`You are a Probe Node — an autonomous code exploration agent.
Your goal: %s

You have access to these tools: [%s]

On each step, produce a JSON object with:
- "action": either "tool_call" (to use a tool) or "synthesize" (to produce a final answer)
- "tool": the tool name (when action is "tool_call")
- "arguments": JSON-encoded tool arguments (when action is "tool_call")
- "nextThought": your reasoning about what to explore next
- "confidence": 0.0-1.0 indicating how confident you are that you have enough information to answer
- "synthesis": your final answer (when action is "synthesize" and confidence >= 0.9)

Be systematic. Build understanding incrementally. Only synthesize when confident.`, goal, toolList)
}

// buildProbeUserPrompt builds the user prompt from persisted thought chain state.
func buildProbeUserPrompt(probeID string, stepNum int, lastToolOutput string) (string, error) {
	var prompt string

	// Include latest compaction summary if available
	summary, err := memory.DB.GetLatestSummary(probeID)
	if err == nil && summary.Summary != "" {
		prompt += fmt.Sprintf("## Previous Exploration Summary\n%s\n\n", summary.Summary)
	}

	// Include recent unccompacted steps
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err == nil && len(steps) > 0 {
		// Only include the most recent steps (after last compaction)
		recentStart := 0
		if len(steps) > 5 {
			recentStart = len(steps) - 5
		}
		prompt += "## Recent Steps\n"
		for _, s := range steps[recentStart:] {
			prompt += fmt.Sprintf("- Step %d: %s", s.StepIndex, s.Thought)
			if s.ToolName != "" {
				prompt += fmt.Sprintf(" [used: %s]", s.ToolName)
			}
			prompt += "\n"
		}
		prompt += "\n"
	}

	// Include last tool output
	if lastToolOutput != "" {
		// Cap tool output to prevent context explosion
		if len(lastToolOutput) > 4000 {
			lastToolOutput = lastToolOutput[:4000] + "\n... (truncated)"
		}
		prompt += fmt.Sprintf("## Last Tool Output\n```\n%s\n```\n\n", lastToolOutput)
	}

	prompt += fmt.Sprintf("Step %d: What should we do next?", stepNum)
	return prompt, nil
}

// compactThoughtChain creates a rolling summary of recent thought chain steps.
func compactThoughtChain(ctx context.Context, probeID, taskID string, currentStep, window int, engine ProbeInferenceEngine) error {
	startStep := currentStep - window + 1
	if startStep < 1 {
		startStep = 1
	}

	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil {
		return err
	}

	// Collect steps in the compaction window
	var windowSteps []memory.ThoughtStep
	for _, s := range steps {
		if s.StepIndex >= startStep && s.StepIndex <= currentStep {
			windowSteps = append(windowSteps, s)
		}
	}

	if len(windowSteps) == 0 {
		return nil
	}

	// Build compaction prompt
	var stepsText string
	for _, s := range windowSteps {
		stepsText += fmt.Sprintf("Step %d: %s", s.StepIndex, s.Thought)
		if s.ToolName != "" {
			stepsText += fmt.Sprintf(" → %s(%s) → %s", s.ToolName, s.ToolArgs, truncate(s.ToolOutput, 200))
		}
		stepsText += "\n"
	}

	systemPrompt := "Compress the following exploration steps into a concise summary preserving all key findings and discoveries. Output only the summary text."
	userPrompt := stepsText

	summaryText, err := engine.Infer(ctx, systemPrompt, userPrompt, "")
	if err != nil {
		return fmt.Errorf("compaction inference failed: %w", err)
	}

	summary := memory.ThoughtSummary{
		ID:        fmt.Sprintf("%s_summary_%d_%d", probeID, startStep, currentStep),
		ProbeID:   probeID,
		TaskID:    taskID,
		StepRange: fmt.Sprintf("%d-%d", startStep, currentStep),
		Summary:   summaryText,
		CreatedAt: time.Now().Unix(),
	}

	return memory.DB.AddThoughtSummary(summary)
}

// forceSynthesis generates a forced synthesis when the step budget is exhausted.
func forceSynthesis(ctx context.Context, probeID, taskID string, engine ProbeInferenceEngine) (string, error) {
	// Gather all state
	summary, _ := memory.DB.GetLatestSummary(probeID)
	steps, _ := memory.DB.GetThoughtSteps(probeID)

	var context string
	if summary.Summary != "" {
		context += "Summary: " + summary.Summary + "\n"
	}
	for _, s := range steps {
		context += fmt.Sprintf("Step %d: %s\n", s.StepIndex, s.Thought)
	}

	systemPrompt := "You have exhausted your exploration budget. Based on everything discovered so far, produce a comprehensive synthesis of your findings. Be thorough and include all relevant details."
	result, err := engine.Infer(ctx, systemPrompt, context, "")
	if err != nil {
		return "Probe budget exhausted. Unable to synthesize findings.", nil
	}
	return result, nil
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
