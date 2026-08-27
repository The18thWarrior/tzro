package executor

import (
	"context"
	"encoding/json"

	"tzro/internal/inference"
)

// ModelTarget controls which inference sidecar a step routes to.
type ModelTarget int

const (
	TargetAuto   ModelTarget = iota // schema → router, no schema → worker
	TargetWorker                    // explicitly use 4B worker
	TargetRouter                    // explicitly use 1B router
)

// ProbeInferenceEngine abstracts the inference call for testability.
// In production, this wraps InferenceBackend.CallModel. In tests,
// a mock returns canned GBNF-constrained JSON responses.
type ProbeInferenceEngine interface {
	Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error)
	// InferMessages sends a pre-segmented message array to the model.
	// This enables KV cache prefix sharing: the system prompt + tool schemas
	// (segment 1) stay identical across steps, so the llama-server's
	// --cache-reuse window can skip re-processing those tokens on every step.
	// Returns (content, error).
	InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error)
}

// ProbeInference unifies worker/router routing into a single implementation.
// TargetAuto: schema → router (1B), no schema → worker (4B).
// TargetWorker: always worker (4B). Used for synthesis, compaction, recall.
// TargetRouter: always router (1B). Used for fast structured extraction.
type ProbeInference struct{}

func (p *ProbeInference) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	messages := []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}
	return p.InferMessages(ctx, messages, jsonSchema, target)
}

func (p *ProbeInference) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	var result *inference.InferenceResult
	var err error
	switch target {
	case TargetWorker:
		result, err = inference.CallWorker(ctx, messages, jsonSchema)
	case TargetRouter:
		result, err = inference.CallRouter(ctx, messages, jsonSchema)
	default: // TargetAuto
		if jsonSchema == "" {
			result, err = inference.CallWorker(ctx, messages, jsonSchema)
		} else {
			result, err = inference.CallRouter(ctx, messages, jsonSchema)
		}
	}
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// ThoughtChainStep is the GBNF-constrained JSON output schema for each
// Thought Chain inference step. The Local Model must produce output
// conforming to this schema on every call.
type ThoughtChainStep struct {
	Action      string                 `json:"action"`              // "tool_call" | "synthesize"
	Tool        string                 `json:"tool,omitempty"`      // tool name (when action == "tool_call")
	Arguments   map[string]interface{} `json:"arguments,omitempty"` // tool arguments JSON map
	NextThought string                 `json:"nextThought"`         // reasoning for the next step
	Confidence  float64                `json:"confidence"`          // 0.0 - 1.0 convergence signal
	Synthesis   string                 `json:"synthesis,omitempty"` // final output (when action == "synthesize")
}

// UnmarshalJSON implements custom unmarshaling to handle arguments as either a JSON string or a JSON object.
func (t *ThoughtChainStep) UnmarshalJSON(data []byte) error {
	type Alias ThoughtChainStep
	aux := struct {
		Arguments interface{} `json:"arguments"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Arguments != nil {
		switch v := aux.Arguments.(type) {
		case map[string]interface{}:
			t.Arguments = v
		case string:
			if v != "" {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(v), &parsed); err != nil {
					t.Arguments = map[string]interface{}{"query": v}
				} else {
					t.Arguments = parsed
				}
			}
		}
	}
	return nil
}

// ThoughtChainStepSchema is the JSON schema that constrains Local Model
// output for each Thought Chain step via GBNF grammar.
const ThoughtChainStepSchema = `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["tool_call", "synthesize"]
		},
		"tool": { "type": "string" },
		"arguments": { "type": "string" },
		"nextThought": { "type": "string" },
		"confidence": { "type": "number" },
		"synthesis": { "type": "string" }
	},
	"required": ["action", "nextThought", "confidence"]
}`

// SynthesisValidationSchema is the GBNF constraint for the Pass 3
// Synthesis Validation Gate. The Worker model evaluates whether the
// Router's synthesis signal is premature and can request more steps.
const SynthesisValidationSchema = `{"type":"object","properties":{"ready":{"type":"boolean"},"reason":{"type":"string"},"additionalSteps":{"type":"integer"}},"required":["ready"]}`

// computeUnusedTools returns tool names from allowedTools that are NOT
// present in usedToolSet. Used by the Synthesis Validation Gate to
// inform the Worker about unexplored capabilities.
func computeUnusedTools(allowedTools []string, usedToolSet map[string]bool) []string {
	var unused []string
	for _, t := range allowedTools {
		if !usedToolSet[t] {
			unused = append(unused, t)
		}
	}
	return unused
}
