package main

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed app/progress.html
var progressAppHTML string

// appResourceURI is the MCP Apps resource URI for the progress visualization.
// The ui:// scheme tells MCP hosts this is an interactive App resource that
// should be rendered in a sandboxed iframe.
const appResourceURI = "ui://tzro/progress.html"

// registerApp registers the MCP App resource for task progress visualization.
// Hosts that support MCP Apps (Claude Desktop, VS Code Copilot, Goose, etc.)
// will render this inline in the conversation when tools reference it via
// _meta.ui.resourceUri.
func registerApp(server *mcp.Server) {
	server.AddResource(&mcp.Resource{
		URI:         appResourceURI,
		Name:        "tzro-progress",
		Title:       "Task Progress",
		Description: "Interactive progress visualization for tzro task execution. Shows DAG graph, node status, progress bar, timing metrics, and cancel button.",
		MIMEType:    "text/html",
	}, handleReadProgressApp)
}

func handleReadProgressApp(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      appResourceURI,
				MIMEType: "text/html",
				Text:     progressAppHTML,
			},
		},
	}, nil
}
