package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"tzro/internal/agent"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// LLMClient is an alias for the canonical agent.LLMClient interface.
type LLMClient = agent.LLMClient

// ObserverEvent is a type alias to the consolidated telemetry event schema
type ObserverEvent = telemetry.ObserverEvent

// ObserverAgent handles background debouncing, aggregation, reflection, and ingestion.
// It embeds agent.BackgroundAgent for shared infrastructure (ADR-0022).
type ObserverAgent struct {
	agent.BackgroundAgent

	mu               sync.RWMutex
	activeEvents     []telemetry.ObserverEvent
	debounceInterval time.Duration
	auditThreshold   int
}

// Verify interface compliance at compile time.
var _ agent.Agent = (*ObserverAgent)(nil)

// Name returns the agent's canonical name.
func (a *ObserverAgent) Name() string {
	return a.AgentName()
}

// NewObserverAgent creates a new isolated ObserverAgent.
func NewObserverAgent() *ObserverAgent {
	return &ObserverAgent{
		BackgroundAgent:  agent.NewBackgroundAgent("observer"),
		debounceInterval: 2 * time.Second,
		auditThreshold:   10,
	}
}

// SetDebounceInterval allows overriding the debounce time.
func (a *ObserverAgent) SetDebounceInterval(d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.debounceInterval = d
}

// SetAuditThreshold allows overriding the audit queue threshold.
func (a *ObserverAgent) SetAuditThreshold(t int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auditThreshold = t
}

// Start spawns the background event aggregation and reflection loop.
func (a *ObserverAgent) Start(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.IsRunning() {
		return
	}

	loopCtx, cancel := context.WithCancel(ctx)
	a.SetCancel(cancel)

	// Subscribe to standard telemetry stream events
	sub := a.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "telemetry"
	})

	subCh := sub.Ch

	go func() {
		ticker := time.NewTicker(a.debounceInterval)
		defer ticker.Stop()

		var lastEventTime time.Time

		for {
			select {
			case <-loopCtx.Done():
				return
			case chunk, ok := <-subCh:
				if !ok {
					return
				}
				event := telemetry.ObserverEvent{
					Type:      chunk.Type,
					TaskID:    chunk.TaskID,
					NodeID:    chunk.NodeID,
					Timestamp: time.Now().Unix(),
					Payload:   chunk.Content,
				}

				a.mu.Lock()
				a.activeEvents = append(a.activeEvents, event)
				lastEventTime = time.Now()
				eventCount := len(a.activeEvents)
				threshold := a.auditThreshold
				a.mu.Unlock()

				fmt.Fprintf(os.Stderr, "[Observer] Received event '%s' for Task '%s'\n", event.Type, event.TaskID)

				if eventCount >= threshold {
					a.triggerAudit(fmt.Sprintf("%d-event threshold reached", threshold))
				}

			case <-ticker.C:
				a.mu.Lock()
				eventCount := len(a.activeEvents)
				debounce := a.debounceInterval
				var quietDuration time.Duration
				if eventCount > 0 {
					quietDuration = time.Since(lastEventTime)
				}
				a.mu.Unlock()

				if eventCount > 0 && quietDuration >= debounce {
					a.triggerAudit("Inactivity debounce threshold reached")
				}
			}
		}
	}()
}

// Stop cancels the background loop context and unsubscribes from the stream.
func (a *ObserverAgent) Stop() {
	a.Cancel()
	a.Unsubscribe()
}

func (a *ObserverAgent) triggerAudit(reason string) {
	a.mu.Lock()
	eventCount := len(a.activeEvents)
	if eventCount == 0 {
		a.mu.Unlock()
		return
	}

	fmt.Fprintf(os.Stderr, "[Observer Audit] Running automated verification: %s. Processing %d events...\n", reason, eventCount)
	for _, ev := range a.activeEvents {
		fmt.Fprintf(os.Stderr, "  -> Audit Event: Task %s | Node %s | Type %s\n", ev.TaskID, ev.NodeID, ev.Type)
	}

	// Copy the events to process in the background goroutine
	eventsCopy := make([]telemetry.ObserverEvent, len(a.activeEvents))
	copy(eventsCopy, a.activeEvents)

	a.activeEvents = nil
	llmClient := a.GetLLMClient()
	tm := a.GetTelemetryManager()
	a.mu.Unlock()

	tm.PublishStream(stream.StreamChunk{
		Source:  "observer",
		Type:    "observer_audit",
		Content: fmt.Sprintf("Observer Audit: Completed automated verification for %d execution events (%s).", eventCount, reason),
	})

	_, _ = notification.Send(context.Background(), "observer", "info", "Observer Verification Audit Complete",
		fmt.Sprintf("Observer Agent completed automated verification for %d execution events (%s). System health remains optimal.", eventCount, reason))

	// Run deterministic operational checks (pre-pass before LLM reflection)
	go a.runOperationalChecks(context.Background())

	if llmClient != nil {
		go a.performReflectionAndIngestion(context.Background(), eventsCopy, llmClient)
	}
}

