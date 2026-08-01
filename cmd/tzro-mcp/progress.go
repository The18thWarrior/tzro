package main

import (
	"tzro/internal/memory"
	"tzro/internal/stream"
)

// TaskProgress provides structured progress metadata for enriched
// resources/read responses.
type TaskProgress struct {
	Completed int    `json:"completed"` // nodes with status "completed"
	Running   int    `json:"running"`   // nodes with status "running"
	Failed    int    `json:"failed"`    // nodes with status "failed"
	Pending   int    `json:"pending"`   // nodes with status "pending"
	Total     int    `json:"total"`     // total node count
	Status    string `json:"status"`    // overall task status: "pending", "running", "completed", "failed"
}

// computeProgress derives structured progress from the set of node states.
func computeProgress(nodes []memory.NodeState) TaskProgress {
	p := TaskProgress{Total: len(nodes)}
	for _, n := range nodes {
		switch n.Status {
		case "completed":
			p.Completed++
		case "running":
			p.Running++
		case "failed":
			p.Failed++
		case "pending", "":
			p.Pending++
		}
	}

	// Derive overall status
	switch {
	case p.Failed > 0:
		p.Status = "failed"
	case p.Running > 0:
		p.Status = "running"
	case p.Completed == p.Total && p.Total > 0:
		p.Status = "completed"
	default:
		p.Status = "pending"
	}

	return p
}

// recordChunkEvent converts a StreamChunk from the bus into a TaskEvent
// and records it in the global event buffer.
func recordChunkEvent(chunk stream.StreamChunk) {
	if chunk.TaskID == "" {
		return
	}

	// Map StreamChunk.Type to structured TaskEvent types
	eventType := chunk.Type
	detail := ""

	switch chunk.Type {
	case "node_started", "node_completed", "node_failed", "node_skipped":
		// These map directly
	case "tool_dispatch", "tool_result":
		detail = chunk.Content
	case "thought_step":
		// Truncate thought content for the event log
		if len(chunk.Content) > 200 {
			detail = chunk.Content[:200] + "..."
		} else {
			detail = chunk.Content
		}
	case "task_started", "task_completed", "task_failed", "task_cancelled":
		// Task lifecycle events
	default:
		// Record all other event types as-is
	}

	taskEventBuffer.Record(chunk.TaskID, TaskEvent{
		Type:   eventType,
		NodeID: chunk.NodeID,
		Detail: detail,
	})
}
