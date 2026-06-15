package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/channel"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	internalmcp "tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/sentinel"
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
	if strings.TrimSpace(args.Prompt) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "prompt cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	taskID := uuid.New().String()
	timeoutSec := args.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	// Start SubagentChannel for real-time event delivery to the harness.
	// Node count is unknown at this point (planning hasn't started), so pass 0.
	ch := startSubagentChannel(req, mcpServer, taskID, 0)
	if ch != nil {
		defer ch.Close()
		// v2: Register channel for bidirectional tool dispatch.
		// ChannelToolHook will intercept client-tool nodes and dispatch via sampling.
		channel.GlobalChannelToolHook.RegisterChannel(taskID, ch)
		defer channel.GlobalChannelToolHook.UnregisterChannel(taskID)
	}

	type execResult struct {
		nodes []memory.NodeState
		err   error
	}

	doneChan := make(chan execResult, 1)

	// Execute task in a background goroutine to allow fallback to async mode if it times out
	go func() {
		_, _, err := task.Execute(context.Background(), args.Prompt, task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "workflow",
			IsForeground: true,
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
		// Timeout hit: check if the graph was persisted to SQLite by querying node_states.
		// If nodes exist, the executor is running and the task ID is safe to return.
		// If no nodes exist, planning is still in progress or failed — give a brief grace period.
		nodes := memory.DB.GetAllNodeStates(taskID)
		if len(nodes) == 0 {
			// Brief grace: wait up to 5 more seconds for planning to complete
			graceTimer := time.After(5 * time.Second)
			graceTicker := time.NewTicker(500 * time.Millisecond)
			defer graceTicker.Stop()

		graceLoop:
			for {
				select {
				case res := <-doneChan:
					// Task finished during grace period
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

				case <-graceTicker.C:
					nodes = memory.DB.GetAllNodeStates(taskID)
					if len(nodes) > 0 {
						break graceLoop
					}

				case <-graceTimer:
					// Grace period exhausted — return "planning" status
					respMap := map[string]interface{}{
						"taskId": taskID,
						"status": "planning",
						"message": "Task is still being planned. The graph has not been compiled yet. " +
							"Check tzro_status after a delay — the task may not appear until planning completes.",
					}
					respBytes, _ := json.MarshalIndent(respMap, "", "  ")
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: string(respBytes)},
						},
					}, nil, nil
				}
			}
		}

		// Nodes exist — graph is persisted and execution is in progress
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
	if strings.TrimSpace(args.TaskID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

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

	// Check if there are unread client-side tool requests for this task
	if taskStatus == "pending" || taskStatus == "running" {
		notifs, err := memory.DB.GetNotifications("unread")
		if err == nil {
			for _, n := range notifs {
				if n.TaskID == args.TaskID && n.Source == "client_tool" && n.Type == "client_tool_request" {
					taskStatus = "waiting_for_client"
					break
				}
			}
		}
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
	configPath := config.ResolvePath(filepath.Join(".tzro", "mcp_config.json"))

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
	if strings.TrimSpace(args.Query) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "query cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

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
	if strings.TrimSpace(args.Content) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "content cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}
	validTypes := map[string]bool{
		"fact":         true,
		"preference":   true,
		"insight":      true,
		"correction":   true,
		"anti_pattern": true,
		"strategy":     true,
	}
	if !validTypes[args.Type] {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "invalid memory type: must be one of fact, preference, insight, correction, anti_pattern, strategy"}`},
			},
			IsError: true,
		}, nil, nil
	}

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
		if strings.TrimSpace(args.Node.ID) == "" || strings.TrimSpace(args.Node.NodeType) == "" || strings.TrimSpace(args.Node.Name) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "node requires non-empty id, nodeType, and name"}`},
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
		if strings.TrimSpace(args.Edge.ID) == "" || strings.TrimSpace(args.Edge.EdgeType) == "" || strings.TrimSpace(args.Edge.SourceID) == "" || strings.TrimSpace(args.Edge.TargetID) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "edge requires non-empty id, edgeType, sourceId, and targetId"}`},
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
	if strings.TrimSpace(args.ID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "id cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

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
	if strings.TrimSpace(args.Prompt) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "prompt cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

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
	if strings.TrimSpace(args.Name) == "" || strings.TrimSpace(args.TriggerDescription) == "" || strings.TrimSpace(args.SOPContent) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "name, triggerDescription, and sopContent are required and cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

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
	if strings.TrimSpace(args.TaskID) == "" || strings.TrimSpace(args.NodeID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId and nodeId are required"}`},
			},
			IsError: true,
		}, nil, nil
	}

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
	if strings.TrimSpace(args.TaskID) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "taskId cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

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