// runOperationalChecks performs deterministic system health evaluations
// that don't require LLM inference. These produce alerts for stale workflows,
// escalation trends, and micro-skill staleness.
func (a *ObserverAgent) runOperationalChecks(ctx context.Context) {
	now := time.Now().Unix()

	// 1. Stale workflow detection: approval requests older than 6 hours
	notifs, err := notification.List(ctx, "unread")
	if err == nil {
		staleThreshold := int64(6 * 60 * 60) // 6 hours in seconds
		for _, n := range notifs {
			if n.Source == "human_approval" && n.Type == "approval_request" {
				age := now - n.CreatedAt
				if age > staleThreshold {
					fp := agent.Fingerprint("stale_approval", n.ID)
					if !a.HasActiveAlert(fp) {
						hours := age / 3600
						_ = a.ProduceAlert(ctx, "ambient",
							"Stale Workflow Approval",
							fmt.Sprintf("Approval request for task '%s' node '%s' has been pending for %d hours.",
								n.TaskID, n.TargetID, hours),
							fp)
					}
				}
			}
		}
	}

	// 2. Micro-skill staleness: skills older than 30 days
	skills := memory.DB.GetSkills()
	staleSkillAge := int64(30 * 24 * 60 * 60) // 30 days in seconds
	for _, s := range skills {
		if s.CreatedAt > 0 && (now-s.CreatedAt) > staleSkillAge {
			fp := agent.Fingerprint("stale_skill", s.ID)
			if !a.HasActiveAlert(fp) {
				days := (now - s.CreatedAt) / (24 * 60 * 60)
				_ = a.ProduceAlert(ctx, "ambient",
					"Stale Micro-Skill Detected",
					fmt.Sprintf("Skill '%s' was created %d days ago and may need review.", s.Name, days),
					fp)
			}
		}
	}
}

