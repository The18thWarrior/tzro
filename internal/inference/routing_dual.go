package inference

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// CallRouter sends an inference request to the router sidecar (fast, small model).
// Use for: tool selection, parameter extraction, Probe navigation, classification,
// validator passes, short summarization.
//
// If the router sidecar is unavailable (not configured or stopped), falls back
// to the worker sidecar transparently.
func CallRouter(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error) {
	if isRouterAvailable() {
		return GlobalRouterModel.CallLocalModel(ctx, messages, jsonSchema)
	}
	// Fallback to worker when router is not available
	fmt.Fprintln(os.Stderr, "[Inference] Router sidecar unavailable, falling back to worker")
	return CallWorker(ctx, messages, jsonSchema)
}

// CallWorker sends an inference request to the worker sidecar (quality, large model).
// Use for: code generation, complex reasoning, DAG planning, repair/fix passes,
// edge thought generation, long-form synthesis.
func CallWorker(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error) {
	return GlobalWorkerModel.CallLocalModel(ctx, messages, jsonSchema)
}

// CallRouterStream sends a streaming inference request to the router sidecar.
func CallRouterStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error) {
	if isRouterAvailable() {
		return GlobalRouterModel.CallLocalModelStream(ctx, messages, jsonSchema, meta)
	}
	fmt.Fprintln(os.Stderr, "[Inference] Router sidecar unavailable, falling back to worker (stream)")
	return CallWorkerStream(ctx, messages, jsonSchema, meta)
}

// CallWorkerStream sends a streaming inference request to the worker sidecar.
func CallWorkerStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error) {
	return GlobalWorkerModel.CallLocalModelStream(ctx, messages, jsonSchema, meta)
}

// isRouterAvailable returns true if the router sidecar is running and healthy.
func isRouterAvailable() bool {
	status, _, _, _, _ := GlobalRouterModel.GetStatusInfo()
	s := strings.ToLower(status)
	return s == "active" || s == "adopted"
}
