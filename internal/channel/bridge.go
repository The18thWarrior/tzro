package channel

import (
	"encoding/json"
	"time"

	"tzro/internal/stream"
)

// chunkContent is a generic structure for parsing chunk.Content JSON.
// Fields are optional — different event types populate different subsets.
type chunkContent struct {
	NodeType        string  `json:"nodeType"`
	Action          string  `json:"action"`
	Output          string  `json:"output"`
	Error           string  `json:"error"`
	Reason          string  `json:"reason"`
	NodeCount       int     `json:"nodeCount"`
	LevelCount      int     `json:"levelCount"`
	Synthesis       string  `json:"synthesisSnippet"`
	Confidence      float64 `json:"confidence"`
	GoalAchieved    bool    `json:"goalAchieved"`
	NodeID          string  `json:"nodeId"`
	SpawnedNodeID   string  `json:"spawnedNodeId"`
	RemainingBudget int     `json:"remainingBudget"`
}

// buildPayload attempts to parse content as JSON and construct the appropriate
// typed payload for the given event type. Returns nil if content is not valid JSON
// or if the event type has no defined payload schema.
func buildPayload(eventType, content string) json.RawMessage {
	var parsed chunkContent
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil
	}

	var payload interface{}

	switch eventType {
	case EventTaskStarted:
		payload = TaskStartedPayload{
			NodeCount:  parsed.NodeCount,
			LevelCount: parsed.LevelCount,
		}
	case EventTaskCompleted:
		payload = TaskCompletedPayload{
			SynthesisSnippet: parsed.Synthesis,
		}
	case EventTaskFailed:
		payload = TaskFailedPayload{Error: parsed.Error}
	case EventTaskPaused:
		payload = TaskPausedPayload{Reason: parsed.Reason}
	case EventNodeStarted:
		payload = NodeStartedPayload{
			NodeType: parsed.NodeType,
			Action:   parsed.Action,
		}
	case EventNodeCompleted:
		snippet := parsed.Output
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		payload = NodeCompletedPayload{
			NodeType:      parsed.NodeType,
			OutputSnippet: snippet,
		}
	case EventNodeFailed:
		payload = NodeFailedPayload{Error: parsed.Error}
	case EventNodeSkipped:
		payload = NodeSkippedPayload{Reason: parsed.Reason}
	case EventEdgeThought:
		payload = EdgeThoughtPayload{
			Confidence:   parsed.Confidence,
			GoalAchieved: parsed.GoalAchieved,
		}
	case EventConfidenceEscalation:
		payload = ConfidenceEscalationPayload{
			NodeID: parsed.NodeID,
			Reason: parsed.Reason,
		}
	case EventMutationSpawned:
		payload = MutationSpawnedPayload{
			SpawnedNodeID:   parsed.SpawnedNodeID,
			RemainingBudget: parsed.RemainingBudget,
		}
	default:
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return data
}

// chunkTypeMap maps executor/telemetry chunk types to SubagentChannel event types.
var chunkTypeMap = map[string]string{
	// Node lifecycle events (published directly as separate types by the executor)
	"node_started":   EventNodeStarted,
	"node_completed": EventNodeCompleted,
	"node_failed":    EventNodeFailed,
	"node_skipped":   EventNodeSkipped,

	// Task lifecycle events
	"task_started":   EventTaskStarted,
	"task_completed": EventTaskCompleted,
	"task_failed":    EventTaskFailed,
	"task_paused":    EventTaskPaused,

	// Special events
	"confidence_insufficient": EventConfidenceEscalation,
	"edge_thought_generated":  EventEdgeThought,
	"node_spawned":            EventMutationSpawned,
}

// ChunkToEvent maps a stream.StreamChunk to an ExecutionEvent.
// Returns nil if the chunk type is not in the event vocabulary.
// v3: Populates event.Payload with typed JSON when chunk.Content is parseable.
func ChunkToEvent(chunk stream.StreamChunk) *ExecutionEvent {
	eventType, ok := chunkTypeMap[chunk.Type]
	if !ok {
		return nil
	}

	event := &ExecutionEvent{
		TaskID:    chunk.TaskID,
		NodeID:    chunk.NodeID,
		Type:      eventType,
		Message:   chunk.Content,
		Timestamp: time.Now().Unix(),
	}

	// v3: Attempt to parse Content as JSON and build typed payload.
	// If Content isn't valid JSON, payload stays nil — graceful degradation.
	event.Payload = buildPayload(eventType, chunk.Content)

	return event
}

// BridgeOptions configures the behavior of BridgeWithOptions.
type BridgeOptions struct {
	Bus         *stream.Bus
	OnEmitError func(event ExecutionEvent, err error) // default: no-op
	StopOnError bool                                  // default: false (keep streaming)
}

// BridgeWithOptions subscribes to the given bus filtered by taskID and forwards
// matching events through the SubagentChannel. It blocks until the subscription
// channel is closed or StopOnError triggers an early exit.
func BridgeWithOptions(ch SubagentChannel, taskID string, opts BridgeOptions) {
	bus := opts.Bus
	if bus == nil {
		bus = stream.GlobalBus
	}
	onErr := opts.OnEmitError

	sub := bus.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.TaskID == taskID
	})
	defer sub.Unsubscribe()

	for chunk := range sub.Ch {
		event := ChunkToEvent(chunk)
		if event != nil {
			// v3: Dynamically update total when task_started carries nodeCount
			if event.Type == EventTaskStarted && event.Payload != nil {
				var payload TaskStartedPayload
				if json.Unmarshal(event.Payload, &payload) == nil && payload.NodeCount > 0 {
					ch.UpdateTotal(float64(payload.NodeCount))
				}
			}
			if err := ch.EmitEvent(*event); err != nil {
				if onErr != nil {
					onErr(*event, err)
				}
				if opts.StopOnError {
					return
				}
			}
		}
	}
}

// Bridge subscribes to the given bus filtered by taskID and forwards
// matching events through the SubagentChannel. It blocks until the
// subscription channel is closed (via ch.Close() or bus shutdown).
// This is a backward-compatible wrapper over BridgeWithOptions.
func Bridge(ch SubagentChannel, taskID string, bus *stream.Bus) {
	BridgeWithOptions(ch, taskID, BridgeOptions{Bus: bus})
}