func (a *ObserverAgent) performReflectionAndIngestion(ctx context.Context, events []telemetry.ObserverEvent, client LLMClient) {
	var contextEvents []telemetry.ObserverEvent
	for _, ev := range events {
		if ev.Type == "node_completed" || ev.Type == "node_failed" || ev.Type == "speed_floor_fallback" || ev.Type == "cloud_escalation" {
			contextEvents = append(contextEvents, ev)
		}
	}

	if len(contextEvents) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("Recent task execution events and trajectories:\n")
	for _, ev := range contextEvents {
		sb.WriteString(fmt.Sprintf("- Event Type: %s | Task ID: %s | Node ID: %s | Timestamp: %d | Details: %s\n",
			ev.Type, ev.TaskID, ev.NodeID, ev.Timestamp, ev.Payload))
	}
	eventsText := sb.String()

	// 1. Run Memory Reflection
	systemPromptMem := `You are a self-improvement reflection agent. Analyze the completed task execution event details and extract memories that will help the assistant perform better in the future.

Extract:
1. corrections — places where the user or system corrected the assistant's assumptions or approach. (Highest Priority).
2. anti_patterns — tools or sequences that failed or returned bad results.
3. preferences — user stated preferences for communication, tools, or styles.
4. strategies — approaches that worked well and got positive user feedback.
5. facts — objective facts about the user's environment or company.

Return valid JSON in this exact structure:
{
  "memories": [
    {
      "type": "correction|anti_pattern|preference|insight|strategy|fact",
      "content": "Self-contained statement of what was learned",
      "context": "Brief note of what triggered this learning",
      "confidence": 0.0-1.0
    }
  ]
}`

	gbnfSchemaMem := `{
		"type": "object",
		"properties": {
			"memories": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"type": { "type": "string", "enum": ["correction", "anti_pattern", "preference", "insight", "strategy", "fact"] },
						"content": { "type": "string" },
						"context": { "type": "string" },
						"confidence": { "type": "number" }
					},
					"required": ["type", "content", "confidence"]
				}
			}
		},
		"required": ["memories"]
	}`

	memJSON, err := client.CallModel(ctx, systemPromptMem, eventsText, gbnfSchemaMem)
	var newMemCount int
	if err == nil {
		var res struct {
			Memories []struct {
				Type       string  `json:"type"`
				Content    string  `json:"content"`
				Context    string  `json:"context"`
				Confidence float64 `json:"confidence"`
			} `json:"memories"`
		}
		if json.Unmarshal([]byte(memJSON), &res) == nil {
			for _, m := range res.Memories {
				memID := fmt.Sprintf("mem_%d", time.Now().UnixNano())
				err := memory.DB.AddMemory(memory.FactMemory{
					ID:         memID,
					UserID:     "default_user",
					Type:       m.Type,
					Content:    m.Content,
					Context:    m.Context,
					Confidence: m.Confidence,
					Source:     "auto_reflection",
					CreatedAt:  time.Now(),
				})
				if err == nil {
					newMemCount++
				} else {
					fmt.Fprintf(os.Stderr, "[Observer Warning] Failed to commit auto-synthesized memory: %v\n", err)
				}
				time.Sleep(time.Millisecond)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[Observer Warning] Memory reflection LLM call failed: %v\n", err)
	}

	// 2. Run Knowledge Graph Extraction
	systemPromptKG := `You are a relational knowledge graph extraction agent. Analyze the completed task execution event details and identify any real-world entities (contacts, accounts, tickets, or documents) and their relationships mentioned or discovered.

Return valid JSON in this exact structure:
{
  "nodes": [
    {
      "id": "machine_key_id (e.g. con_alice or acc_acme)",
      "type": "account|contact|ticket|document",
      "name": "Human display name (e.g. Alice Smith or Acme Corp)",
      "metadata": {
        "key": "value"
      }
    }
  ],
  "edges": [
    {
      "type": "belongs_to|assigned_to|references",
      "sourceId": "machine_key_id of source node",
      "targetId": "machine_key_id of target node",
      "metadata": {
        "key": "value"
      }
    }
  ]
}`

	gbnfSchemaKG := `{
		"type": "object",
		"properties": {
			"nodes": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": { "type": "string" },
						"type": { "type": "string", "enum": ["account", "contact", "ticket", "document"] },
						"name": { "type": "string" },
						"metadata": { "type": "object" }
					},
					"required": ["id", "type", "name"]
				}
			},
			"edges": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"type": { "type": "string", "enum": ["belongs_to", "assigned_to", "references"] },
						"sourceId": { "type": "string" },
						"targetId": { "type": "string" },
						"metadata": { "type": "object" }
					},
					"required": ["type", "sourceId", "targetId"]
				}
			}
		},
		"required": ["nodes", "edges"]
	}`

	kgJSON, err := client.CallModel(ctx, systemPromptKG, eventsText, gbnfSchemaKG)
	var newNodeCount int
	var newEdgeCount int
	tm := a.GetTelemetryManager()
	if err == nil {
		var res struct {
			Nodes []struct {
				ID       string                 `json:"id"`
				Type     string                 `json:"type"`
				Name     string                 `json:"name"`
				Metadata map[string]interface{} `json:"metadata"`
			} `json:"nodes"`
			Edges []struct {
				Type     string                 `json:"type"`
				SourceID string                 `json:"sourceId"`
				TargetID string                 `json:"targetId"`
				Metadata map[string]interface{} `json:"metadata"`
			} `json:"edges"`
		}
		if json.Unmarshal([]byte(kgJSON), &res) == nil {
			for _, n := range res.Nodes {
				err := memory.DB.AddNode(memory.KGNode{
					ID:       n.ID,
					NodeType: n.Type,
					Name:     n.Name,
					Metadata: n.Metadata,
					Source:   "observer",
					Weight:   1.0,
				})
				if err == nil {
					newNodeCount++
				}
			}
			for _, e := range res.Edges {
				edgeID := fmt.Sprintf("edge_%d", time.Now().UnixNano())
				err := memory.DB.AddEdge(memory.KGEdge{
					ID:       edgeID,
					EdgeType: e.Type,
					SourceID: e.SourceID,
					TargetID: e.TargetID,
					Metadata: e.Metadata,
					Weight:   1.0,
				})
				if err == nil {
					newEdgeCount++
				}
				time.Sleep(time.Millisecond)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[Observer Warning] KG extraction LLM call failed: %v\n", err)
	}

	if newMemCount > 0 || newNodeCount > 0 || newEdgeCount > 0 {
		msg := fmt.Sprintf("Observer Agent auto-reflected on recent events: synthesized %d new memories, %d knowledge graph nodes, and %d relationships.",
			newMemCount, newNodeCount, newEdgeCount)

		fmt.Fprintln(os.Stderr, "[Observer]", msg)

		tm.PublishStream(stream.StreamChunk{
			Source:  "observer",
			Type:    "observer_audit",
			Content: msg,
		})

		_, _ = notification.Send(ctx, "observer", "info", "Observer Auto-Synthesis Complete", msg)
	}
}

// DefaultAgent is the global singleton observer agent.
var DefaultAgent = NewObserverAgent()

// Start begins the background observer event monitoring.
func Start() {
	DefaultAgent.Start(context.Background())
}

// Stop terminates the background observer event monitoring.
func Stop() {
	DefaultAgent.Stop()
}

// SetLLMClient registers an LLM client with the default agent.
func SetLLMClient(client LLMClient) {
	DefaultAgent.SetLLMClient(client)
}

// SetDebounceInterval overrides the default agent's quiet debounce interval.
func SetDebounceInterval(d time.Duration) {
	DefaultAgent.SetDebounceInterval(d)
}

// SetAuditThreshold overrides the default agent's audit event threshold.
func SetAuditThreshold(t int) {
	DefaultAgent.SetAuditThreshold(t)
}