// TzroCompletionArgs defines inputs for tzro_completion.
type TzroCompletionArgs struct {
	SystemPrompt string  `json:"systemPrompt" jsonschema:"required,System prompt to guide the local model behavior"`
	UserPrompt   string  `json:"userPrompt" jsonschema:"required,The user-facing prompt or content to process"`
	JsonSchema   string  `json:"jsonSchema,omitempty" jsonschema:"Optional JSON schema to constrain output via GBNF grammar. When provided the model output is guaranteed valid JSON matching this schema."`
	MaxTokens    int     `json:"maxTokens,omitempty" jsonschema:"Maximum tokens to generate. Default 2048"`
	Temperature  float64 `json:"temperature,omitempty" jsonschema:"Sampling temperature. Default 1.0"`
}

func handleTzroCompletion(ctx context.Context, req *mcp.CallToolRequest, args TzroCompletionArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.UserPrompt) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "userPrompt cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Resolve the active inference backend
	backend := inference.ActiveBackend
	if backend == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "no inference backend configured"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Auto-start if stopped
	if strings.ToLower(backend.Status()) == "stopped" {
		if err := backend.Start(ctx); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "local model failed to start: %s"}`, err.Error())},
				},
				IsError: true,
			}, nil, nil
		}
	}

	result, err := backend.CallModel(ctx, []inference.InferenceMessage{{Role: "system", Content: args.SystemPrompt}, {Role: "user", Content: args.UserPrompt}}, args.JsonSchema)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "local model inference failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	respMap := map[string]interface{}{
		"content":          result.Content,
		"promptTokens":     result.PromptTokens,
		"completionTokens": result.CompletionTokens,
		"durationSeconds":  result.DurationSeconds,
		"tokensPerSecond":  result.TokensPerSecond,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroClassificationArgs defines inputs for tzro_classification.
type TzroClassificationArgs struct {
	Input      string   `json:"input" jsonschema:"required,The text content to classify"`
	Categories []string `json:"categories" jsonschema:"required,The set of valid classification labels"`
	Context    string   `json:"context,omitempty" jsonschema:"Optional context or instructions to guide classification"`
}

func handleTzroClassification(ctx context.Context, req *mcp.CallToolRequest, args TzroClassificationArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Input) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "input cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	if len(args.Categories) < 2 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "at least 2 categories are required"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Resolve the active inference backend
	backend := inference.ActiveBackend
	if backend == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "no inference backend configured"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Auto-start if stopped
	if strings.ToLower(backend.Status()) == "stopped" {
		if err := backend.Start(ctx); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "local model failed to start: %s"}`, err.Error())},
				},
				IsError: true,
			}, nil, nil
		}
	}

	// Build classification system prompt
	systemPrompt := "You are a classification agent. Classify the input into exactly one of the provided categories. Respond with ONLY valid JSON matching the schema."
	if args.Context != "" {
		systemPrompt += "\n\nAdditional context: " + args.Context
	}

	// Build user prompt with categories listed
	userPrompt := fmt.Sprintf("Classify this input:\n\n%s\n\nValid categories: %s", args.Input, strings.Join(args.Categories, ", "))

	// Build JSON schema with enum constraint — GBNF guarantees the output is one of the valid labels
	categoriesJSON, _ := json.Marshal(args.Categories)
	jsonSchema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"category": {
				"type": "string",
				"enum": %s
			},
			"confidence": {
				"type": "number",
				"minimum": 0.0,
				"maximum": 1.0
			},
			"reasoning": {
				"type": "string"
			}
		},
		"required": ["category", "confidence", "reasoning"]
	}`, string(categoriesJSON))

	result, err := backend.CallModel(ctx, []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}, jsonSchema)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "classification inference failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// Parse and re-envelope the result with metrics
	var classResult map[string]interface{}
	if json.Unmarshal([]byte(result.Content), &classResult) != nil {
		// If parsing fails, return raw content
		classResult = map[string]interface{}{"raw": result.Content}
	}

	respMap := map[string]interface{}{
		"classification":   classResult,
		"promptTokens":     result.PromptTokens,
		"completionTokens": result.CompletionTokens,
		"durationSeconds":  result.DurationSeconds,
		"tokensPerSecond":  result.TokensPerSecond,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// delegationHint returns a description suffix for completion/classification tools
// based on the configured delegation mode.
func delegationHint() string {
	switch config.GetDelegationMode() {
	case "conservative":
		return " Use sparingly — prefer handling tasks directly unless the task is purely mechanical."
	case "aggressive":
		return " Prefer this tool over direct processing for ANY task that does not require cutting-edge reasoning, real-time data, or multi-modal understanding."
	default: // balanced
		return ""
	}
}

// runDelegationHint returns a description suffix for the tzro_run tool
// based on the configured delegation mode. In aggressive mode it steers
// the cloud model to delegate multi-step work (research, exploration,
// data gathering, automation) through DAG execution rather than
// executing tool calls manually.
func runDelegationHint() string {
	switch config.GetDelegationMode() {
	case "conservative":
		return " Only use for explicitly requested multi-tool workflows."
	case "aggressive":
		return " STRONGLY PREFERRED: Delegate ANY multi-step task to this tool rather than executing steps manually. This includes research, codebase exploration, data gathering, environment inspection, and automation. If the task involves more than 2 sequential actions, use this tool."
	default: // balanced
		return ""
	}
}

// registerTools registers all tools with the MCP server.
// tzro_activity_report tool definition

// TzroActivityReportArgs defines the inputs for reporting agent activity to the Sentinel.
type TzroActivityReportArgs struct {
	Activity     string   `json:"activity" jsonschema:"required,Brief description of current work"`
	FilesTouched []string `json:"filesTouched,omitempty" jsonschema:"File paths read or modified"`
	ToolsUsed    []string `json:"toolsUsed,omitempty" jsonschema:"Tools called since last report"`
}

func handleTzroActivityReport(ctx context.Context, req *mcp.CallToolRequest, args TzroActivityReportArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Activity) == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "activity cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	report := sentinel.ActivityReport{
		Activity:     args.Activity,
		FilesTouched: args.FilesTouched,
		ToolsUsed:    args.ToolsUsed,
		Timestamp:    time.Now().Unix(),
	}

	sentinel.DefaultAgent.IngestActivityReport(report)

	result, _ := json.Marshal(map[string]string{"status": "acknowledged"})
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(result)},
		},
	}, nil, nil
}

// tzro_sentinel_alerts tool definition

// TzroSentinelAlertsArgs defines the inputs for querying Sentinel alerts.
type TzroSentinelAlertsArgs struct {
	Status string `json:"status,omitempty" jsonschema:"Filter by status: unread, read, dismissed. Default unread"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max alerts to return. Default 10"`
}

