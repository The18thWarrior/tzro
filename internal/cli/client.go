package cli

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"tzro/internal/compiler"
	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/tui"
)

// Re-expose standard offline mutation error for CLI client commands.
var ErrOfflineMutation = tui.ErrOfflineMutation

// RESTClient implements tui.TZROClient via REST API and SSE queries.
type RESTClient struct {
	BaseURL string
}

func NewRESTClient(baseURL string) *RESTClient {
	return &RESTClient{BaseURL: baseURL}
}

func (c *RESTClient) GetTasks() ([]tui.TaskStateItem, error) {
	resp, err := http.Get(c.BaseURL + "/api/tasks")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error: status %s", resp.Status)
	}
	var list []tui.TaskStateItem
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *RESTClient) GetMemories() (tui.MemoryPayload, error) {
	resp, err := http.Get(c.BaseURL + "/api/memories")
	if err != nil {
		return tui.MemoryPayload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tui.MemoryPayload{}, fmt.Errorf("server error: status %s", resp.Status)
	}
	var payload tui.MemoryPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return tui.MemoryPayload{}, err
	}
	return payload, nil
}

func (c *RESTClient) AddMemory(userId, memType, content, context string, confidence float64) error {
	reqBody, err := json.Marshal(map[string]interface{}{
		"userId":     userId,
		"type":       memType,
		"content":    content,
		"context":    context,
		"confidence": confidence,
		"source":     "manual_cli",
	})
	if err != nil {
		return err
	}
	resp, err := http.Post(c.BaseURL+"/api/memories", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to save memory: status %s", resp.Status)
	}
	return nil
}

func (c *RESTClient) GetSkills() ([]memory.Skill, error) {
	resp, err := http.Get(c.BaseURL + "/api/skills")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error: status %s", resp.Status)
	}
	var list []memory.Skill
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *RESTClient) GetMCPList() (map[string]mcp.MCPServerConfig, error) {
	resp, err := http.Get(c.BaseURL + "/api/mcp")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error: status %s", resp.Status)
	}
	var list map[string]mcp.MCPServerConfig
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *RESTClient) GetSidecarStatus() (tui.SidecarStatus, error) {
	resp, err := http.Get(c.BaseURL + "/api/sidecar")
	if err != nil {
		return tui.SidecarStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tui.SidecarStatus{}, fmt.Errorf("server error: status %s", resp.Status)
	}
	var status tui.SidecarStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return tui.SidecarStatus{}, err
	}
	return status, nil
}

func (c *RESTClient) GetNotifications() ([]memory.DurableNotification, error) {
	resp, err := http.Get(c.BaseURL + "/api/notifications")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error: status %s", resp.Status)
	}
	var list []memory.DurableNotification
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *RESTClient) GetEventsStream(ctx context.Context) (<-chan stream.StreamChunk, error) {
	ch := make(chan stream.StreamChunk, 100)
	go startSSEStream(ctx, c.BaseURL, ch)
	return ch, nil
}

func (c *RESTClient) TriggerWorkflow(workflowId string) error {
	reqBody, _ := json.Marshal(map[string]string{"id": workflowId})
	resp, err := http.Post(c.BaseURL+"/api/workflows/trigger", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to trigger workflow: status %s", resp.Status)
	}
	return nil
}

func (c *RESTClient) ToggleWorkflow(workflowId string) error {
	reqBody, _ := json.Marshal(map[string]string{"id": workflowId})
	resp, err := http.Post(c.BaseURL+"/api/workflows/toggle", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to toggle workflow: status %s", resp.Status)
	}
	return nil
}

// DirectDBClient implements tui.TZROClient via read-only SQLite direct connection.
type DirectDBClient struct {
	DBPath string
}

func NewDirectDBClient(dbPath string) *DirectDBClient {
	return &DirectDBClient{DBPath: dbPath}
}

func (c *DirectDBClient) initDB() error {
	if memory.DB.RawDB() != nil {
		return nil
	}
	memory.DB.SetDBPathForTesting(c.DBPath)
	return memory.DB.Init()
}

