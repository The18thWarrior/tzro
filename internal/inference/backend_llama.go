package inference

import (
	"context"
	"strings"
	"tzro/internal/telemetry"
)

// LlamaServerBackend wraps the existing LocalModelManager to implement InferenceBackend.
type LlamaServerBackend struct {
	manager   *LocalModelManager
	publisher telemetry.EventPublisher
}

// NewLlamaServerBackend creates a new LlamaServerBackend wrapping a LocalModelManager.
func NewLlamaServerBackend(manager *LocalModelManager, publisher telemetry.EventPublisher) *LlamaServerBackend {
	return &LlamaServerBackend{
		manager:   manager,
		publisher: publisher,
	}
}

// CallModel performs a structured inference call using the local manager.
func (b *LlamaServerBackend) CallModel(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (*InferenceResult, error) {
	return b.manager.CallLocalModel(ctx, systemPrompt, userPrompt, jsonSchema)
}

// CallModelStream performs a streaming inference call using the local manager.
func (b *LlamaServerBackend) CallModelStream(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, meta StreamMeta) (*InferenceResult, error) {
	return b.manager.CallLocalModelStream(ctx, systemPrompt, userPrompt, jsonSchema, meta)
}

// Status returns the backend's current readiness.
func (b *LlamaServerBackend) Status() string {
	status, _, _, _, _ := b.manager.GetStatusInfo()
	switch strings.ToLower(status) {
	case "active", "adopted":
		return "active"
	case "starting":
		return "starting" // We can return "starting" or "stopped". Let's return "stopped" or "active". We can stick to the spec's "active" | "stopped" | "unavailable"
	default:
		return "stopped"
	}
}

// Start initializes the backend.
func (b *LlamaServerBackend) Start(ctx context.Context) error {
	return b.manager.Start(ctx)
}

// Stop tears down the backend.
func (b *LlamaServerBackend) Stop() error {
	return b.manager.Stop()
}