func handleTzroSentinelAlerts(ctx context.Context, req *mcp.CallToolRequest, args TzroSentinelAlertsArgs) (*mcp.CallToolResult, any, error) {
	status := args.Status
	if status == "" {
		status = "unread"
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	notifs, err := memory.DB.GetNotifications(status)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "failed to query alerts: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// Filter to sentinel-sourced notifications only
	var sentinelAlerts []memory.DurableNotification
	for _, n := range notifs {
		if n.Source == "sentinel" {
			sentinelAlerts = append(sentinelAlerts, n)
			if len(sentinelAlerts) >= limit {
				break
			}
		}
	}

	// Mark returned unread alerts as read
	if status == "unread" {
		for _, n := range sentinelAlerts {
			_ = memory.DB.UpdateNotificationStatus(n.ID, "read")
		}
	}

	if sentinelAlerts == nil {
		sentinelAlerts = []memory.DurableNotification{}
	}

	result, _ := json.Marshal(map[string]interface{}{
		"alerts": sentinelAlerts,
		"count":  len(sentinelAlerts),
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(result)},
		},
	}, nil, nil
}

// tzro_sentinel_wake tool definition

// TzroSentinelWakeArgs defines the inputs for manually waking the Sentinel Agent.
type TzroSentinelWakeArgs struct {
	ContextHint string `json:"contextHint,omitempty" jsonschema:"Optional hint to bias Sentinel analysis toward a specific topic, e.g. 'check for security issues in auth module'"`
}

