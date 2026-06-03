package inference

import (
	"context"
	"tzro/internal/config"
	"tzro/internal/telemetry"
)

// InferenceBackend abstracts structured LLM inference calls from the hosting process.
type InferenceBackend interface {
	// CallModel performs a structured inference call with optional JSON schema constraint.
	CallModel(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (*InferenceResult, error)
	// CallModelStream performs a streaming inference call.
	CallModelStream(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, meta StreamMeta) (*InferenceResult, error)
	// Status returns the backend's current readiness.
	Status() string // "active" | "stopped" | "unavailable"
	// Start initializes the backend (no-op for remote backends).
	Start(ctx context.Context) error
	// Stop tears down the backend (no-op for remote backends).
	Stop() error
}

// ActiveBackend is the globally set active inference backend.
var ActiveBackend InferenceBackend

// NewBackend creates an InferenceBackend from config.
func NewBackend(cfg config.BackendConfig, publisher telemetry.EventPublisher) InferenceBackend {
	switch cfg.Type {
	case "openai-compatible":
		return NewRemoteOpenAIBackend(cfg, publisher)
	case "llama-server":
		return NewLlamaServerBackend(GlobalLocalModel, publisher)
	default:
		// Default if type is empty or unrecognized (e.g. during transition/bootstrap)
		return NewLlamaServerBackend(GlobalLocalModel, publisher)
	}
}
