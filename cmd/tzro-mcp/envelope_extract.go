package main

import (
	"encoding/json"

	"tzro/internal/memory"
)

// extractEnvelopeResult finds the Execution Envelope from a set of node states
// and returns it as a parsed map for inclusion as a top-level "result" key in
// MCP responses (ADR-0055).
// Returns nil if no node has a non-empty StructuredOutput.
func extractEnvelopeResult(nodes []memory.NodeState) map[string]interface{} {
	for _, n := range nodes {
		if n.StructuredOutput != "" {
			var result map[string]interface{}
			if json.Unmarshal([]byte(n.StructuredOutput), &result) == nil {
				return result
			}
		}
	}
	return nil
}
