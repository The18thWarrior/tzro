package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/task"
)

// ExecuteWorkflow initializes a new workflow execution run and drives it.
func ExecuteWorkflow(ctx context.Context, wfID string) error {
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

	tasks, err := memory.DB.GetWorkflowTasks(wfID)
	if err != nil {
		return err
	}

	execID := fmt.Sprintf("wf_exec_%d", time.Now().UnixNano())
	exec := memory.WorkflowExecution{
		ID:         execID,
		WorkflowID: wfID,
		Status:     "running",
		StartedAt:  time.Now().Unix(),
	}

	var taskRuns []memory.WorkflowTaskExecution
	for _, t := range tasks {
		taskRuns = append(taskRuns, memory.WorkflowTaskExecution{
			WorkflowExecutionID: execID,
			TaskTemplateID:      t.TaskTemplateID,
			Status:              "pending",
			StartedAt:           time.Now().Unix(),
		})
	}

	if err := memory.DB.CreateWorkflowExecution(exec, taskRuns); err != nil {
		return err
	}

	publishWorkflowState(wfID, execID, "running", "", "", "")

	// Drive the execution loop asynchronously or synchronously (we drive it in place as goroutine already spawned)
	return runWorkflowLoop(ctx, execID)
}

// ResumeWorkflow resumes a previously interrupted running execution.
func ResumeWorkflow(ctx context.Context, execID string) error {
	exec, _, err := memory.DB.GetWorkflowExecutionDetails(execID)
	if err != nil {
		return err
	}
	if exec == nil || exec.Status != "running" {
		return nil
	}

	fmt.Printf("[Orchestrator] Resuming interrupted workflow execution: %s\n", execID)
	publishWorkflowState(exec.WorkflowID, execID, "running", "", "", "")

	return runWorkflowLoop(ctx, execID)
}

// RecoverInterruptedWorkflows scans and resumes interrupted workflows on boot.
func RecoverInterruptedWorkflows(ctx context.Context) {
	executions, err := memory.DB.GetWorkflowExecutions("")
	if err != nil {
		fmt.Printf("[Orchestrator Recovery Error] Failed to scan executions: %v\n", err)
		return
	}

	for _, exec := range executions {
		if exec.Status == "running" {
			go func(eID string) {
				if err := ResumeWorkflow(context.Background(), eID); err != nil {
					fmt.Printf("[Orchestrator Recovery Error] Resumption failed for %s: %v\n", eID, err)
				}
			}(exec.ID)
		}
	}
}