func handleTzroSentinelWake(ctx context.Context, req *mcp.CallToolRequest, args TzroSentinelWakeArgs) (*mcp.CallToolResult, any, error) {
	alerted := sentinel.DefaultAgent.Wake(ctx, args.ContextHint)

	respMap := map[string]interface{}{
		"status":        "completed",
		"alertProduced": alerted,
	}
	if args.ContextHint != "" {
		respMap["contextHint"] = args.ContextHint
	}

	result, _ := json.Marshal(respMap)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(result)},
		},
	}, nil, nil
}

// tzro_workflow tool definition

// WorkflowNodeInput defines a single node in a user-specified DAG workflow.
type WorkflowNodeInput struct {
	ID                  string             `json:"id" jsonschema:"required,Unique node identifier"`
	Type                string             `json:"type" jsonschema:"required,Node type: action, probe, deterministic, branch, merge, synthesis, hypothesis"`
	Action              string             `json:"action,omitempty" jsonschema:"Target tool name for action/deterministic nodes"`
	Instructions        string             `json:"instructions" jsonschema:"required,Natural language step instructions. Use double-braces variable binding to reference upstream node outputs."`
	AllowedTools        []string           `json:"allowedTools,omitempty" jsonschema:"Whitelist of permitted tools for this node"`
	StaticArgs          string             `json:"staticArgs,omitempty" jsonschema:"Pre-known arguments as a JSON string"`
	RequireApproval     bool               `json:"requireApproval,omitempty" jsonschema:"Pause and wait for human approval before executing"`
	ActivationThreshold float64            `json:"activationThreshold,omitempty" jsonschema:"Sufficiency gate 0.0-1.0. 0.0 disables Edge Thoughts."`
	ProbeConfig         *WorkflowProbeInput `json:"probeConfig,omitempty" jsonschema:"Configuration for probe nodes. Required when type is probe."`
}

// WorkflowEdgeInput defines a directed edge between two nodes.
type WorkflowEdgeInput struct {
	SourceID string `json:"sourceId" jsonschema:"required,Source node ID"`
	TargetID string `json:"targetId" jsonschema:"required,Target node ID"`
}

// WorkflowProbeInput configures a Probe Node's Thought Chain execution loop.
type WorkflowProbeInput struct {
	Goal         string   `json:"goal" jsonschema:"required,The exploration objective"`
	AllowedTools []string `json:"allowedTools" jsonschema:"required,Tools the probe may use"`
	StepBudget   int      `json:"stepBudget,omitempty" jsonschema:"Maximum number of Thought Chain steps before forced synthesis. Default 20"`
	CompactEvery int      `json:"compactEvery,omitempty" jsonschema:"Rolling compaction frequency in steps. Default 3"`
}

// TzroWorkflowArgs defines the inputs for creating and executing a DAG workflow directly.
type TzroWorkflowArgs struct {
	Nodes          []WorkflowNodeInput `json:"nodes" jsonschema:"required,Array of DAG nodes defining the workflow steps"`
	Edges          []WorkflowEdgeInput `json:"edges,omitempty" jsonschema:"Array of directed edges defining node dependencies"`
	MutationBudget int                 `json:"mutationBudget,omitempty" jsonschema:"Max dynamic node spawns for activation thresholds. Default 0 (disabled)"`
	MaxCycles      int                 `json:"maxCycles,omitempty" jsonschema:"Max execution cycles. Default 5"`
	Timeout        int                 `json:"timeout,omitempty" jsonschema:"Execution timeout in seconds before switching to async. Default 60"`
	DryRun         bool                `json:"dryRun,omitempty" jsonschema:"If true validates and compiles the graph without executing. Returns execution levels."`
}

