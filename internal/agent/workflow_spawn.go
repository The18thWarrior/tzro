package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/stream"
)

// WorkflowSpawnBudget holds budget limits for a spawned dynamic workflow.
type WorkflowSpawnBudget struct {
	MaxTokens    int
	MaxToolCalls int
}

// WorkflowSpawnRequest represents the payload for a workflow_spawn proposed action.
type WorkflowSpawnRequest struct {
	Goal          string              `json:"goal"`
	Budget        WorkflowSpawnBudget `json:"budget"`
	ApprovedLevel int                 `json:"approvedLevel"`
	SpawnedBy     string              `json:"spawnedBy"`
}

// SpawnWorkflow creates a dynamic workflow definition and emits a notification for tracking.
// The agent's MaxLevel (from its Daemon implementation) must be >= the requested approvedLevel.
// Returns the workflow ID on success.
func (b *BackgroundAgent) SpawnWorkflow(ctx context.Context, goal string, budget WorkflowSpawnBudget, approvedLevel int, maxLevel int) (string, error) {
	agentName := b.AgentName()

	// Gate: agent cannot approve a level higher than its own max level
	if approvedLevel > maxLevel {
		return "", fmt.Errorf("[%s] cannot spawn workflow at L%d: agent's MaxLevel is L%d", agentName, approvedLevel, maxLevel)
	}

	wfID := fmt.Sprintf("wf_dyn_%s_%d", agentName, time.Now().UnixNano())
	now := time.Now().Unix()

	wf := memory.WorkflowDefinition{
		ID:                wfID,
		Name:              fmt.Sprintf("Dynamic: %s", truncateGoal(goal, 60)),
		Description:       goal,
		TriggerType:       "background",
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
		OrchestrationMode: "dynamic",
		Goal:              goal,
		ApprovedLevel:     approvedLevel,
		MaxTokens:         budget.MaxTokens,
		MaxToolCalls:      budget.MaxToolCalls,
		SpawnedBy:         agentName,
	}

	if err := memory.DB.SaveWorkflow(wf, nil); err != nil {
		return "", fmt.Errorf("[%s] failed to save spawned workflow: %w", agentName, err)
	}

	// Emit a notification for tracking
	spawnReq := WorkflowSpawnRequest{
		Goal:          goal,
		Budget:        budget,
		ApprovedLevel: approvedLevel,
		SpawnedBy:     agentName,
	}
	payload, _ := json.Marshal(spawnReq)

	_, _ = notification.Send(ctx, agentName, "info",
		"Dynamic Workflow Spawned",
		fmt.Sprintf("Agent '%s' spawned workflow '%s' with goal: %s", agentName, wfID, truncateGoal(goal, 120)),
		notification.WithTargetID(wfID),
		notification.WithActionPayload(string(payload)))

	// Publish to StreamBus for real-time tracking
	b.mu.RLock()
	tm := b.telemetryMgr
	b.mu.RUnlock()
	if tm != nil {
		tm.PublishStream(stream.StreamChunk{
			Source:  agentName,
			Type:    "workflow_spawn",
			TaskID:  wfID,
			Content: fmt.Sprintf("Dynamic workflow spawned: %s", truncateGoal(goal, 80)),
		})
	}

	return wfID, nil
}

func truncateGoal(goal string, maxLen int) string {
	if len(goal) <= maxLen {
		return goal
	}
	return goal[:maxLen-3] + "..."
}