func runWorkflowLoop(ctx context.Context, execID string) error {
	for {
		exec, taskRuns, err := memory.DB.GetWorkflowExecutionDetails(execID)
		if err != nil {
			return err
		}

		if exec.Status == "completed" || exec.Status == "failed" || exec.Status == "cancelled" {
			return nil
		}

		tasks, err := memory.DB.GetWorkflowTasks(exec.WorkflowID)
		if err != nil {
			return err
		}

		statusMap := make(map[string]string)
		for _, tr := range taskRuns {
			statusMap[tr.TaskTemplateID] = tr.Status
		}

		var readyTasks []memory.WorkflowTask
		var runningCount int
		var failedCount int
		var completedCount int

		for _, t := range tasks {
			status := statusMap[t.TaskTemplateID]
			switch status {
			case "completed":
				completedCount++
			case "failed":
				failedCount++
			case "running":
				runningCount++
			case "pending", "":
				// Check dependencies
				depsMet := true
				if t.Dependencies != "" {
					deps := strings.Split(t.Dependencies, ",")
					for _, dep := range deps {
						dep = strings.TrimSpace(dep)
						if dep != "" && statusMap[dep] != "completed" {
							depsMet = false
							break
						}
					}
				}
				if depsMet {
					readyTasks = append(readyTasks, t)
				}
			}
		}

		if failedCount > 0 {
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "failed", time.Now().Unix())
			publishWorkflowState(exec.WorkflowID, execID, "failed", "", "", "")
			return fmt.Errorf("workflow execution failed due to task failures")
		}

		if completedCount == len(tasks) {
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "completed", time.Now().Unix())
			publishWorkflowState(exec.WorkflowID, execID, "completed", "", "", "")
			return nil
		}

		if len(readyTasks) == 0 && runningCount == 0 {
			_ = memory.DB.UpdateWorkflowExecutionStatus(execID, "failed", time.Now().Unix())
			publishWorkflowState(exec.WorkflowID, execID, "failed", "", "", "")
			return fmt.Errorf("workflow execution deadlocked: no tasks running or ready")
		}

		// Fire ready tasks
		for _, t := range readyTasks {
			taskExecID := fmt.Sprintf("task_%d_%s", time.Now().Unix(), t.TaskTemplateID)
			_ = memory.DB.UpdateWorkflowTaskExecution(execID, t.TaskTemplateID, taskExecID, "running", 0)
			publishWorkflowState(exec.WorkflowID, execID, "running", t.TaskTemplateID, "running", taskExecID)

			go func(wfTask memory.WorkflowTask, tExecID string) {
				// Re-fetch latest task runs for variable interpolation
				_, latestTaskRuns, _ := memory.DB.GetWorkflowExecutionDetails(execID)
				if latestTaskRuns == nil {
					latestTaskRuns = taskRuns
				}

				interpolatedInstructions := interpolateWorkflowVariables(wfTask.Instructions, execID, latestTaskRuns)
				fmt.Printf("[Orchestrator] Launching Task: %s -> Goal: %s\n", wfTask.TaskTemplateID, interpolatedInstructions)

				_, _, execErr := task.Execute(ctx, interpolatedInstructions, task.ExecuteOptions{
					TaskID: tExecID,
				})

				completedAt := time.Now().Unix()
				if execErr != nil {
					_ = memory.DB.UpdateWorkflowTaskExecution(execID, wfTask.TaskTemplateID, tExecID, "failed", completedAt)
					publishWorkflowState(exec.WorkflowID, execID, "running", wfTask.TaskTemplateID, "failed", tExecID)
				} else {
					_ = memory.DB.UpdateWorkflowTaskExecution(execID, wfTask.TaskTemplateID, tExecID, "completed", completedAt)
					publishWorkflowState(exec.WorkflowID, execID, "running", wfTask.TaskTemplateID, "completed", tExecID)
				}
			}(t, taskExecID)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

type WorkflowStateUpdate struct {
	ExecutionID     string `json:"executionId"`
	WorkflowID      string `json:"workflowId"`
	Status          string `json:"status"`
	TaskTemplateID  string `json:"taskTemplateId,omitempty"`
	TaskStatus      string `json:"taskStatus,omitempty"`
	TaskExecutionID string `json:"taskExecutionId,omitempty"`
}

func publishWorkflowState(wfID, execID, status, taskTemplateID, taskStatus, taskExecID string) {
	update := WorkflowStateUpdate{
		ExecutionID:     execID,
		WorkflowID:      wfID,
		Status:          status,
		TaskTemplateID:  taskTemplateID,
		TaskStatus:      taskStatus,
		TaskExecutionID: taskExecID,
	}

	payload, err := json.Marshal(update)
	if err == nil {
		stream.GlobalBus.Publish(stream.StreamChunk{
			Source:  "workflow_orchestrator",
			Type:    "workflow_state",
			TaskID:  wfID,
			NodeID:  taskTemplateID,
			Content: string(payload),
		})
	}
}

func interpolateWorkflowVariables(instructions string, execID string, taskRuns []memory.WorkflowTaskExecution) string {
	reProp := regexp.MustCompile(`\{\{tasks\.([^.]+)\.output\.([^}]+)\}\}`)
	instructions = reProp.ReplaceAllStringFunc(instructions, func(match string) string {
		submatches := reProp.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		taskTemplateID := submatches[1]
		propertyKey := submatches[2]

		var taskExecID string
		for _, tr := range taskRuns {
			if tr.TaskTemplateID == taskTemplateID {
				taskExecID = tr.TaskExecutionID
				break
			}
		}
		if taskExecID == "" {
			return "null"
		}

		rawOutput, err := memory.DB.GetLatestNodeOutput(taskExecID)
		if err != nil || rawOutput == "" {
			return "null"
		}

		// Remove execution tier prefix like "[Local Tactician] " or "[Cloud Fallback] "
		if idx := strings.Index(rawOutput, "] "); idx != -1 {
			rawOutput = rawOutput[idx+2:]
		}

		var outputMap map[string]interface{}
		if err := json.Unmarshal([]byte(rawOutput), &outputMap); err != nil {
			// Try parsing as simple string or fallback
			return rawOutput
		}

		val, found := outputMap[propertyKey]
		if !found {
			return "null"
		}
		if mVal, ok := val.(map[string]interface{}); ok {
			b, _ := json.Marshal(mVal)
			return string(b)
		}
		if aVal, ok := val.([]interface{}); ok {
			b, _ := json.Marshal(aVal)
			return string(b)
		}
		return fmt.Sprintf("%v", val)
	})

	reFull := regexp.MustCompile(`\{\{tasks\.([^.]+)\.output\}\}`)
	instructions = reFull.ReplaceAllStringFunc(instructions, func(match string) string {
		submatches := reFull.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		taskTemplateID := submatches[1]

		var taskExecID string
		for _, tr := range taskRuns {
			if tr.TaskTemplateID == taskTemplateID {
				taskExecID = tr.TaskExecutionID
				break
			}
		}
		if taskExecID == "" {
			return "null"
		}

		rawOutput, err := memory.DB.GetLatestNodeOutput(taskExecID)
		if err != nil || rawOutput == "" {
			return "null"
		}

		if idx := strings.Index(rawOutput, "] "); idx != -1 {
			rawOutput = rawOutput[idx+2:]
		}
		return rawOutput
	})

	return instructions
}