func (c *DirectDBClient) GetTasks() ([]tui.TaskStateItem, error) {
	if err := c.initDB(); err != nil {
		return nil, err
	}
	db := memory.DB.RawDB()
	if db == nil {
		return nil, errors.New("database not initialized")
	}

	rows, err := db.Query("SELECT DISTINCT task_id FROM node_states")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []tui.TaskStateItem
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, err
		}

		nodeRows, err := db.Query("SELECT node_id, status, output, completed_at FROM node_states WHERE task_id = ?", taskID)
		if err != nil {
			return nil, err
		}

		statesMap := make(map[string]interface{})
		var nodes []compiler.GraphNode
		var minCompletedAt int64 = 0

		for nodeRows.Next() {
			var nodeID, status, output string
			var completedAt int64
			if err := nodeRows.Scan(&nodeID, &status, &output, &completedAt); err != nil {
				nodeRows.Close()
				return nil, err
			}
			statesMap[nodeID] = map[string]interface{}{
				"status":      status,
				"output":      output,
				"completedAt": completedAt,
			}
			nodes = append(nodes, compiler.GraphNode{
				ID:     nodeID,
				Status: status,
				Output: output,
			})
			if minCompletedAt == 0 || completedAt < minCompletedAt {
				minCompletedAt = completedAt
			}
		}
		nodeRows.Close()

		g := &compiler.ExecutionGraph{
			TaskID:    taskID,
			Nodes:     nodes,
			CreatedAt: minCompletedAt,
		}

		list = append(list, tui.TaskStateItem{
			TaskID:    taskID,
			Graph:     g,
			States:    statesMap,
			CreatedAt: minCompletedAt,
		})
	}

	return list, nil
}

func (c *DirectDBClient) GetMemories() (tui.MemoryPayload, error) {
	if err := c.initDB(); err != nil {
		return tui.MemoryPayload{}, err
	}
	facts := memory.DB.GetMemories()
	nodeMap := memory.DB.GetNodes()
	edgeMap := memory.DB.GetEdges()

	var nodes []memory.KGNode
	for _, n := range nodeMap {
		nodes = append(nodes, n)
	}

	var edges []memory.KGEdge
	for _, e := range edgeMap {
		edges = append(edges, e)
	}

	return tui.MemoryPayload{
		Facts: facts,
		Nodes: nodes,
		Edges: edges,
	}, nil
}

func (c *DirectDBClient) AddMemory(userId, memType, content, context string, confidence float64) error {
	return ErrOfflineMutation
}

func (c *DirectDBClient) GetSkills() ([]memory.Skill, error) {
	if err := c.initDB(); err != nil {
		return nil, err
	}
	return memory.DB.GetSkills(), nil
}

func (c *DirectDBClient) GetMCPList() (map[string]mcp.MCPServerConfig, error) {
	return make(map[string]mcp.MCPServerConfig), nil
}

func (c *DirectDBClient) GetSidecarStatus() (tui.SidecarStatus, error) {
	return tui.SidecarStatus{
		Status: "Inactive",
	}, nil
}

func (c *DirectDBClient) GetNotifications() ([]memory.DurableNotification, error) {
	if err := c.initDB(); err != nil {
		return nil, err
	}
	db := memory.DB.RawDB()
	if db == nil {
		return nil, errors.New("database not initialized")
	}

	rows, err := db.Query("SELECT id, source, type, title, message, task_id, workflow_id, target_id, status, action_payload, created_at FROM durable_notifications ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []memory.DurableNotification
	for rows.Next() {
		var n memory.DurableNotification
		var taskID, workflowID, targetID, actionPayload sql.NullString
		err := rows.Scan(&n.ID, &n.Source, &n.Type, &n.Title, &n.Message, &taskID, &workflowID, &targetID, &n.Status, &actionPayload, &n.CreatedAt)
		if err != nil {
			return nil, err
		}
		if taskID.Valid {
			n.TaskID = taskID.String
		}
		if workflowID.Valid {
			n.WorkflowID = workflowID.String
		}
		if targetID.Valid {
			n.TargetID = targetID.String
		}
		if actionPayload.Valid {
			n.ActionPayload = actionPayload.String
		}
		list = append(list, n)
	}
	return list, nil
}

func (c *DirectDBClient) GetEventsStream(ctx context.Context) (<-chan stream.StreamChunk, error) {
	return nil, ErrOfflineMutation
}

func (c *DirectDBClient) TriggerWorkflow(workflowId string) error {
	return ErrOfflineMutation
}

func (c *DirectDBClient) ToggleWorkflow(workflowId string) error {
	return ErrOfflineMutation
}

// startSSEStream reads standard Server-Sent Events from daemon and writes them to channels.
func startSSEStream(ctx context.Context, baseURL string, ch chan<- stream.StreamChunk) {
	defer close(ch)

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/events", nil)
	if err != nil {
		return
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var chunk stream.StreamChunk
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}

		select {
		case ch <- chunk:
		case <-ctx.Done():
			return
		default:
		}
	}
}
