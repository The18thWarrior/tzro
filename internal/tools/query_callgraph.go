package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tzro/internal/symbols"
)

// QueryCallgraphTool allows the agent to query the call graph index for a directory.
type QueryCallgraphTool struct{}

// NewQueryCallgraphTool creates a new query_callgraph tool.
func NewQueryCallgraphTool() *QueryCallgraphTool {
	return &QueryCallgraphTool{}
}

func (q *QueryCallgraphTool) Name() string {
	return "query_callgraph"
}

func (q *QueryCallgraphTool) GetSchema() (string, error) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_arguments": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"directory": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to the directory to analyze",
					},
					"goal": map[string]interface{}{
						"type":        "string",
						"description": "Natural language goal to select relevant entry points",
					},
					"hops": map[string]interface{}{
						"type":        "integer",
						"description": "Number of call graph hops from entry points (default: 2)",
					},
					"include_bodies": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, include function bodies in output. If false, only signatures and edges.",
					},
					"max_functions": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of functions to include (default: 30)",
					},
				},
				"required": []string{"directory"},
			},
		},
	}
	b, err := json.Marshal(schema)
	return string(b), err
}

func (q *QueryCallgraphTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	toolArgs, _ := args["tool_arguments"].(map[string]interface{})
	if toolArgs == nil {
		toolArgs = args
	}

	dir, _ := toolArgs["directory"].(string)
	if dir == "" {
		return marshalResult(ToolError("directory parameter is required"))
	}

	hops := 2
	if h, ok := toolArgs["hops"].(float64); ok {
		hops = int(h)
	}

	includeBodies := false
	if b, ok := toolArgs["include_bodies"].(bool); ok {
		includeBodies = b
	}

	maxFunctions := 30
	if m, ok := toolArgs["max_functions"].(float64); ok {
		maxFunctions = int(m)
	}

	// Build call graph
	graphSymbols, graphEdges, err := symbols.BuildCallGraph(dir)
	if err != nil {
		return marshalResult(ToolError(fmt.Sprintf("building call graph: %v", err)))
	}

	if len(graphSymbols) == 0 {
		return marshalResult(ToolError("no symbols found in directory"))
	}

	// Use all exported functions as entry points (no LLM selection for direct tool use)
	var entryNames []string
	for _, s := range graphSymbols {
		if s.Exported {
			entryNames = append(entryNames, s.Name)
		}
	}
	if len(entryNames) == 0 {
		// If no exported functions, use all
		for _, s := range graphSymbols {
			entryNames = append(entryNames, s.Name)
		}
	}

	// Traverse and assemble
	traversed := symbols.TraverseSubgraph(graphSymbols, graphEdges, entryNames, hops, 24000, maxFunctions)
	context, err := symbols.AssembleContext(traversed, graphEdges, dir, includeBodies)
	if err != nil {
		return marshalResult(ToolError(fmt.Sprintf("assembling context: %v", err)))
	}

	result := &ToolResult{
		Success: true,
		Data:    context,
		Meta: &ToolResultMeta{
			Tool:      "query_callgraph",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
	return marshalResult(result)
}

func marshalResult(r *ToolResult) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
