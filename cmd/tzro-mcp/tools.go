package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/executor"
	internalmcp "tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/task"
	"tzro/internal/tools"
)

// tzro_run tool definition

// TzroRunArgs defines the inputs for running a natural language task.
type TzroRunArgs struct {
	Prompt  string `json:"prompt" jsonschema:"required,The natural language task to execute"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"Execution timeout in seconds before switching to async. Default 60"`
}

func handleTzroRun(ctx context.Context, req *mcp.CallToolRequest, args TzroRunArgs) (*mcp.CallToolResult, any, error) {
	taskID := uuid.New().String()
	timeoutSec := args.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	type execResult struct {
		nodes []memory.NodeState
		err   error
	}

	doneChan := make(chan execResult, 1)

	// Execute task in a background goroutine to allow fallback to async mode if it times out
	go func() {
		_, _, err := task.Execute(context.Background(), args.Prompt, task.ExecuteOptions{
			TaskID:     taskID,
			IntentType: "workflow",
		})
		nodes := memory.DB.GetAllNodeStates(taskID)
		doneChan <- execResult{nodes: nodes, err: err}
	}()

	select {
	case res := <-doneChan:
		status := "completed"
		var errMsg string
		if res.err != nil {
			status = "failed"
			errMsg = res.err.Error()
		}

		respMap := map[string]interface{}{
			"taskId": taskID,
			"status": status,
			"nodes":  res.nodes,
		}
		if errMsg != "" {
			respMap["error"] = errMsg
		}

		respBytes, _ := json.MarshalIndent(respMap, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil

	case <-time.After(time.Duration(timeoutSec) * time.Second):
		// Exceeded timeout limit: let it run in background and return async details
		respMap := map[string]interface{}{
			"taskId": taskID,
			"status": "running",
		}
		respBytes, _ := json.MarshalIndent(respMap, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil
	}
}

// tzro_status tool definition

// TzroStatusArgs defines the inputs for checking task execution status.
type TzroStatusArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to check"`
}

func handleTzroStatus(ctx context.Context, req *mcp.CallToolRequest, args TzroStatusArgs) (*mcp.CallToolResult, any, error) {
	nodes := memory.DB.GetAllNodeStates(args.TaskID)
	if len(nodes) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"taskId": "%s", "error": "task not found"}`, args.TaskID)},
			},
			IsError: true,
		}, nil, nil
	}

	failedCount := 0
	runningCount := 0
	completedCount := 0
	nodeCount := len(nodes)
	var completedAt int64

	for _, n := range nodes {
		if n.Status == "failed" {
			failedCount++
		} else if n.Status == "running" {
			runningCount++
		} else if n.Status == "completed" {
			completedCount++
		}
		if n.CompletedAt > completedAt {
			completedAt = n.CompletedAt
		}
	}

	taskStatus := "pending"
	if failedCount > 0 {
		taskStatus = "failed"
	} else if runningCount > 0 {
		taskStatus = "running"
	} else if completedCount == nodeCount {
		taskStatus = "completed"
	}

	respMap := map[string]interface{}{
		"taskId":      args.TaskID,
		"status":      taskStatus,
		"nodes":       nodes,
		"completedAt": completedAt,
	}

	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_list_tasks tool definition

// TzroListTasksArgs defines the inputs for listing recent tasks.
type TzroListTasksArgs struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of tasks to return. Default 20"`
	Status string `json:"status,omitempty" jsonschema:"Filter by status: running, completed, failed. Default all"`
}

