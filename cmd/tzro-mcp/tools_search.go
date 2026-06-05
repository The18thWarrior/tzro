package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/tools"
)

// TzroWebSearchArgs defines inputs for tzro_web_search.
type TzroWebSearchArgs struct {
	Query      string `json:"query" jsonschema:"required,The search query to execute"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"Max number of results to return. Default 5"`
}

func handleTzroWebSearch(ctx context.Context, req *mcp.CallToolRequest, args TzroWebSearchArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Query) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "query cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	limit := args.MaxResults
	if limit <= 0 {
		limit = 5
	}

	results, source := tools.WebSearchMetasearch(ctx, args.Query, limit)

	// Convert SearchResult structs to serializable maps
	resultMaps := make([]map[string]string, 0, len(results))
	for _, r := range results {
		resultMaps = append(resultMaps, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Snippet,
		})
	}

	respMap := map[string]interface{}{
		"query":   args.Query,
		"source":  source,
		"results": resultMaps,
	}

	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}
