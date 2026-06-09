package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"tzro/internal/memory"
	"tzro/internal/notification"
)

// LLMClient decouples structured inference from the orchestrator.
// This is the same interface as agent.LLMClient to avoid a circular dependency.
type LLMClient interface {
	CallModel(ctx context.Context, systemPrompt, userPrompt string, jsonSchema string) (string, error)
}

// TaskRunner abstracts child task execution, enabling mock injection in tests.
type TaskRunner interface {
	Run(ctx context.Context, instruction string, taskID string) error
}

// LLMDecision represents the structured response from the LLM orchestrator call.
type LLMDecision struct {
	Action      string `json:"action"`      // "next_task" | "goal_achieved"
	Instruction string `json:"instruction"` // next task instruction (when action=next_task)
	Summary     string `json:"summary"`     // completion summary (when action=goal_achieved)
}

// LLMDecisionSchema is the JSON schema used to constrain LLM orchestrator decisions.
const LLMDecisionSchema = `{
	"type": "object",
	"properties": {
		"action": {"type": "string", "enum": ["next_task", "goal_achieved"]},
		"instruction": {"type": "string"},
		"summary": {"type": "string"}
	},
	"required": ["action"]
}`

const dynamicOrchestratorSystemPrompt = `You are a Dynamic Workflow Orchestrator. You are given a goal and the history of completed child tasks.

Based on the goal and what has been accomplished so far, decide the next action:
- If more work is needed, respond with action "next_task" and provide an instruction for the next child task.
- If the goal has been achieved, respond with action "goal_achieved" and provide a summary of what was accomplished.

Always respond with valid JSON matching the required schema.`

// ExecuteDynamicWorkflow runs a dynamic orchestration loop for a workflow.
// It calls the LLM after each child task to decide the next step or declare goal-achieved.
func ExecuteDynamicWorkflow(ctx context.Context, wfID string, llm LLMClient, runner TaskRunner) error {
	workflows, err := memory.DB.GetWorkflows()
	if err != nil {
		return err
	}

	var targetWf *memory.WorkflowDefinition
	for i := range workflows {
		if workflows[i].ID == wfID {
			targetWf = &workflows[i]
			break
		}
	}
	if targetWf == nil {
		return fmt.Errorf("workflow not found: %s", wfID)
	}

	if targetWf.OrchestrationMode != "dynamic" {
		return fmt.Errorf("workflow %s is not in dynamic orchestration mode", wfID)
	}

	execID := fmt.Sprintf("wf_dyn_exec_%d", time.Now().UnixNano())
	exec := memory.WorkflowExecution{
		ID:         execID,
		WorkflowID: wfID,
		Status:     "running",
		StartedAt:  time.Now().Unix(),
	}

	if err := memory.DB.CreateWorkflowExecution(exec, nil); err != nil {
		return err
	}

	publishWorkflowState(wfID, execID, "running", "", "", "")

	return runDynamicLoop(ctx, targetWf, execID, llm, runner)
}

