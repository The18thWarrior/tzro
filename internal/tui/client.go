package tui

import (
	"context"
	"errors"
	"tzro/internal/compiler"
	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/stream"
)

// TaskStateItem mirrors the server's task payload structure.
type TaskStateItem struct {
	TaskID    string                  `json:"taskId"`
	Graph     *compiler.ExecutionGraph `json:"graph"`
	States    map[string]interface{}  `json:"states"`
	CreatedAt int64                   `json:"createdAt"`
}

// MemoryPayload mirrors the server's memory payload structure.
type MemoryPayload struct {
	Facts []memory.FactMemory `json:"facts"`
	Nodes []memory.KGNode     `json:"nodes"`
	Edges []memory.KGEdge     `json:"edges"`
}

// SidecarStatus mirrors the local model sidecar state.
type SidecarStatus struct {
	ActivePort       int    `json:"activePort"`
	ActivePID        int    `json:"activePid"`
	Status           string `json:"status"`
	ManifestProgress int    `json:"manifestProgress"`
	GGUFModelPath    string `json:"ggufModelPath"`
}

// TZROClient abstracts data-access logic for the CLI and TUI.
type TZROClient interface {
	GetTasks() ([]TaskStateItem, error)
	GetMemories() (MemoryPayload, error)
	AddMemory(userId, memType, content, context string, confidence float64) error
	GetSkills() ([]memory.Skill, error)
	GetMCPList() (map[string]mcp.MCPServerConfig, error)
	GetSidecarStatus() (SidecarStatus, error)
	GetNotifications() ([]memory.DurableNotification, error)
	GetEventsStream(ctx context.Context) (<-chan stream.StreamChunk, error)
	TriggerWorkflow(workflowId string) error
	ToggleWorkflow(workflowId string) error
}

// ErrOfflineMutation is returned when attempting a write mutation in offline mode.
var ErrOfflineMutation = errors.New("[Offline Mode] Mutations are disabled. Please start the tzrod daemon and run this command in connected mode.")
