package executor

import (
	_ "embed"
	"encoding/json"
)

// GraphToolDescription and GraphToolParameters are shared by installed clients.
const GraphToolDescription = "Run a multi-step workflow locally in one call. Execute tools, use configured local models for decisions and extraction, and return results or yield when cloud reasoning is needed. Batch known dependent steps instead of separate cloud turns. Tool-only graphs need no models. Use depends_on and {\"$ref\":\"/nodes/id/output/field\"} to wire results; select returns to avoid unnecessary context. For decisions, set accept.min_confidence so weak answers yield. Full intermediate evidence is retained for tzro expand."

//go:embed tool_schema.json
var GraphToolSchemaJSON string

func GraphToolParameters() map[string]any {
	var schema map[string]any
	if err := json.Unmarshal([]byte(GraphToolSchemaJSON), &schema); err != nil {
		panic(err) // Embedded build-time contract, not external input.
	}
	return schema
}