func runDynamicLoop(ctx context.Context, wf *memory.WorkflowDefinition, execID string, llm LLMClient, runner TaskRunner) error {
	var taskHistory []string
	taskIndex := 0

	for {
		// Check context cancellation (preemption)
		select {
		case <-ctx.Done():
			// Leave workflow in "running" status for auto-resume
			return ctx.Err()
		default:
		}

		// Budget gate: check before LLM call
		exec, _, err := memory.DB.GetWorkflowExecutionDetails(execID)
		if err != nil {
			return err
		}

		if wf.MaxToolCalls > 0 && exec.ToolCallsConsumed >= wf.MaxToolCalls {
			summary := fmt.Sprintf("Budget exhausted after %d tool calls (limit: %d). Completed tasks: %d.",
				exec.ToolCallsConsumed, wf.MaxToolCalls, taskIndex)
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "completed", time.Now().Unix())
			publishWorkflowState(wf.ID, execID, "completed", "", "", "")

			_, _ = notification.Send(ctx, "workflow_orchestrator", "info",
				"Dynamic Workflow Budget Exhausted",
				summary,
				notification.WithTargetID(execID))
			return nil
		}

		if wf.MaxTokens > 0 && exec.TokensConsumed >= wf.MaxTokens {
			summary := fmt.Sprintf("Token budget exhausted (%d/%d tokens). Completed tasks: %d.",
				exec.TokensConsumed, wf.MaxTokens, taskIndex)
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "completed", time.Now().Unix())
			publishWorkflowState(wf.ID, execID, "completed", "", "", "")

			_, _ = notification.Send(ctx, "workflow_orchestrator", "info",
				"Dynamic Workflow Budget Exhausted",
				summary,
				notification.WithTargetID(execID))
			return nil
		}

		// Build user prompt with goal and history
		userPrompt := buildDynamicPrompt(wf.Goal, taskHistory)

		// Call LLM for decision
		llmResponse, err := llm.CallModel(ctx, dynamicOrchestratorSystemPrompt, userPrompt, LLMDecisionSchema)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "failed", time.Now().Unix())
			publishWorkflowState(wf.ID, execID, "failed", "", "", "")
			return fmt.Errorf("LLM decision call failed: %w", err)
		}

		// Estimate tokens consumed by LLM call (rough estimate)
		_ = memory.DB.UpdateWorkflowExecutionBudget(execID, estimateTokens(userPrompt, llmResponse), 0)

		var decision LLMDecision
		if err := json.Unmarshal([]byte(llmResponse), &decision); err != nil {
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "failed", time.Now().Unix())
			publishWorkflowState(wf.ID, execID, "failed", "", "", "")
			return fmt.Errorf("failed to parse LLM decision: %w", err)
		}

		switch decision.Action {
		case "goal_achieved":
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "completed", time.Now().Unix())
			publishWorkflowState(wf.ID, execID, "completed", "", "", "")

			_, _ = notification.Send(ctx, "workflow_orchestrator", "info",
				"Dynamic Workflow Goal Achieved",
				decision.Summary,
				notification.WithTargetID(execID))
			return nil

		case "next_task":
			taskIndex++
			taskTemplateID := fmt.Sprintf("dynamic_task_%d", taskIndex)
			taskExecID := fmt.Sprintf("dyn_task_%d_%d", time.Now().UnixNano(), taskIndex)

			// Record the child task run in the workflow execution
			childTaskRun := memory.WorkflowTaskExecution{
				WorkflowExecutionID: execID,
				TaskTemplateID:      taskTemplateID,
				TaskExecutionID:     taskExecID,
				Status:              "running",
				StartedAt:           time.Now().Unix(),
			}

			// Insert child task execution row
			_ = memory.DB.InsertWorkflowTaskExecution(childTaskRun)

			publishWorkflowState(wf.ID, execID, "running", taskTemplateID, "running", taskExecID)

			fmt.Printf("[Dynamic Orchestrator] Executing child task %d: %s\n", taskIndex, decision.Instruction)

			// Execute the child task
			execErr := runner.Run(ctx, decision.Instruction, taskExecID)

			// Record budget usage
			_ = memory.DB.UpdateWorkflowExecutionBudget(execID, 0, 1)

			completedAt := time.Now().Unix()
			if execErr != nil {
				if ctx.Err() != nil {
					// Preemption — mark child as interrupted, leave workflow running
					_ = memory.DB.UpdateWorkflowTaskExecution(execID, taskTemplateID, taskExecID, "interrupted", completedAt)
					return ctx.Err()
				}

				_ = memory.DB.UpdateWorkflowTaskExecution(execID, taskTemplateID, taskExecID, "failed", completedAt)
				taskHistory = append(taskHistory, fmt.Sprintf("Task %d FAILED: %s (Error: %v)", taskIndex, decision.Instruction, execErr))
				publishWorkflowState(wf.ID, execID, "running", taskTemplateID, "failed", taskExecID)

				// Consult LLM about the failure — the next loop iteration handles the LLM call with the failure in history
			} else {
				_ = memory.DB.UpdateWorkflowTaskExecution(execID, taskTemplateID, taskExecID, "completed", completedAt)
				taskHistory = append(taskHistory, fmt.Sprintf("Task %d COMPLETED: %s", taskIndex, decision.Instruction))
				publishWorkflowState(wf.ID, execID, "running", taskTemplateID, "completed", taskExecID)
			}

		default:
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "failed", time.Now().Unix())
			publishWorkflowState(wf.ID, execID, "failed", "", "", "")
			return fmt.Errorf("unknown LLM decision action: %s", decision.Action)
		}
	}
}

func buildDynamicPrompt(goal string, taskHistory []string) string {
	prompt := fmt.Sprintf("## Goal\n%s\n\n", goal)

	if len(taskHistory) > 0 {
		prompt += "## Completed Tasks\n"
		for _, h := range taskHistory {
			prompt += fmt.Sprintf("- %s\n", h)
		}
		prompt += "\n"
	} else {
		prompt += "## Status\nNo tasks have been executed yet. This is the initial decision.\n\n"
	}

	prompt += "## Instructions\nDecide what to do next. Respond with a JSON object containing 'action' and either 'instruction' (for next_task) or 'summary' (for goal_achieved)."
	return prompt
}

func estimateTokens(prompt, response string) int {
	// Rough estimate: ~4 characters per token
	return (len(prompt) + len(response)) / 4
}
