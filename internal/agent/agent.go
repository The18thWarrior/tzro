// Package agent defines the hosting abstraction for autonomous processes within the tzro daemon.
//
// The Agent interface is the minimal contract: Name, Start, Stop. Concrete agent types
// specialize the trigger mechanism and capabilities.
//
// BackgroundAgent provides shared infrastructure for daemon-resident agents that run
// on their own trigger schedule: LLM client wiring, telemetry subscription, memory access,
// and durable notification output.
//
// See ADR-0022 for the architectural decision.
package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"tzro/internal/notification"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// Agent is the minimal contract for any autonomous process hosted by tzro.
type Agent interface {
	Name() string
	Start(ctx context.Context)
	Stop()
}

// LLMClient decouples structured inference execution from the agent system.
type LLMClient interface {
	CallModel(ctx context.Context, systemPrompt, userPrompt string, jsonSchema string) (string, error)
}

// BackgroundAgent provides shared infrastructure for daemon-resident agents
// that run continuously on their own trigger schedule. The Observer and Sentinel
// embed this base and supply their own trigger loop and evaluation logic.
type BackgroundAgent struct {
	mu           sync.RWMutex
	agentName    string
	llm          LLMClient
	telemetryMgr *telemetry.TelemetryManager
	cancelFunc   context.CancelFunc
	subscription *telemetry.TelemetrySubscription
}

// NewBackgroundAgent creates a new BackgroundAgent with the given name.
func NewBackgroundAgent(name string) BackgroundAgent {
	return BackgroundAgent{
		agentName:    name,
		telemetryMgr: telemetry.Default,
	}
}

// AgentName returns the agent's canonical name.
func (b *BackgroundAgent) AgentName() string {
	return b.agentName
}

// SetLLMClient registers an LLM client with the agent.
func (b *BackgroundAgent) SetLLMClient(client LLMClient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.llm = client
}

// GetLLMClient returns the registered LLM client.
func (b *BackgroundAgent) GetLLMClient() LLMClient {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.llm
}

// SetTelemetryManager overrides the telemetry manager instance used by the agent.
func (b *BackgroundAgent) SetTelemetryManager(tm *telemetry.TelemetryManager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.telemetryMgr = tm
}

// GetTelemetryManager returns the telemetry manager.
func (b *BackgroundAgent) GetTelemetryManager() *telemetry.TelemetryManager {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.telemetryMgr
}

// Subscribe creates a telemetry subscription with the given filter.
func (b *BackgroundAgent) Subscribe(filter func(stream.StreamChunk) bool) *telemetry.TelemetrySubscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscription = b.telemetryMgr.Subscribe(filter)
	return b.subscription
}

// Unsubscribe removes the active telemetry subscription.
func (b *BackgroundAgent) Unsubscribe() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscription != nil {
		b.subscription.Unsubscribe()
		b.subscription = nil
	}
}

// SetCancel stores the context cancel function for lifecycle management.
func (b *BackgroundAgent) SetCancel(cancel context.CancelFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelFunc = cancel
}

// Cancel invokes the stored cancel function and clears it.
func (b *BackgroundAgent) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelFunc != nil {
		b.cancelFunc()
		b.cancelFunc = nil
	}
}

// IsRunning returns true if the agent has an active cancel function.
func (b *BackgroundAgent) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cancelFunc != nil
}

// ProduceAlert writes a durable notification and publishes to the StreamBus.
func (b *BackgroundAgent) ProduceAlert(ctx context.Context, priority, title, message, targetID string) error {
	source := b.agentName

	opts := []notification.Option{}
	if targetID != "" {
		opts = append(opts, notification.WithTargetID(targetID))
	}

	_, err := notification.Send(ctx, source, priority, title, message, opts...)
	if err != nil {
		return fmt.Errorf("[%s] failed to produce alert: %w", source, err)
	}

	// Publish to StreamBus for real-time event bridge
	b.mu.RLock()
	tm := b.telemetryMgr
	b.mu.RUnlock()
	if tm != nil {
		tm.PublishStream(stream.StreamChunk{
			Source:  source,
			Type:    source + "_alert",
			Content: fmt.Sprintf("%s: %s — %s", priority, title, message),
		})
	}

	return nil
}

// HasActiveAlert checks if there is already an active (unread or read) alert
// with the given targetID from this agent's source.
func (b *BackgroundAgent) HasActiveAlert(targetID string) bool {
	notifs, err := notification.List(context.Background(), "")
	if err != nil {
		return false
	}
	source := b.agentName
	for _, n := range notifs {
		if n.Source == source && n.TargetID == targetID && (n.Status == "unread" || n.Status == "read") {
			return true
		}
	}
	return false
}

// Fingerprint produces a SHA-256 hex fingerprint from the given inputs.
// Used for alert deduplication via TargetID.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte(":"))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
