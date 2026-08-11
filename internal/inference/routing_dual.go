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
//
// When ActiveBackend is set (e.g., remote OpenAI-compatible endpoint), routes
// through the backend instead of the local sidecar.
func CallWorker(ctx context.Context, messages []InferenceMessage, jsonSchema string) (*InferenceResult, error) {
	if ActiveBackend != nil {
		return ActiveBackend.CallModel(ctx, messages, jsonSchema)
	}
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
//
// When ActiveBackend is set, routes through the backend instead of the local sidecar.
func CallWorkerStream(ctx context.Context, messages []InferenceMessage, jsonSchema string, meta StreamMeta) (*InferenceResult, error) {
	if ActiveBackend != nil {
		return ActiveBackend.CallModelStream(ctx, messages, jsonSchema, meta)
	}
	return GlobalWorkerModel.CallLocalModelStream(ctx, messages, jsonSchema, meta)
}

// ExecuteRouterStructured routes a StructuredInferenceRequest through the router
// sidecar (fast, small model). Use for: classification, confidence checks, edge
// thought generation, parameter extraction, branch evaluation, binding resolution.
//
// Falls back to the worker sidecar transparently if the router is unavailable.
//
// IMPORTANT: This dispatches via CallRouter/CallRouterStream (which target
// GlobalRouterModel directly) rather than GlobalRouterModel.ExecuteStructured(),
// because ExecuteStructured checks the package-level ActiveBackend and would
// route all calls through the remote inference backend instead of the local
// router sidecar.
func ExecuteRouterStructured(ctx context.Context, req StructuredInferenceRequest) (string, error) {
	var res *InferenceResult
	var err error

	if req.StreamMeta != nil {
		res, err = CallRouterStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta)
	} else {
		res, err = CallRouter(ctx, req.Messages, req.JSONSchema)
	}

	if err != nil {
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("router returned nil result")
	}
	return res.Content, nil
}

// ExecuteWorkerStructured routes a StructuredInferenceRequest through the worker
// sidecar (quality, large model). Use for: code generation, complex reasoning,
// DAG planning, synthesis, primary tool parameter extraction.
//
// When ActiveBackend is set, the ExecuteStructured routing logic on GlobalWorkerModel
// already dispatches through ActiveBackend (see routing.go lines ~290-302).
func ExecuteWorkerStructured(ctx context.Context, req StructuredInferenceRequest) (string, error) {
	return GlobalWorkerModel.ExecuteStructured(ctx, req)
}

// isRouterAvailable returns true if the router sidecar is running and healthy.
func isRouterAvailable() bool {
	status, _, _, _, _ := GlobalRouterModel.GetStatusInfo()
	s := strings.ToLower(status)
	return s == "active" || s == "adopted"
}
