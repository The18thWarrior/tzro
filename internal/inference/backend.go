package inference

import (
	"context"
	"fmt"
	"os"
	"sync"

	"tzro/internal/config"
	"tzro/internal/embeddings"
	"tzro/internal/telemetry"
)

// InferenceBackend abstracts structured LLM inference calls from the hosting process.
type InferenceBackend interface {
	// CallModel performs a structured inference call with optional JSON schema constraint.
	CallModel(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error)
	// CallModelStream performs a streaming inference call.
	CallModelStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error)
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
		return NewLlamaServerBackend(GlobalWorkerModel, publisher)
	default:
		// Default if type is empty or unrecognized (e.g. during transition/bootstrap)
		return NewLlamaServerBackend(GlobalWorkerModel, publisher)
	}
}

// StopActive gracefully stops all inference backends.
// Stops both the worker and router sidecars, plus any pluggable ActiveBackend.
func StopActive() error {
	var firstErr error

	if ActiveBackend != nil {
		if err := ActiveBackend.Stop(); err != nil {
			firstErr = err
		}
	} else {
		if err := GlobalWorkerModel.Stop(); err != nil {
			firstErr = err
		}
	}

	// Stop router sidecar (independent of ActiveBackend)
	if err := GlobalRouterModel.Stop(); err != nil && firstErr == nil {
		firstErr = err
	}

	// Stop embedding sidecar
	if err := GlobalEmbeddingSidecar.Stop(); err != nil && firstErr == nil {
		firstErr = err
	}

	return firstErr
}

// StartActive starts all inference backends.
// Starts both the worker and router sidecars in parallel.
// The router only starts if routerModelPath is configured.
func StartActive(ctx context.Context) error {
	var workerErr, routerErr error
	var wg sync.WaitGroup

	// Start worker (or ActiveBackend)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if ActiveBackend != nil {
			workerErr = ActiveBackend.Start(ctx)
		} else {
			workerErr = GlobalWorkerModel.Start(ctx)
		}
	}()

	// NOTE: When ActiveBackend is a remote endpoint, we intentionally do NOT
	// start the local worker sidecar. CallWorker() already routes through
	// ActiveBackend, so a local worker would waste memory and GPU. The router
	// sidecar alone handles local classification/navigation needs.

	// Start router if configured
	routerPath := config.GetRouterModelPath()
	if routerPath != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Set the model path before starting
			GlobalRouterModel.GGUFModelPath = routerPath
			routerErr = GlobalRouterModel.Start(ctx)
			if routerErr != nil {
				fmt.Fprintf(os.Stderr, "[Inference] Router sidecar failed to start: %v (falling back to single-sidecar mode)\n", routerErr)
			}
		}()
	}

	// Start embedding sidecar (non-fatal — falls back to bag-of-words)
	var embErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		embErr = GlobalEmbeddingSidecar.Start(ctx)
		if embErr != nil {
			fmt.Fprintf(os.Stderr, "[Inference] Embedding sidecar failed to start: %v (bag-of-words fallback)\n", embErr)
		} else {
			embeddings.SetDefaultEngine(GlobalEmbeddingSidecar)
			fmt.Fprintf(os.Stderr, "[Inference] Neural embedding engine active (All-MiniLM-L6-v2)\n")
		}
	}()

	wg.Wait()

	// Worker failure is fatal; router/embedding failures are non-fatal
	if workerErr != nil {
		return workerErr
	}
	return nil
}

