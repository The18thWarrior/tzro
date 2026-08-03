package main

import (
	"context"
	_ "embed"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/config"
)

//go:embed app/progress.html
var progressAppHTML string

// appResourceURI is the MCP Apps resource URI for the progress visualization.
// The ui:// scheme tells MCP hosts this is an interactive App resource that
// should be rendered in a sandboxed iframe.
const appResourceURI = "ui://tzro/progress.html"

// lastTaskState tracks the most recent task launched via tzro_run so the
// progress app can be pre-populated when the host reads the resource.
// The sandboxed iframe cannot make localhost fetch calls, so we inject the
// taskId directly into the HTML at resource-read time.
var (
	lastTaskMu    sync.RWMutex
	lastTaskID    string
	lastDaemonPort string
)

// setLastTask records the most recently launched task for the progress app.
func setLastTask(taskID, daemonPort string) {
	lastTaskMu.Lock()
	lastTaskID = taskID
	lastDaemonPort = daemonPort
	lastTaskMu.Unlock()
}

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
		MIMEType:    "text/html;profile=mcp-app",
	}, handleReadProgressApp)
}

func handleReadProgressApp(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	lastTaskMu.RLock()
	taskID := lastTaskID
	daemonPort := lastDaemonPort
	lastTaskMu.RUnlock()

	if daemonPort == "" {
		daemonPort = getDaemonPort()
	}

	// Inject the taskId and daemonPort into the HTML so the sandboxed iframe
	// has them available without needing to make localhost fetch calls.
	html := progressAppHTML
	if taskID != "" {
		injection := "<script>window.__TZRO_TASK_ID__='" + taskID + "';window.__TZRO_DAEMON_PORT__='" + daemonPort + "';</script>"
		html = strings.Replace(html, "<head>", "<head>"+injection, 1)
	} else if daemonPort != "" {
		injection := "<script>window.__TZRO_DAEMON_PORT__='" + daemonPort + "';</script>"
		html = strings.Replace(html, "<head>", "<head>"+injection, 1)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      appResourceURI,
				MIMEType: "text/html;profile=mcp-app",
				Text:     html,
				Meta: mcp.Meta{
					"ui": map[string]any{
						"csp": map[string]any{
							"connectDomains": []string{
								"http://127.0.0.1:*",
								"http://localhost:*",
							},
						},
						"prefersBorder": false,
					},
				},
			},
		},
	}, nil
}

// getDaemonPortForApp returns the daemon port, used by app.go to populate the HTML.
// Re-exported here to avoid circular dependency; the canonical helper is in tools.go.
func getDaemonPortForApp() string {
	daemonURL := config.GetDaemonURL()
	if idx := strings.LastIndex(daemonURL, ":"); idx != -1 {
		return daemonURL[idx+1:]
	}
	return "8080"
}

