package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tzro/internal/inference"
)

// ClassifyCodeComplexity analyzes the provided spec against the current code context
// and returns a binary complexity tier: "simple" or "complex".
//
// "simple" tasks can be generated directly by the local model.
// "complex" tasks require the draft+fix pipeline (local draft, then cloud repair).
//
// Uses the router sidecar (CallRouter) for fast GBNF-constrained classification.
// Falls back to "simple" on any error (conservative: allows direct generation).
func ClassifyCodeComplexity(spec string, codeCtx *CodeContext) string {
	// Build the user prompt describing the spec and existing context.
	userPrompt := fmt.Sprintf(`
Current codebase context:
%+v

Task spec:
%s
`, codeCtx, spec)

	// Build the JSON schema constraining the output to a binary tier.
	schemaJSON := `{
		"type": "object",
		"properties": {
			"tier": {
				"type": "string",
				"enum": ["simple", "complex"]
			}
		},
		"required": ["tier"],
		"additionalProperties": false
	}`

	// Call the router sidecar — classification is a fast, GBNF-constrained task.
	msgs := []inference.InferenceMessage{
		{Role: "system", Content: `Classify code generation complexity as "simple" or "complex".

"simple": Single-concept tasks that can be directly generated from a spec.
Examples: add a method, create a handler, add error handling to one function.

"complex": Multi-concept tasks requiring coordinated design decisions.
Examples: implement a generic data structure with concurrency, refactor across interfaces,
build a query builder with type-safe chaining, create event systems with wildcard matching.

Output JSON with tier field.`},
		{Role: "user", Content: userPrompt},
	}

	inferResult, err := inference.CallRouter(context.Background(), msgs, schemaJSON)
	tierJSON := ""
	if err == nil {
		tierJSON = inferResult.Content
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "[codegen] ClassifyCodeComplexity: error=%v, defaulting to simple for spec=%.60s...\n", err, spec)
		return "simple"
	}

	var result struct {
		Tier string `json:"tier"`
	}

	// Parse the response into the JSON structure.
	err = json.Unmarshal([]byte(tierJSON), &result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[codegen] ClassifyCodeComplexity: parse error=%v, defaulting to simple for spec=%.60s...\n", err, spec)
		return "simple"
	}

	// Validate the tier value — only "simple" and "complex" are accepted.
	if result.Tier != "simple" && result.Tier != "complex" {
		fmt.Fprintf(os.Stderr, "[codegen] ClassifyCodeComplexity: unexpected tier=%q, defaulting to simple for spec=%.60s...\n", result.Tier, spec)
		return "simple"
	}

	fmt.Fprintf(os.Stderr, "[codegen] ClassifyCodeComplexity: tier=%s for spec=%.60s...\n", result.Tier, spec)
	return result.Tier
}
