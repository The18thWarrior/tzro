package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tzro/internal/codegen"
	"tzro/internal/inference"
)

// classifyCodeComplexity analyzes the provided spec against the current code context
// and returns a complexity tier string ("simple", "moderate", or "complex").
// It uses the inference.GlobalLocalModel to make the classification decision.
func classifyCodeComplexity(spec string, codeCtx *codegen.CodeContext) string {
	// Build the user prompt describing the spec and existing context.
	userPrompt := fmt.Sprintf(`
Current codebase context:
%+v

Task spec:
%s
`, codeCtx, spec)

	// Build the JSON schema constraining the output to a single tier.
	schemaJSON := `{
		"type": "object",
		"properties": {
			"tier": {
				"type": "string",
				"enum": ["simple", "moderate", "complex"]
			}
		},
		"required": ["tier"],
		"additionalProperties": false
	}`

	// Call the inference engine.
	req := inference.StructuredInferenceRequest{
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: "Classify code generation complexity. Output JSON with tier field."},
			{Role: "user", Content: userPrompt},
		},
		JSONSchema: schemaJSON,
	}
	tierJSON, err := inference.GlobalLocalModel.ExecuteStructured(
		context.Background(),
		req,
	)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[tzro_code] classifyCodeComplexity: error=%v, defaulting to simple for spec=%.60s...\n", err, spec)
		return "simple"
	}

	var result struct {
		Tier string `json:"tier"`
	}

	// Parse the response into the JSON structure.
	err = json.Unmarshal([]byte(tierJSON), &result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tzro_code] classifyCodeComplexity: parse error=%v, defaulting to simple for spec=%.60s...\n", err, spec)
		return "simple"
	}

	// Validate the tier value.
	allowedTiers := []string{"simple", "moderate", "complex"}
	if !contains(allowedTiers, result.Tier) {
		// Unknown tier: fallback to conservative "simple" classification.
		fmt.Fprintf(os.Stderr, "[tzro_code] classifyCodeComplexity: tier=%s for spec=%.60s...\n", "simple", spec)
		return "simple"
	}

	// Apply bias rule: if siblings > 2 and spec length > 200, bias toward moderate.
	biasFactor := float64(len(codeCtx.Siblings)) / 3.0
	if len(spec) > 200 && biasFactor > 0.25 {
		switch result.Tier {
		case "simple":
			result.Tier = "moderate"
		}
	}

	fmt.Fprintf(os.Stderr, "[tzro_code] classifyCodeComplexity: tier=%s for spec=%.60s...\n", result.Tier, spec)
	return result.Tier
}

// contains checks if a slice includes a value.
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}