func handleTzroListTasks(ctx context.Context, req *mcp.CallToolRequest, args TzroListTasksArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	tasks, err := memory.DB.GetRecentTasks(limit, args.Status)
	if err != nil {
		return nil, nil, err
	}

	respBytes, _ := json.MarshalIndent(tasks, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// tzro_configure_tools tool definition

// TzroConfigureToolsArgs defines the inputs for configuring new daemon tools.
type TzroConfigureToolsArgs struct {
	Servers map[string]internalmcp.MCPServerConfig `json:"servers" jsonschema:"required,Map of server name to MCP server config"`
}

func handleTzroConfigureTools(ctx context.Context, req *mcp.CallToolRequest, args TzroConfigureToolsArgs) (*mcp.CallToolResult, any, error) {
	configPath := filepath.Join(".tzro", "mcp_config.json")

	// Read existing config or initialize empty
	var mcpCfg internalmcp.MCPConfig
	mcpCfg.MCPServers = make(map[string]internalmcp.MCPServerConfig)

	if _, err := os.Stat(configPath); err == nil {
		fileBytes, err := os.ReadFile(configPath)
		if err == nil {
			_ = json.Unmarshal(fileBytes, &mcpCfg)
		}
	}

	// Merge new entries
	for k, v := range args.Servers {
		mcpCfg.MCPServers[k] = v
	}

	// Write back to disk
	mergedBytes, err := json.MarshalIndent(mcpCfg, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal merged config: %w", err)
	}

	_ = os.MkdirAll(".tzro", 0755)
	if err := os.WriteFile(configPath, mergedBytes, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write mcp_config.json: %w", err)
	}

	// Reload daemon config
	if err := internalmcp.GlobalRegistry.LoadConfig(configPath); err != nil {
		return nil, nil, fmt.Errorf("failed to reload daemon config: %w", err)
	}

	// Discover new tools
	newTools, err := internalmcp.GlobalRegistry.DiscoverTools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover tools: %w", err)
	}

	// Gather newly discovered tool names
	var toolNames []string
	for name := range newTools {
		toolNames = append(toolNames, name)
	}

	respMap := map[string]interface{}{
		"status":          "success",
		"discoveredTools": toolNames,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroMemoryQueryArgs defines inputs for tzro_memory_query.
type TzroMemoryQueryArgs struct {
	Query string `json:"query" jsonschema:"required,The natural language semantic search query"`
	Limit int    `json:"limit,omitempty" jsonschema:"Max number of memories and nodes to return. Default 10"`
}

func handleTzroMemoryQuery(ctx context.Context, req *mcp.CallToolRequest, args TzroMemoryQueryArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	mems, nodes, err := memory.DB.SearchMemoriesAndNodes(args.Query, limit)
	if err != nil {
		return nil, nil, err
	}
	respMap := map[string]interface{}{
		"memories": mems,
		"nodes":    nodes,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroMemoryIngestArgs defines inputs for tzro_memory_ingest.
type TzroMemoryIngestArgs struct {
	Type       string  `json:"type" jsonschema:"required,The category of memory: fact, preference, insight, correction, anti_pattern, strategy"`
	Content    string  `json:"content" jsonschema:"required,The text content representing the memory"`
	UserID     string  `json:"userId,omitempty" jsonschema:"User ID this memory belongs to"`
	Context    string  `json:"context,omitempty" jsonschema:"Session ID or context description"`
	Confidence float64 `json:"confidence,omitempty" jsonschema:"Confidence score between 0.0 and 1.0"`
}

func handleTzroMemoryIngest(ctx context.Context, req *mcp.CallToolRequest, args TzroMemoryIngestArgs) (*mcp.CallToolResult, any, error) {
	var embedding []float32
	if memory.DB.EmbeddingEngine != nil {
		vec, err := memory.DB.EmbeddingEngine.Embed(ctx, args.Content)
		if err == nil {
			embedding = vec
		}
	}
	conf := args.Confidence
	if conf <= 0 {
		conf = 1.0
	}
	m := memory.FactMemory{
		UserID:     args.UserID,
		Type:       args.Type,
		Content:    args.Content,
		Context:    args.Context,
		Confidence: conf,
		Source:     "mcp_ingest",
		Embedding:  embedding,
	}
	if err := memory.DB.AddMemory(m); err != nil {
		return nil, nil, err
	}
	respMap := map[string]interface{}{
		"status":  "success",
		"content": args.Content,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroKgNeighborhoodArgs defines inputs for tzro_kg_neighborhood.
type TzroKgNeighborhoodArgs struct {
	EntityID  string   `json:"entityId" jsonschema:"required,The node ID from which to start traversal"`
	MaxHops   int      `json:"maxHops,omitempty" jsonschema:"Max traverse steps. Default 2"`
	NodeTypes []string `json:"nodeTypes,omitempty" jsonschema:"Restrict traversal to these node types"`
	EdgeTypes []string `json:"edgeTypes,omitempty" jsonschema:"Restrict traversal to these edge types"`
	Direction string   `json:"direction,omitempty" jsonschema:"Traversal direction: incoming, outgoing, undirected. Default undirected"`
	Limit     int      `json:"limit,omitempty" jsonschema:"Max number of nodes to return"`
}

func handleTzroKgNeighborhood(ctx context.Context, req *mcp.CallToolRequest, args TzroKgNeighborhoodArgs) (*mcp.CallToolResult, any, error) {
	maxHops := args.MaxHops
	if maxHops <= 0 {
		maxHops = 2
	}
	var opts []memory.NeighborhoodOption
	if len(args.NodeTypes) > 0 {
		opts = append(opts, memory.WithNodeTypes(args.NodeTypes))
	}
	if len(args.EdgeTypes) > 0 {
		opts = append(opts, memory.WithEdgeTypes(args.EdgeTypes))
	}
	if args.Direction != "" {
		opts = append(opts, memory.WithDirection(args.Direction))
	}
	if args.Limit > 0 {
		opts = append(opts, memory.WithLimit(args.Limit))
	}
	subgraph := memory.DB.GetEntityNeighborhood(args.EntityID, maxHops, opts...)
	respBytes, _ := json.MarshalIndent(subgraph, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// KGNodeInput defines node properties for tzro_kg_add_entity.
type KGNodeInput struct {
	ID       string                 `json:"id" jsonschema:"required,Node unique identifier"`
	NodeType string                 `json:"nodeType" jsonschema:"required,Node type e.g. account, contact, ticket, document"`
	Name     string                 `json:"name" jsonschema:"required,Display name of entity"`
	Metadata map[string]interface{} `json:"metadata,omitempty" jsonschema:"Arbitrary node metadata properties"`
	Source   string                 `json:"source,omitempty" jsonschema:"Provenance source"`
	Weight   float64                `json:"weight,omitempty" jsonschema:"Importance weight between 0.0 and 1.0"`
}

// KGEdgeInput defines edge properties for tzro_kg_add_entity.
type KGEdgeInput struct {
	ID       string                 `json:"id" jsonschema:"required,Edge unique identifier"`
	EdgeType string                 `json:"edgeType" jsonschema:"required,Edge type e.g. belongs_to, assigned_to, references"`
	SourceID string                 `json:"sourceId" jsonschema:"required,Source node ID"`
	TargetID string                 `json:"targetId" jsonschema:"required,Target node ID"`
	Metadata map[string]interface{} `json:"metadata,omitempty" jsonschema:"Arbitrary edge metadata properties"`
	Weight   float64                `json:"weight,omitempty" jsonschema:"Edge weight between 0.0 and 1.0"`
}

// TzroKgAddEntityArgs defines inputs for tzro_kg_add_entity.
type TzroKgAddEntityArgs struct {
	Node *KGNodeInput `json:"node,omitempty" jsonschema:"Node entity to add/update"`
	Edge *KGEdgeInput `json:"edge,omitempty" jsonschema:"Edge relationship to add/update"`
}

func handleTzroKgAddEntity(ctx context.Context, req *mcp.CallToolRequest, args TzroKgAddEntityArgs) (*mcp.CallToolResult, any, error) {
	if args.Node == nil && args.Edge == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "must provide at least one of node or edge"}`},
			},
			IsError: true,
		}, nil, nil
	}

	if args.Node != nil {
		if args.Node.ID == "" || args.Node.NodeType == "" || args.Node.Name == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "node requires id, nodeType, and name"}`},
				},
				IsError: true,
			}, nil, nil
		}
		var embedding []float32
		if memory.DB.EmbeddingEngine != nil {
			vec, err := memory.DB.EmbeddingEngine.Embed(ctx, args.Node.Name+" "+args.Node.NodeType)
			if err == nil {
				embedding = vec
			}
		}
		weight := args.Node.Weight
		if weight <= 0 {
			weight = 1.0
		}
		node := memory.KGNode{
			ID:        args.Node.ID,
			NodeType:  args.Node.NodeType,
			Name:      args.Node.Name,
			Metadata:  args.Node.Metadata,
			Source:    args.Node.Source,
			Weight:    weight,
			Embedding: embedding,
		}
		if err := memory.DB.AddNode(node); err != nil {
			return nil, nil, err
		}
	}

	if args.Edge != nil {
		if args.Edge.ID == "" || args.Edge.EdgeType == "" || args.Edge.SourceID == "" || args.Edge.TargetID == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "edge requires id, edgeType, sourceId, and targetId"}`},
				},
				IsError: true,
			}, nil, nil
		}
		weight := args.Edge.Weight
		if weight <= 0 {
			weight = 1.0
		}
		edge := memory.KGEdge{
			ID:       args.Edge.ID,
			EdgeType: args.Edge.EdgeType,
			SourceID: args.Edge.SourceID,
			TargetID: args.Edge.TargetID,
			Metadata: args.Edge.Metadata,
			Weight:   weight,
		}
		if err := memory.DB.AddEdge(edge); err != nil {
			return nil, nil, err
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: `{"status": "success"}`},
		},
	}, nil, nil
}

// TzroRagContextArgs defines inputs for tzro_rag_context.
type TzroRagContextArgs struct {
	Query    string `json:"query" jsonschema:"required,The user query or prompt to retrieve context for"`
	MaxChars int    `json:"maxChars,omitempty" jsonschema:"Max character limit of the returned context. Default 2000"`
}

func handleTzroRagContext(ctx context.Context, req *mcp.CallToolRequest, args TzroRagContextArgs) (*mcp.CallToolResult, any, error) {
	maxChars := args.MaxChars
	if maxChars <= 0 {
		maxChars = 2000
	}
	ragStr := memory.DB.GetGraphRAGContext(args.Query, maxChars)
	respMap := map[string]interface{}{
		"context": ragStr,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsListArgs defines inputs for tzro_skills_list.
type TzroSkillsListArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max number of skills to return"`
}

func handleTzroSkillsList(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsListArgs) (*mcp.CallToolResult, any, error) {
	skills := memory.DB.GetSkills()
	if args.Limit > 0 && len(skills) > args.Limit {
		skills = skills[:args.Limit]
	}
	respBytes, _ := json.MarshalIndent(skills, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsGetArgs defines inputs for tzro_skills_get.
type TzroSkillsGetArgs struct {
	ID string `json:"id" jsonschema:"required,The skill ID to retrieve"`
}

func handleTzroSkillsGet(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsGetArgs) (*mcp.CallToolResult, any, error) {
	s, err := memory.DB.GetSkill(args.ID)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "%s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}
	respBytes, _ := json.MarshalIndent(s, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsRelevantArgs defines inputs for tzro_skills_relevant.
type TzroSkillsRelevantArgs struct {
	Prompt string `json:"prompt" jsonschema:"required,The user prompt or query to match skills against"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of relevant skills to return. Default 5"`
}

func handleTzroSkillsRelevant(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsRelevantArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	skills := memory.DB.GetRelevantSkills(args.Prompt, limit)
	respBytes, _ := json.MarshalIndent(skills, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroSkillsAddArgs defines inputs for tzro_skills_add.
type TzroSkillsAddArgs struct {
	Name               string `json:"name" jsonschema:"required,The name of the micro-skill/SOP"`
	TriggerDescription string `json:"triggerDescription" jsonschema:"required,Description of scenarios that trigger this SOP"`
	SOPContent         string `json:"sopContent" jsonschema:"required,The step-by-step Standard Operating Procedure content in Markdown"`
}

func handleTzroSkillsAdd(ctx context.Context, req *mcp.CallToolRequest, args TzroSkillsAddArgs) (*mcp.CallToolResult, any, error) {
	s := &memory.Skill{
		Name:               args.Name,
		TriggerDescription: args.TriggerDescription,
		SOPContent:         args.SOPContent,
	}
	if err := memory.DB.AddSkill(s); err != nil {
		return nil, nil, err
	}
	respMap := map[string]interface{}{
		"status": "success",
		"skill":  s,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroHookListArgs defines inputs for tzro_hook_list.
type TzroHookListArgs struct{}

func handleTzroHookList(ctx context.Context, req *mcp.CallToolRequest, args TzroHookListArgs) (*mcp.CallToolResult, any, error) {
	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var list []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "human_approval" && n.Type == "approval_request" {
			list = append(list, n)
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroHookApproveArgs defines inputs for tzro_hook_approve.
type TzroHookApproveArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to approve"`
	NodeID string `json:"nodeId" jsonschema:"required,The node ID to approve"`
}

func handleTzroHookApprove(ctx context.Context, req *mcp.CallToolRequest, args TzroHookApproveArgs) (*mcp.CallToolResult, any, error) {
	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var target *memory.DurableNotification
	for _, n := range notifs {
		if n.TaskID == args.TaskID && n.TargetID == args.NodeID && n.Source == "human_approval" && n.Type == "approval_request" {
			target = &n
			break
		}
	}
	if target == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "unread approval request for task '%s' node '%s' not found"}`, args.TaskID, args.NodeID)},
			},
			IsError: true,
		}, nil, nil
	}

	// Update notification status to approved
	if err := memory.DB.UpdateNotificationStatus(target.ID, "approved"); err != nil {
		return nil, nil, err
	}

	// Trigger task resume in the background
	go func() {
		_ = runResumeTask(context.Background(), args.TaskID)
	}()

	respMap := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Node %s approved and task resume triggered.", args.NodeID),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroResumeArgs defines inputs for tzro_resume.
type TzroResumeArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to resume"`
}

func handleTzroResume(ctx context.Context, req *mcp.CallToolRequest, args TzroResumeArgs) (*mcp.CallToolResult, any, error) {
	go func() {
		_ = runResumeTask(context.Background(), args.TaskID)
	}()

	respMap := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Task %s resume triggered in background.", args.TaskID),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

func runResumeTask(ctx context.Context, taskID string) error {
	db := memory.DB.RawDB()
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	var graphBytes string
	err := db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", "graph_"+taskID).Scan(&graphBytes)
	if err != nil {
		return fmt.Errorf("failed to load cached graph for task %s: %w", taskID, err)
	}

	var graph compiler.ExecutionGraph
	if err := json.Unmarshal([]byte(graphBytes), &graph); err != nil {
		return fmt.Errorf("failed to unmarshal cached graph: %w", err)
	}

	levels, err := compiler.CompileAndSort(&graph)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}

	// Run graph execution
	return executor.GlobalEngine.ExecuteGraph(ctx, &graph, levels)
}

// TzroObserverEventsArgs defines inputs for tzro_observer_events.
type TzroObserverEventsArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max number of events to return. Default 10"`
}

func handleTzroObserverEvents(ctx context.Context, req *mcp.CallToolRequest, args TzroObserverEventsArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	notifs, err := memory.DB.GetNotifications("")
	if err != nil {
		return nil, nil, err
	}
	var list []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "observer" {
			list = append(list, n)
			if len(list) >= limit {
				break
			}
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroObserverMemoriesArgs defines inputs for tzro_observer_memories.
type TzroObserverMemoriesArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max number of memories to return. Default 10"`
}

func handleTzroObserverMemories(ctx context.Context, req *mcp.CallToolRequest, args TzroObserverMemoriesArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	mems := memory.DB.GetMemories()
	var list []memory.FactMemory
	for _, m := range mems {
		if m.Source == "auto_reflection" {
			list = append(list, m)
			if len(list) >= limit {
				break
			}
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// ClientToolInput defines one client tool registration
type ClientToolInput struct {
	Name        string                 `json:"name" jsonschema:"required,Name of the client tool"`
	Description string                 `json:"description" jsonschema:"required,Description of the client-side tool"`
	InputSchema map[string]interface{} `json:"inputSchema" jsonschema:"required,JSON Schema parameters of the tool"`
}

// TzroRegisterClientToolsArgs defines inputs for tzro_register_client_tools
type TzroRegisterClientToolsArgs struct {
	Tools []ClientToolInput `json:"tools" jsonschema:"required,List of client-side tools to register"`
}

func handleTzroRegisterClientTools(ctx context.Context, req *mcp.CallToolRequest, args TzroRegisterClientToolsArgs) (*mcp.CallToolResult, any, error) {
	var registered []string
	for _, t := range args.Tools {
		gbnfSchema, err := internalmcp.GetGBNFSchema(t.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to wrap schema for tool %s: %w", t.Name, err)
		}
		adapter := &tools.ClientToolAdapter{
			NameVal:        t.Name,
			DescriptionVal: t.Description,
			SchemaVal:      gbnfSchema,
		}
		tools.Register(adapter)
		registered = append(registered, t.Name)
	}

	respMap := map[string]interface{}{
		"status":          "success",
		"registeredTools": registered,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroClientToolListArgs defines inputs for tzro_client_tool_list
type TzroClientToolListArgs struct{}

func handleTzroClientToolList(ctx context.Context, req *mcp.CallToolRequest, args TzroClientToolListArgs) (*mcp.CallToolResult, any, error) {
	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var list []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "client_tool" && n.Type == "client_tool_request" {
			list = append(list, n)
		}
	}
	respBytes, _ := json.MarshalIndent(list, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroClientToolSubmitArgs defines inputs for tzro_client_tool_submit
type TzroClientToolSubmitArgs struct {
	TaskID string `json:"taskId" jsonschema:"required,The task ID to resume"`
	NodeID string `json:"nodeId" jsonschema:"required,The node ID containing the client tool step"`
	Output string `json:"output" jsonschema:"required,The output/result from client-side execution"`
}

func handleTzroClientToolSubmit(ctx context.Context, req *mcp.CallToolRequest, args TzroClientToolSubmitArgs) (*mcp.CallToolResult, any, error) {
	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var target *memory.DurableNotification
	for _, n := range notifs {
		if n.TaskID == args.TaskID && n.TargetID == args.NodeID && n.Source == "client_tool" && n.Type == "client_tool_request" {
			target = &n
			break
		}
	}
	if target == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "unread client tool request for task '%s' node '%s' not found"}`, args.TaskID, args.NodeID)},
			},
			IsError: true,
		}, nil, nil
	}

	// 1. Update notification status to approved
	if err := memory.DB.UpdateNotificationStatus(target.ID, "approved"); err != nil {
		return nil, nil, err
	}

	// 2. Perform caching/compaction and write completed node state
	displayOutput := args.Output
	compactedOutput, cacheID, err := cache.Process(ctx, args.Output, "")
	if err == nil && cacheID != "" {
		displayOutput = compactedOutput
	}

	nodeStatus := fmt.Sprintf("[Client Execution] %s", displayOutput)
	_ = memory.DB.SetNodeState(args.TaskID, args.NodeID, "completed", nodeStatus)
	_ = memory.DB.SetNodeRawOutput(args.TaskID, args.NodeID, args.Output)

	// Publish node completed event & stream update to keep UI in sync
	stream.GlobalBus.Publish(stream.StreamChunk{
		Source:  "executor",
		TaskID:  args.TaskID,
		NodeID:  args.NodeID,
		Type:    "event",
		Content: fmt.Sprintf("Node %s completed client-side execution", args.NodeID),
	})

	if statePayload, err := json.Marshal(map[string]string{"status": "completed", "output": nodeStatus}); err == nil {
		stream.GlobalBus.Publish(stream.StreamChunk{
			Source:  "executor",
			TaskID:  args.TaskID,
			NodeID:  args.NodeID,
			Type:    "node_state",
			Content: string(statePayload),
		})
	}

	// 3. Trigger task resume in the background
	go func() {
		_ = runResumeTask(context.Background(), args.TaskID)
	}()

	respMap := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Client tool output submitted for node %s and task resume triggered.", args.NodeID),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// registerTools registers all tools with the MCP server.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_run",
		Description: "Plan, compile, and execute a durable DAG workflow from a natural language prompt.",
	}, handleTzroRun)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_status",
		Description: "Check the execution status, node states, and outcomes of a specific tzro task by its ID.",
	}, handleTzroStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_list_tasks",
		Description: "List recent planning and execution tasks, optionally filtered by status.",
	}, handleTzroListTasks)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_configure_tools",
		Description: "Configure and provision external MCP server hosts dynamically that tzro can use during DAG planning and execution.",
	}, handleTzroConfigureTools)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_memory_query",
		Description: "Query fact memories and knowledge graph nodes using hybrid semantic/text similarity.",
	}, handleTzroMemoryQuery)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_memory_ingest",
		Description: "Ingest a new fact memory into the sqlite database, embedding it if active.",
	}, handleTzroMemoryIngest)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_kg_neighborhood",
		Description: "Traverse the connected entities in the knowledge graph starting from a node up to max hops.",
	}, handleTzroKgNeighborhood)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_kg_add_entity",
		Description: "Add or update nodes and/or edge relationships in the relational knowledge graph.",
	}, handleTzroKgAddEntity)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_rag_context",
		Description: "Get graph-RAG context retrieved semantically for a natural language query.",
	}, handleTzroRagContext)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_skills_list",
		Description: "List all micro-skills and SOPs registered in the tzro database.",
	}, handleTzroSkillsList)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_skills_get",
		Description: "Get full details of a specific SOP skill by its ID.",
	}, handleTzroSkillsGet)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_skills_relevant",
		Description: "Find relevant micro-skills and SOPs using dynamic semantic search.",
	}, handleTzroSkillsRelevant)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_skills_add",
		Description: "Add a new SOP micro-skill to enable bidirectional execution coordination.",
	}, handleTzroSkillsAdd)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_hook_list",
		Description: "List human-in-the-loop workflow approval requests awaiting action.",
	}, handleTzroHookList)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_hook_approve",
		Description: "Approve a paused human-in-the-loop task execution step and trigger resumption.",
	}, handleTzroHookApprove)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_resume",
		Description: "Manually resume execution of a paused/interrupted workflow task by its ID.",
	}, handleTzroResume)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_observer_events",
		Description: "Retrieve recent observer verification and audit telemetry logs.",
	}, handleTzroObserverEvents)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_observer_memories",
		Description: "List memories dynamically synthesized by the background Observer Agent.",
	}, handleTzroObserverMemories)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_register_client_tools",
		Description: "Register dynamic client-side tool definitions that the tzro planning engine can leverage.",
	}, handleTzroRegisterClientTools)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_client_tool_list",
		Description: "List pending client-side tool execution requests awaiting outcomes.",
	}, handleTzroClientToolList)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_client_tool_submit",
		Description: "Submit execution outcomes for a client-side tool to resume the paused workflow.",
	}, handleTzroClientToolSubmit)
}