func handleTzroWorkflow(ctx context.Context, req *mcp.CallToolRequest, args TzroWorkflowArgs) (*mcp.CallToolResult, any, error) {
	// --- Validation ---
	if len(args.Nodes) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "nodes array cannot be empty"}`},
			},
			IsError: true,
		}, nil, nil
	}

	// Check for unique node IDs and build lookup set
	nodeIDs := make(map[string]bool, len(args.Nodes))
	for _, n := range args.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "all nodes must have a non-empty id"}`},
				},
				IsError: true,
			}, nil, nil
		}
		if nodeIDs[n.ID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "duplicate node id: %s"}`, n.ID)},
				},
				IsError: true,
			}, nil, nil
		}
		nodeIDs[n.ID] = true

		// Validate probe nodes have probeConfig
		if n.Type == "probe" && n.ProbeConfig == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "probe node '%s' requires a probeConfig"}`, n.ID)},
				},
				IsError: true,
			}, nil, nil
		}
	}

	// Validate edge references
	for _, e := range args.Edges {
		if !nodeIDs[e.SourceID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "edge references non-existent source node: %s"}`, e.SourceID)},
				},
				IsError: true,
			}, nil, nil
		}
		if !nodeIDs[e.TargetID] {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "edge references non-existent target node: %s"}`, e.TargetID)},
				},
				IsError: true,
			}, nil, nil
		}
	}

	// --- Map to compiler types ---
	graphNodes := make([]compiler.GraphNode, 0, len(args.Nodes))
	for _, n := range args.Nodes {
		gn := compiler.GraphNode{
			ID:                  n.ID,
			Type:                n.Type,
			Action:              n.Action,
			Instructions:        n.Instructions,
			AllowedTools:        n.AllowedTools,
			StaticArgs:          n.StaticArgs,
			RequireApproval:     n.RequireApproval,
			ActivationThreshold: n.ActivationThreshold,
			Status:              "pending",
		}
		if n.ProbeConfig != nil {
			stepBudget := n.ProbeConfig.StepBudget
			if stepBudget <= 0 {
				stepBudget = 20
			}
			compactEvery := n.ProbeConfig.CompactEvery
			if compactEvery <= 0 {
				compactEvery = 3
			}
			gn.ProbeConfig = &compiler.ProbeConfig{
				Goal:         n.ProbeConfig.Goal,
				AllowedTools: n.ProbeConfig.AllowedTools,
				StepBudget:   stepBudget,
				CompactEvery: compactEvery,
			}
		}
		graphNodes = append(graphNodes, gn)
	}

	graphEdges := make([]compiler.GraphEdge, 0, len(args.Edges))
	for _, e := range args.Edges {
		graphEdges = append(graphEdges, compiler.GraphEdge{
			SourceID: e.SourceID,
			TargetID: e.TargetID,
		})
	}

	taskID := uuid.New().String()
	maxCycles := args.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 5
	}

	graph := &compiler.ExecutionGraph{
		TaskID:    taskID,
		Nodes:     graphNodes,
		Edges:     graphEdges,
		MaxCycles: maxCycles,
		CreatedAt: time.Now().Unix(),
	}

	// Set mutation budget if provided
	if args.MutationBudget > 0 {
		graph.MutationBudget = &compiler.MutationBudget{
			MaxSpawns:       args.MutationBudget,
			RemainingSpawns: args.MutationBudget,
		}
	}

	// --- SCT Expansion ---
	expanded, err := compiler.ExpandToSCTGraph(graph, tools.GetSchema)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "SCT expansion failed: %s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// --- Kahn Compile ---
	levels, err := compiler.CompileAndSort(expanded)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "%s"}`, err.Error())},
			},
			IsError: true,
		}, nil, nil
	}

	// --- Dry Run ---
	if args.DryRun {
		respMap := map[string]interface{}{
			"taskId":          taskID,
			"status":          "dry_run",
			"executionLevels": levels,
			"nodeCount":       len(expanded.Nodes),
			"edgeCount":       len(expanded.Edges),
		}
		respBytes, _ := json.MarshalIndent(respMap, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
		}, nil, nil
	}

	// --- Execute with timeout/async fallback (same pattern as tzro_run) ---
	timeoutSec := args.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	// Start SubagentChannel for real-time event delivery to the harness.
	// Node count is known from the compiled graph.
	wfCh := startSubagentChannel(req, mcpServer, taskID, float64(len(expanded.Nodes)))
	if wfCh != nil {
		defer wfCh.Close()
		channel.GlobalChannelToolHook.RegisterChannel(taskID, wfCh)
		defer channel.GlobalChannelToolHook.UnregisterChannel(taskID)
	}

	type execResult struct {
		nodes []memory.NodeState
		err   error
	}

	doneChan := make(chan execResult, 1)

	go func() {
		execErr := executor.GlobalEngine.ExecuteGraph(context.Background(), expanded, levels)
		nodes := memory.DB.GetAllNodeStates(taskID)
		doneChan <- execResult{nodes: nodes, err: execErr}
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
		// Timeout hit: check if nodes were persisted
		nodes := memory.DB.GetAllNodeStates(taskID)
		if len(nodes) == 0 {
			// Brief grace: wait up to 5 more seconds
			graceTimer := time.After(5 * time.Second)
			graceTicker := time.NewTicker(500 * time.Millisecond)
			defer graceTicker.Stop()

		graceLoop:
			for {
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

				case <-graceTicker.C:
					nodes = memory.DB.GetAllNodeStates(taskID)
					if len(nodes) > 0 {
						break graceLoop
					}

				case <-graceTimer:
					respMap := map[string]interface{}{
						"taskId":  taskID,
						"status":  "compiling",
						"message": "Workflow graph is still being compiled. Check tzro_status after a delay.",
					}
					respBytes, _ := json.MarshalIndent(respMap, "", "  ")
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: string(respBytes)},
						},
					}, nil, nil
				}
			}
		}

		// Nodes exist — execution is in progress
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

// tzro_dashboard tool definition

type TzroDashboardArgs struct{}

func handleTzroDashboard(ctx context.Context, req *mcp.CallToolRequest, args TzroDashboardArgs) (*mcp.CallToolResult, any, error) {
	spec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		respBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
			IsError: true,
		}, nil, nil
	}

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}
	url := fmt.Sprintf("http://localhost:%s/dashboard/", port)

	if spec == nil {
		// Trigger initial generation in the background
		taskID := uuid.New().String()
		go func() {
			_, _, _ = task.Execute(context.Background(), "Generate system dashboard spec", task.ExecuteOptions{
				TaskID:       taskID,
				IntentType:   "workflow",
				IsForeground: false,
			})
		}()

		respBytes, _ := json.Marshal(map[string]interface{}{
			"url":     url,
			"status":  "generating",
			"message": "No dashboard spec found. Triggered initial generation. Please check back shortly.",
			"taskId":  taskID,
		})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
		}, nil, nil
	}

	ageSeconds := time.Now().Unix() - spec.GeneratedAt
	respBytes, _ := json.Marshal(map[string]interface{}{
		"url":             url,
		"status":          "active",
		"specId":          spec.ID,
		"generatedAt":     spec.GeneratedAt,
		"ageSeconds":      ageSeconds,
		"generatorTaskId": spec.GeneratorTaskID,
		"ttlSeconds":      spec.TTLSeconds,
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

// tzro_dashboard_regenerate tool definition

type TzroDashboardRegenerateArgs struct {
	Wait bool `json:"wait,omitempty" jsonschema:"Whether to block and wait for the generation to complete"`
}

func handleTzroDashboardRegenerate(ctx context.Context, req *mcp.CallToolRequest, args TzroDashboardRegenerateArgs) (*mcp.CallToolResult, any, error) {
	taskID := uuid.New().String()
	prompt := "Generate system dashboard spec"

	if args.Wait {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		graph, _, err := task.Execute(timeoutCtx, prompt, task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "workflow",
			IsForeground: true,
		})
		if err != nil {
			if timeoutCtx.Err() == context.DeadlineExceeded {
				respBytes, _ := json.Marshal(map[string]interface{}{
					"status": "generating",
					"taskId": taskID,
				})
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
				}, nil, nil
			}
			respBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
				IsError: true,
			}, nil, nil
		}

		nodeSucceeded := false
		for _, node := range graph.Nodes {
			if node.Action == "terminal_synthesis" {
				state, ok := memory.DB.GetNodeState(taskID, node.ID)
				if ok && state.Status == "completed" {
					nodeSucceeded = true
				}
			}
		}

		status := "failed"
		if nodeSucceeded {
			status = "completed"
		}

		respBytes, _ := json.Marshal(map[string]interface{}{
			"status": status,
			"taskId": taskID,
		})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
		}, nil, nil
	}

	go func() {
		_, _, _ = task.Execute(context.Background(), prompt, task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "workflow",
			IsForeground: false,
		})
	}()

	respBytes, _ := json.Marshal(map[string]interface{}{
		"status": "generating",
		"taskId": taskID,
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
	}, nil, nil
}

// tzro_dashboard_spec tool definition

type TzroDashboardSpecArgs struct{}

func handleTzroDashboardSpec(ctx context.Context, req *mcp.CallToolRequest, args TzroDashboardSpecArgs) (*mcp.CallToolResult, any, error) {
	spec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		respBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
			IsError: true,
		}, nil, nil
	}

	if spec == nil {
		respBytes, _ := json.Marshal(map[string]string{"error": "no dashboard spec found"})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(respBytes)}},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: spec.Spec}},
	}, nil, nil
}

func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_run",
		Description: "Plan, compile, and execute a durable DAG workflow from a natural language prompt." + runDelegationHint(),
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
		Name:        "tzro_web_search",
		Description: "Execute a multi-engine web search using tiered fallback (Startpage, Brave, Bing, DuckDuckGo). Returns ranked results with titles, URLs, and snippets.",
	}, handleTzroWebSearch)

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

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_model_list",
		Description: "List available GGUF models from the catalog with download status, active model indicator, and local file paths.",
	}, handleTzroModelList)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_model_set",
		Description: "Change the active local worker model. Accepts a catalog modelId, a direct ggufModelPath, or a downloadUrl. Stops the current sidecar, cleans up the old managed model file, updates config, and restarts with the new model.",
	}, handleTzroModelSet)

	// Local model delegation tools — enable cloud-to-local cost arbitrage
	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_completion",
		Description: "Run a prompt through the local on-device LLM for structured text generation. " +
			"Use this for tasks that don't require frontier-model reasoning: " +
			"summarization, extraction, reformatting, translation, boilerplate generation, " +
			"and any task where output structure matters more than world knowledge. " +
			"Supports optional JSON schema constraint (GBNF grammar) for guaranteed-valid structured output. " +
			"Zero cost, zero latency to external APIs, fully private." + delegationHint(),
	}, handleTzroCompletion)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_classification",
		Description: "Classify arbitrary text into one of a fixed set of categories using the local on-device LLM " +
			"with grammar-constrained output (GBNF). The model is forced to output exactly one of the provided labels — " +
			"no hallucination possible. Use for sentiment analysis, intent routing, priority triage, " +
			"content categorization, or any multi-class classification task. Zero cost, fully private." + delegationHint(),
	}, handleTzroClassification)

	// Sentinel Agent tools (ADR-0023)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_activity_report",
		Description: "Report current agent activity to the Sentinel for proactive context analysis. Called periodically by the cloud agent to enable richer proactive assistance.",
	}, handleTzroActivityReport)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_sentinel_alerts",
		Description: "Retrieve proactive insight alerts generated by the Sentinel Agent. Returns alerts filtered by status (default: unread). Marks returned unread alerts as read.",
	}, handleTzroSentinelAlerts)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_sentinel_wake",
		Description: "Manually trigger the Sentinel Agent's retrieval-grounded synthesis pipeline outside its normal heartbeat cadence. Use when you want an immediate proactive analysis — e.g., after completing a major code change, before a commit, or when the user explicitly asks for a Sentinel check. Accepts an optional contextHint to bias the analysis toward a specific topic.",
	}, handleTzroSentinelWake)

	// Direct workflow creation tool
	mcp.AddTool(server, &mcp.Tool{
		Name: "tzro_workflow",
		Description: "Create and execute a tzro DAG workflow by directly specifying nodes, edges, " +
			"and execution parameters. Bypasses the LLM Strategic Planner — use when you have a " +
			"pre-defined workflow structure. The graph is SCT-expanded (action nodes decomposed into " +
			"bridge/exec pairs) and Kahn-sorted before execution. Supports dry-run validation, " +
			"probe nodes, activation thresholds, mutation budgets, and human-in-the-loop approval gates.",
	}, handleTzroWorkflow)

	// Dashboard tools
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_dashboard",
		Description: "Check spec status and return the HTTP dashboard URL, age, and status. Triggers initial generation if no spec exists.",
	}, handleTzroDashboard)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_dashboard_regenerate",
		Description: "Trigger immediate generation of the system dashboard spec, supporting optional wait blocking parameters.",
	}, handleTzroDashboardRegenerate)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tzro_dashboard_spec",
		Description: "Return the current raw system dashboard spec JSON for debugging.",
	}, handleTzroDashboardSpec)
}
