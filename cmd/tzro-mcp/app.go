package main

//go:generate bash build-app.sh

import (
	"context"
	_ "embed"
	"net/url"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/config"
)

//go:embed app/progress.html
var progressAppHTML string

// appResourceURIBase is the base MCP Apps resource URI for the progress visualization.
// The ui:// scheme tells MCP hosts this is an interactive App resource that
// should be rendered in a sandboxed iframe. Query parameters (?taskId=...&port=...)
// are appended per-invocation so the URI is self-contained and survives server restarts.
const appResourceURIBase = "ui://tzro/progress.html"

// lastTaskState tracks the most recent task launched via tzro_run so the
// progress app can be pre-populated when the host reads the resource.
// This is the in-memory fallback; the primary mechanism is query params in the URI.
var (
	lastTaskMu     sync.RWMutex
	lastTaskID     string
	lastDaemonPort string
)

// setLastTask records the most recently launched task for the progress app.
func setLastTask(taskID, daemonPort string) {
	lastTaskMu.Lock()
	lastTaskID = taskID
	lastDaemonPort = daemonPort
	lastTaskMu.Unlock()
}

// buildAppResourceURI constructs a concrete app resource URI with taskId and
// port encoded as query parameters. This makes the URI self-contained so it
// survives MCP server restarts — the harness can always re-read task details.
func buildAppResourceURI(taskID, daemonPort string) string {
	if taskID == "" && daemonPort == "" {
		return appResourceURIBase
	}
	params := url.Values{}
	if taskID != "" {
		params.Set("taskId", taskID)
	}
	if daemonPort != "" {
		params.Set("port", daemonPort)
	}
	return appResourceURIBase + "?" + params.Encode()
}

// registerApp registers the MCP App resource template for task progress visualization.
// Uses RFC 6570 URI Templates with optional query parameters so each invocation
// can carry its own taskId and port. Hosts that support MCP Apps (Claude Desktop,
// VS Code Copilot, Goose, etc.) will render this inline in the conversation when
// tools reference it via _meta.ui.resourceUri.
func registerApp(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "ui://tzro/progress.html{?taskId,port}",
		Name:        "tzro-progress",
		Title:       "Task Progress",
		Description: "Interactive progress visualization for tzro task execution. Shows DAG graph, node status, progress bar, timing metrics, and cancel button.",
		MIMEType:    "text/html;profile=mcp-app",
	}, handleReadProgressApp)
}

func handleReadProgressApp(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI

	// Primary: extract taskId and port from URI query parameters.
	// This is the durable mechanism that survives server restarts.
	var taskID, daemonPort string
	u, err := url.Parse(uri)
	if err == nil {
		taskID = u.Query().Get("taskId")
		daemonPort = u.Query().Get("port")
	}

	// Fallback: use in-memory last-task state for backward compatibility
	// (e.g., hosts that read the base URI without query params).
	if taskID == "" {
		lastTaskMu.RLock()
		taskID = lastTaskID
		if daemonPort == "" {
			daemonPort = lastDaemonPort
		}
		lastTaskMu.RUnlock()
	}

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
				URI:      uri,
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

