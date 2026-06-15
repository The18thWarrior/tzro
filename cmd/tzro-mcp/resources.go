package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/memory"
	"tzro/internal/stream"
)

var (
	taskOutputRegex     = regexp.MustCompile(`^tzro://tasks/([^/?]+)/output(?:\?.*)?$`)
	nodeOutputRegex     = regexp.MustCompile(`^tzro://tasks/([^/?]+)/nodes/([^/?]+)/output(?:\?.*)?$`)
	sentinelAlertsRegex = regexp.MustCompile(`^tzro://sentinel/alerts(?:\?.*)?$`)
)

func getResourcesServerOptions() *mcp.ServerOptions {
	return &mcp.ServerOptions{
		SubscribeHandler: func(ctx context.Context, req *mcp.SubscribeRequest) error {
			uri := req.Params.URI
			if taskOutputRegex.MatchString(uri) || nodeOutputRegex.MatchString(uri) || sentinelAlertsRegex.MatchString(uri) {
				return nil
			}
			return fmt.Errorf("invalid subscription URI: %s", uri)
		},
		UnsubscribeHandler: func(ctx context.Context, req *mcp.UnsubscribeRequest) error {
			uri := req.Params.URI
			if taskOutputRegex.MatchString(uri) || nodeOutputRegex.MatchString(uri) || sentinelAlertsRegex.MatchString(uri) {
				return nil
			}
			return fmt.Errorf("invalid unsubscription URI: %s", uri)
		},
	}
}

func registerResources(server *mcp.Server) {
	// Register the task output resource template using RFC 6570 optional query parameters
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "tzro://tasks/{taskId}/output{?format}",
		Name:        "task-output",
		Description: "Consolidated, real-time output and status metrics for a Task.",
	}, handleReadTaskOutputResource)

	// Register the node output resource template using RFC 6570 optional query parameters
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "tzro://tasks/{taskId}/nodes/{nodeId}/output{?format}",
		Name:        "node-output",
		Description: "Intermediate output for a single execution node step.",
	}, handleReadNodeOutputResource)

	// Register the sentinel alerts resource template (ADR-0023)
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "tzro://sentinel/alerts{?status}",
		Name:        "sentinel-alerts",
		Description: "Proactive insight alerts from the Sentinel Agent, filtered by status.",
	}, handleReadSentinelAlertsResource)

	// Start the background event bridge to trigger client updates
	startEventBridge(server)
}

func handleReadTaskOutputResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	matches := taskOutputRegex.FindStringSubmatch(uri)
	if len(matches) < 2 {
		return nil, fmt.Errorf("invalid task output resource URI: %s", uri)
	}
	taskID := matches[1]

	nodes := memory.DB.GetAllNodeStates(taskID)
	if len(nodes) == 0 {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{},
		}, nil
	}

	// Parse query parameters
	var isRaw bool
	u, err := url.Parse(uri)
	if err == nil {
		isRaw = u.Query().Get("format") == "raw"
	}

	// Zero out raw output for compact view if format=raw is NOT requested
	if !isRaw {
		for i := range nodes {
			nodes[i].RawOutput = ""
		}
	}

	payload, err := json.Marshal(nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal node states: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      uri,
				MIMEType: "application/json",
				Text:     string(payload),
			},
		},
	}, nil
}

func handleReadNodeOutputResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	matches := nodeOutputRegex.FindStringSubmatch(uri)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid node output resource URI: %s", uri)
	}
	taskID := matches[1]
	nodeID := matches[2]

	state, ok := memory.DB.GetNodeState(taskID, nodeID)
	if !ok {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{},
		}, nil
	}

	// Parse query parameters
	var isRaw bool
	u, err := url.Parse(uri)
	if err == nil {
		isRaw = u.Query().Get("format") == "raw"
	}

	// Zero out raw output for compact view if format=raw is NOT requested
	if !isRaw {
		state.RawOutput = ""
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal node state: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      uri,
				MIMEType: "application/json",
				Text:     string(payload),
			},
		},
	}, nil
}

func startEventBridge(server *mcp.Server) {
	// 1. Start local GlobalBus event bridge
	go startLocalBridge(context.Background(), server)

	// 2. Start remote SSE bridge in reconnect loop
	go func() {
		daemonURL := os.Getenv("TZRO_DAEMON_URL")
		if daemonURL == "" {
			port := "8080"
			if envPort := os.Getenv("PORT"); envPort != "" {
				port = envPort
			}
			daemonURL = "http://localhost:" + port
		}

		for {
			err := startSSEBridge(context.Background(), server, daemonURL)
			if err != nil {
				// Back off before retrying
				time.Sleep(5 * time.Second)
			}
		}
	}()
}

func startLocalBridge(ctx context.Context, server *mcp.Server) {
	sub := stream.GlobalBus.Subscribe(nil)
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-sub.Ch:
			if !ok {
				return
			}
			// Sentinel alert chunks trigger the sentinel resource update
			if chunk.Source == "sentinel" && chunk.Type == "sentinel_alert" {
				notifySentinelResourceUpdate(server)
			}
			notifyResourceUpdate(server, chunk.TaskID, chunk.NodeID)
		}
	}
}

func startSSEBridge(ctx context.Context, server *mcp.Server, daemonURL string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", daemonURL+"/api/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{
		Timeout: 0,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status from SSE endpoint: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		var chunk struct {
			TaskID string `json:"taskId"`
			NodeID string `json:"nodeId"`
			Source string `json:"source"`
			Type   string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if chunk.Source == "sentinel" && chunk.Type == "sentinel_alert" {
				notifySentinelResourceUpdate(server)
			}
			notifyResourceUpdate(server, chunk.TaskID, chunk.NodeID)
		}
	}

	return scanner.Err()
}

func notifyResourceUpdate(server *mcp.Server, taskID, nodeID string) {
	if taskID == "" {
		return
	}

	// Notify task aggregate subscription
	taskURI := fmt.Sprintf("tzro://tasks/%s/output", taskID)
	_ = server.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{
		URI: taskURI,
	})

	// Notify granular node subscription if present
	if nodeID != "" {
		nodeURI := fmt.Sprintf("tzro://tasks/%s/nodes/%s/output", taskID, nodeID)
		_ = server.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{
			URI: nodeURI,
		})
	}
}

// handleReadSentinelAlertsResource handles reads of the tzro://sentinel/alerts resource.
func handleReadSentinelAlertsResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI

	// Parse optional status query parameter
	status := "unread"
	u, err := url.Parse(uri)
	if err == nil {
		if s := u.Query().Get("status"); s != "" {
			status = s
		}
	}

	notifs, err := memory.DB.GetNotifications(status)
	if err != nil {
		return nil, fmt.Errorf("failed to query sentinel alerts: %w", err)
	}

	// Filter to sentinel-sourced notifications
	var sentinelAlerts []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "sentinel" {
			sentinelAlerts = append(sentinelAlerts, n)
		}
	}
	if sentinelAlerts == nil {
		sentinelAlerts = []memory.DurableNotification{}
	}

	payload, err := json.Marshal(map[string]interface{}{
		"alerts": sentinelAlerts,
		"count":  len(sentinelAlerts),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sentinel alerts: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      uri,
				MIMEType: "application/json",
				Text:     string(payload),
			},
		},
	}, nil
}

// notifySentinelResourceUpdate fires a ResourceUpdated notification for the sentinel alerts URI.
func notifySentinelResourceUpdate(server *mcp.Server) {
	_ = server.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{
		URI: "tzro://sentinel/alerts",
	})
}
