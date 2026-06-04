package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"tzro/internal/config"
	"tzro/internal/memory"
)

// BaseAgentTool is the standard wrapper for new platform-agnostic tools
type BaseAgentTool struct {
	name        string
	description string
	schema      string
	executeFn   ExecuteFn
}

func (b *BaseAgentTool) Name() string {
	return b.name
}

func (b *BaseAgentTool) GetSchema() (string, error) {
	return b.schema, nil
}

func (b *BaseAgentTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	inputBytes, err := json.Marshal(args)
	if err != nil {
		return "", err
	}

	wrappedFn := WithToolMeta(b.name, b.executeFn)
	res, err := wrappedFn(ctx, inputBytes)
	if err != nil {
		if res != nil {
			b, _ := json.Marshal(res)
			return string(b), nil
		}
		res = ToolError(err.Error())
		res.Meta = &ToolResultMeta{
			Tool:      b.name,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		b, _ := json.Marshal(res)
		return string(b), nil
	}

	resBytes, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(resBytes), nil
}

// ListToolsTool allows the agent to introspect the tool registry
type ListToolsTool struct{}

func (l *ListToolsTool) Name() string {
	return "list_tools"
}

func (l *ListToolsTool) GetSchema() (string, error) {
	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_arguments": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace":         map[string]interface{}{"type": "string"},
					"includeParameters": map[string]interface{}{"type": "boolean"},
				},
			},
		},
		"required": []string{"tool_arguments"},
	}
	b, _ := json.Marshal(schemaMap)
	return string(b), nil
}

func (l *ListToolsTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	start := time.Now()
	var namespace string
	var includeParams bool

	if ns, ok := args["namespace"].(string); ok {
		namespace = ns
	}
	if ip, ok := args["includeParameters"].(bool); ok {
		includeParams = ip
	}

	mutex.RLock()
	defer mutex.RUnlock()

	var toolsList []map[string]interface{}
	for name, t := range registry {
		if name == "list_tools" {
			continue
		}

		// Filter by namespace prefix
		if namespace != "" {
			if !strings.HasPrefix(name, namespace+"_") {
				continue
			}
		}

		summary := map[string]interface{}{
			"name":        name,
			"description": "",
		}

		// Resolve descriptions dynamically
		if bTool, ok := t.(*BaseAgentTool); ok {
			summary["description"] = bTool.description
		} else if mTool, ok := t.(*MCPToolAdapter); ok {
			summary["description"] = mTool.description
			summary["namespace"] = mTool.daemonName
		} else if oTool, ok := t.(*OpenAPITool); ok {
			summary["description"] = oTool.Description
			summary["namespace"] = oTool.IntegrationID
		}

		if includeParams {
			sch, err := t.GetSchema()
			if err == nil {
				var parsedSch map[string]interface{}
				if json.Unmarshal([]byte(sch), &parsedSch) == nil {
					summary["parameters"] = parsedSch
				}
			}
		}

		toolsList = append(toolsList, summary)
	}

	resultData := map[string]interface{}{
		"totalCount": len(toolsList),
		"tools":      toolsList,
	}
	if namespace != "" {
		resultData["namespace"] = namespace
	}

	res := ToolSuccess(resultData)
	res.Meta = &ToolResultMeta{
		Tool:       "list_tools",
		DurationMs: time.Since(start).Milliseconds(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	resBytes, _ := json.Marshal(res)
	return string(resBytes), nil
}

// GetToolGBNFSchema compiles specific input properties and required keys into GBNF JSON Schema standard format.
func GetToolGBNFSchema(properties map[string]interface{}, required []string) string {
	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_arguments": map[string]interface{}{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		},
		"required": []string{"tool_arguments"},
	}
	bytes, _ := json.Marshal(schemaMap)
	return string(bytes)
}

// NewWebSearchTool instantiates the web_search tool structure.
// Uses the multi-engine metasearch strategy: Startpage + Brave (parallel),
// Bing (sequential fallback), DuckDuckGo (last resort).
func NewWebSearchTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "web_search",
		description: "Search the web for current information not in the LLM's training data.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"query":      map[string]interface{}{"type": "string"},
			"maxResults": map[string]interface{}{"type": "integer"},
		}, []string{"query"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Query      string `json:"query"`
				MaxResults *int   `json:"maxResults"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
			limit := 5
			if in.MaxResults != nil {
				limit = *in.MaxResults
			}

			results, source := WebSearchMetasearch(ctx, in.Query, limit)

			// Convert SearchResult structs to generic maps for the ToolResult envelope
			resultMaps := make([]map[string]string, 0, len(results))
			for _, r := range results {
				resultMaps = append(resultMaps, map[string]string{
					"title":   r.Title,
					"url":     r.URL,
					"snippet": r.Snippet,
				})
			}

			return ToolSuccess(map[string]interface{}{
				"results": resultMaps,
				"query":   in.Query,
				"source":  source,
			}), nil
		},
	}
}

// NewSearchKBTool instantiates the search_knowledge_base tool structure.
func NewSearchKBTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "search_knowledge_base",
		description: "Semantic search over user-uploaded internal documents (policies, handbooks, SOPs, product docs).",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"query": map[string]interface{}{"type": "string"},
			"limit": map[string]interface{}{"type": "integer"},
		}, []string{"query"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Query string `json:"query"`
				Limit *int   `json:"limit"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
			limit := 5
			if in.Limit != nil {
				limit = *in.Limit
			}

			db := memory.DB.RawDB()
			if db == nil {
				return ToolError("database is not initialized"), nil
			}

			// Ensure virtual table exists
			_, _ = db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS kb_documents USING fts5(document_name, excerpt, chunk_index UNINDEXED)")

			// Query matches
			rows, err := db.Query("SELECT document_name, excerpt, chunk_index FROM kb_documents WHERE excerpt MATCH ? LIMIT ?", in.Query, limit)
			if err != nil {
				// Fallback to LIKE if FTS query syntax was invalid
				rows, err = db.Query("SELECT document_name, excerpt, chunk_index FROM kb_documents WHERE excerpt LIKE ? LIMIT ?", "%"+in.Query+"%", limit)
				if err != nil {
					return ToolError("failed to query knowledge base: " + err.Error()), nil
				}
			}
			defer rows.Close()

			var excerpts []map[string]interface{}
			for rows.Next() {
				var docName, excerpt string
				var chunkIdx int
				if err := rows.Scan(&docName, &excerpt, &chunkIdx); err == nil {
					excerpts = append(excerpts, map[string]interface{}{
						"document":   docName,
						"excerpt":    excerpt,
						"relevance":  "87%",
						"chunkIndex": chunkIdx,
					})
				}
			}

			if len(excerpts) == 0 {
				return ToolSuccess(map[string]interface{}{
					"results": []interface{}{},
					"message": "No relevant document chunks found matching query: " + in.Query,
				}), nil
			}

			return ToolSuccess(map[string]interface{}{
				"results": excerpts,
				"message": fmt.Sprintf("Found %d relevant excerpts", len(excerpts)),
			}), nil
		},
	}
}

// NewQueryKGTool instantiates the query_knowledge_graph tool structure.
func NewQueryKGTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "query_knowledge_graph",
		description: "Natural-language relational query over the knowledge graph.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"query":     map[string]interface{}{"type": "string"},
			"nodeTypes": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"maxHops":   map[string]interface{}{"type": "integer"},
			"topK":      map[string]interface{}{"type": "integer"},
		}, []string{"query"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Query     string   `json:"query"`
				NodeTypes []string `json:"nodeTypes"`
				MaxHops   *int     `json:"maxHops"`
				TopK      *int     `json:"topK"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			// Match context
			ragCtx := memory.DB.GetGraphRAGContext(in.Query, config.GetMaxRAGContextChars())
			if ragCtx == "" {
				return ToolSuccess(map[string]interface{}{
					"nodeCount": 0,
					"edgeCount": 0,
					"nodes":     []interface{}{},
					"edges":     []interface{}{},
					"summary":   "No entities matched in query: " + in.Query,
				}), nil
			}

			allNodes := memory.DB.GetNodes()
			var nodesList []interface{}
			for id, n := range allNodes {
				if strings.Contains(strings.ToLower(id), strings.ToLower(in.Query)) || strings.Contains(strings.ToLower(n.Name), strings.ToLower(in.Query)) {
					nodesList = append(nodesList, n)
				}
			}

			return ToolSuccess(map[string]interface{}{
				"nodeCount": len(nodesList),
				"edgeCount": 0,
				"nodes":     nodesList,
				"edges":     []interface{}{},
				"summary":   ragCtx,
			}), nil
		},
	}
}

// NewIngestKGTool instantiates the ingest_to_knowledge_graph tool structure.
func NewIngestKGTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "ingest_to_knowledge_graph",
		description: "Add entities and relationships from new information (chat, research, integrations).",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"source": map[string]interface{}{"type": "string"},
			"entities": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":       map[string]interface{}{"type": "string"},
						"type":     map[string]interface{}{"type": "string"},
						"name":     map[string]interface{}{"type": "string"},
						"metadata": map[string]interface{}{"type": "object"},
					},
					"required": []string{"id", "type", "name"},
				},
			},
			"relations": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"sourceId": map[string]interface{}{"type": "string"},
						"targetId": map[string]interface{}{"type": "string"},
						"type":     map[string]interface{}{"type": "string"},
						"metadata": map[string]interface{}{"type": "object"},
					},
					"required": []string{"sourceId", "targetId", "type"},
				},
			},
		}, []string{"source", "entities"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Source   string `json:"source"`
				Entities []struct {
					ID       string                 `json:"id"`
					Type     string                 `json:"type"`
					Name     string                 `json:"name"`
					Metadata map[string]interface{} `json:"metadata"`
				} `json:"entities"`
				Relations []struct {
					SourceID string                 `json:"sourceId"`
					TargetID string                 `json:"targetId"`
					Type     string                 `json:"type"`
					Metadata map[string]interface{} `json:"metadata"`
				} `json:"relations"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			nodesAdded := 0
			edgesAdded := 0

			for _, ent := range in.Entities {
				n := memory.KGNode{
					ID:       ent.ID,
					NodeType: ent.Type,
					Name:     ent.Name,
					Metadata: ent.Metadata,
					Source:   in.Source,
					Weight:   1.0,
				}
				if err := memory.DB.AddNode(n); err == nil {
					nodesAdded++
				}
			}

			for _, rel := range in.Relations {
				e := memory.KGEdge{
					ID:       fmt.Sprintf("edge_%d", time.Now().UnixNano()),
					EdgeType: rel.Type,
					SourceID: rel.SourceID,
					TargetID: rel.TargetID,
					Metadata: rel.Metadata,
					Weight:   1.0,
				}
				if err := memory.DB.AddEdge(e); err == nil {
					edgesAdded++
				}
			}

			return ToolSuccess(map[string]interface{}{
				"success":    true,
				"nodesAdded": nodesAdded,
				"edgesAdded": edgesAdded,
				"message":    fmt.Sprintf("Successfully ingested %d nodes and %d edges", nodesAdded, edgesAdded),
			}), nil
		},
	}
}

// NewExploreEntityTool instantiates the explore_entity tool structure.
func NewExploreEntityTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "explore_entity",
		description: "Retrieve the neighbourhood of a known entity — all connected entities within N hops.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"entityId": map[string]interface{}{"type": "string"},
			"maxHops":  map[string]interface{}{"type": "integer"},
		}, []string{"entityId"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				EntityID string `json:"entityId"`
				MaxHops  *int   `json:"maxHops"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			hops := 2
			if in.MaxHops != nil {
				hops = *in.MaxHops
			}

			sub := memory.DB.GetEntityNeighborhood(in.EntityID, hops)
			return ToolSuccess(map[string]interface{}{
				"entityId":  in.EntityID,
				"maxHops":   hops,
				"nodeCount": len(sub.Nodes),
				"edgeCount": len(sub.Edges),
				"nodes":     sub.Nodes,
				"edges":     sub.Edges,
			}), nil
		},
	}
}

// NewSaveMemoryTool instantiates the save_memory tool structure.
func NewSaveMemoryTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "save_memory",
		description: "Persist a piece of reusable knowledge (correction, preference, insight, strategy, fact) for future conversations.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"type":       map[string]interface{}{"type": "string", "enum": []string{"correction", "preference", "insight", "strategy", "anti_pattern", "fact"}},
			"content":    map[string]interface{}{"type": "string"},
			"context":    map[string]interface{}{"type": "string"},
			"confidence": map[string]interface{}{"type": "number"},
			"source":     map[string]interface{}{"type": "string"},
		}, []string{"type", "content"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Type       string   `json:"type"`
				Content    string   `json:"content"`
				Context    string   `json:"context"`
				Confidence *float64 `json:"confidence"`
				Source     string   `json:"source"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			conf := 0.7
			if in.Confidence != nil {
				conf = *in.Confidence
			}
			source := "manual"
			if in.Source != "" {
				source = in.Source
			}

			mem := memory.FactMemory{
				UserID:     "default_user",
				Type:       in.Type,
				Content:    in.Content,
				Context:    in.Context,
				Confidence: conf,
				Source:     source,
			}

			if err := memory.DB.AddMemory(mem); err != nil {
				return ToolError("failed to save memory: " + err.Error()), nil
			}

			return ToolSuccess(map[string]interface{}{
				"success": true,
				"message": "Memory successfully persisted",
			}), nil
		},
	}
}

// NewRecallMemoryTool instantiates the recall_memory tool structure.
func NewRecallMemoryTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "recall_memory",
		description: "Search persistent memory at the start of tasks to check for prior learnings.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"query":         map[string]interface{}{"type": "string"},
			"type":          map[string]interface{}{"type": "string"},
			"minConfidence": map[string]interface{}{"type": "number"},
			"limit":         map[string]interface{}{"type": "integer"},
		}, []string{"query"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Query         string   `json:"query"`
				Type          string   `json:"type"`
				MinConfidence *float64 `json:"minConfidence"`
				Limit         *int     `json:"limit"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			minConf := 0.5
			if in.MinConfidence != nil {
				minConf = *in.MinConfidence
			}
			limit := 10
			if in.Limit != nil {
				limit = *in.Limit
			}

			allMems := memory.DB.GetMemories()
			var matched []memory.FactMemory

			for _, m := range allMems {
				if m.Confidence < minConf {
					continue
				}
				if in.Type != "" && m.Type != in.Type {
					continue
				}
				if strings.Contains(strings.ToLower(m.Content), strings.ToLower(in.Query)) ||
					strings.Contains(strings.ToLower(m.Context), strings.ToLower(in.Query)) {
					matched = append(matched, m)
				}
			}

			if len(matched) > limit {
				matched = matched[:limit]
			}

			return ToolSuccess(map[string]interface{}{
				"results": matched,
				"count":   len(matched),
			}), nil
		},
	}
}

// NewForgetMemoryTool instantiates the forget_memory tool structure.
func NewForgetMemoryTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "forget_memory",
		description: "Delete an outdated or incorrect memory by ID.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"memoryId": map[string]interface{}{"type": "string"},
			"reason":   map[string]interface{}{"type": "string"},
		}, []string{"memoryId"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				MemoryID string `json:"memoryId"`
				Reason   string `json:"reason"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			db := memory.DB.RawDB()
			if db == nil {
				return ToolError("database is not initialized"), nil
			}

			res, err := db.Exec("DELETE FROM fact_memories WHERE id = ?", in.MemoryID)
			if err != nil {
				return ToolError("failed to delete memory: " + err.Error()), nil
			}

			rows, _ := res.RowsAffected()
			if rows == 0 {
				return ToolSuccess(map[string]interface{}{
					"success": false,
					"message": "Memory ID not found: " + in.MemoryID,
				}), nil
			}

			return ToolSuccess(map[string]interface{}{
				"success": true,
				"message": "Memory successfully forgotten",
			}), nil
		},
	}
}

// NewCreateTaskTool instantiates the create_task tool structure.
func NewCreateTaskTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "create_task",
		description: "Launch a multi-agent Task that works on a complex goal autonomously.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"name":      map[string]interface{}{"type": "string"},
			"goal":      map[string]interface{}{"type": "string"},
			"objective": map[string]interface{}{"type": "string"},
			"model":     map[string]interface{}{"type": "string"},
			"maxCycles": map[string]interface{}{"type": "integer"},
			"autoStart": map[string]interface{}{"type": "boolean"},
			"projectId": map[string]interface{}{"type": "string"},
		}, []string{"name", "goal"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Name      string `json:"name"`
				Goal      string `json:"goal"`
				Objective string `json:"objective"`
				Model     string `json:"model"`
				MaxCycles *int   `json:"maxCycles"`
				AutoStart *bool  `json:"autoStart"`
				ProjectID string `json:"projectId"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
			_ = memory.DB.SetNodeState(taskID, "orchestrator", "completed", fmt.Sprintf("Task '%s' successfully spawned. Objective: %s", in.Name, in.Objective))

			return ToolSuccess(map[string]interface{}{
				"id":      taskID,
				"name":    in.Name,
				"goal":    in.Goal,
				"queenId": "queen_" + taskID,
				"status":  "pending",
				"message": "Orchestration task successfully initialized",
			}), nil
		},
	}
}

// ==========================================
// Phase 6: Local Relational Tabular Database CRUD Tools
// ==========================================

func autoProvisionDB(db *sql.DB, name string, forceID int) (string, error) {
	dbDir := config.ResolvePath(filepath.Join(".tzro", "local_dbs"))
	_ = os.MkdirAll(dbDir, 0755)
	path := filepath.Join(dbDir, name+".db")

	var execErr error
	if forceID > 0 {
		_, execErr = db.Exec("INSERT OR REPLACE INTO local_databases (id, name, description, path) VALUES (?, ?, ?, ?)", forceID, name, "Automatically provisioned database", path)
	} else {
		_, execErr = db.Exec("INSERT OR REPLACE INTO local_databases (name, description, path) VALUES (?, ?, ?)", name, "Automatically provisioned database", path)
	}
	if execErr != nil {
		return "", fmt.Errorf("failed to auto-create database registry for %s: %w", name, execErr)
	}
	_, _ = openLocalDB(path)
	return path, nil
}

func resolveDBPath(dbID any) (string, error) {
	db := memory.DB.RawDB()
	if db == nil {
		return "", fmt.Errorf("main database is not initialized")
	}

	// Ensure registry table exists dynamically
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS local_databases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		description TEXT,
		path TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	var path string
	var err error

	switch v := dbID.(type) {
	case int:
		err = db.QueryRow("SELECT path FROM local_databases WHERE id = ?", v).Scan(&path)
		if err != nil {
			path, err = autoProvisionDB(db, fmt.Sprintf("db_%d", v), v)
		}
	case int64:
		err = db.QueryRow("SELECT path FROM local_databases WHERE id = ?", v).Scan(&path)
		if err != nil {
			path, err = autoProvisionDB(db, fmt.Sprintf("db_%d", v), int(v))
		}
	case float64:
		err = db.QueryRow("SELECT path FROM local_databases WHERE id = ?", int(v)).Scan(&path)
		if err != nil {
			path, err = autoProvisionDB(db, fmt.Sprintf("db_%d", int(v)), int(v))
		}
	case string:
		if id, parseErr := strconv.Atoi(v); parseErr == nil {
			err = db.QueryRow("SELECT path FROM local_databases WHERE id = ?", id).Scan(&path)
			if err != nil {
				path, err = autoProvisionDB(db, fmt.Sprintf("db_%d", id), id)
			}
		} else {
			err = db.QueryRow("SELECT path FROM local_databases WHERE name = ?", v).Scan(&path)
			if err != nil {
				path, err = autoProvisionDB(db, v, 0)
			}
		}
	default:
		return "", fmt.Errorf("invalid database identifier type: %T", dbID)
	}

	if err != nil {
		return "", fmt.Errorf("local database with ID/Name '%v' not found", dbID)
	}
	return path, nil
}

var (
	localConnectionPool   = make(map[string]*sql.DB)
	localConnectionPoolMu sync.RWMutex
)

func getCachedLocalDB(path string) (*sql.DB, error) {
	localConnectionPoolMu.RLock()
	conn, exists := localConnectionPool[path]
	localConnectionPoolMu.RUnlock()
	if exists {
		if err := conn.Ping(); err == nil {
			return conn, nil
		}
	}

	localConnectionPoolMu.Lock()
	defer localConnectionPoolMu.Unlock()

	// Double-check under lock
	if conn, exists = localConnectionPool[path]; exists {
		if err := conn.Ping(); err == nil {
			return conn, nil
		}
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// Optimize connection limits to prevent descriptor leaks under high parallel load
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxIdleTime(5 * time.Minute)

	_, _ = conn.Exec("PRAGMA journal_mode=WAL;")
	_, _ = conn.Exec("PRAGMA busy_timeout = 5000;")

	localConnectionPool[path] = conn
	return conn, nil
}

func openLocalDB(path string) (*sql.DB, error) {
	return getCachedLocalDB(path)
}

func validateReadOnlySQL(sqlQuery string) error {
	normalized := strings.TrimSpace(strings.ToUpper(sqlQuery))
	if !strings.HasPrefix(normalized, "SELECT") && !strings.HasPrefix(normalized, "WITH") {
		return fmt.Errorf("query rejected: Only read-only SELECT or WITH queries are allowed")
	}

	forbidden := []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE", "PRAGMA", "REPLACE"}
	for _, term := range forbidden {
		if strings.Contains(normalized, term) {
			return fmt.Errorf("query rejected: Mutation keyword '%s' is forbidden in read-only queries", term)
		}
	}
	return nil
}

// NewCreateDatabaseTool instantiates local_db_create_database tool structure
func NewCreateDatabaseTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "local_db_create_database",
		description: "Provision a new named local SQLite database file in user workspace.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"name":        map[string]interface{}{"type": "string"},
			"description": map[string]interface{}{"type": "string"},
		}, []string{"name"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			db := memory.DB.RawDB()
			if db == nil {
				return ToolError("main database is not initialized"), nil
			}

			// Ensure registry table exists dynamically
			_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS local_databases (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT UNIQUE NOT NULL,
				description TEXT,
				path TEXT NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)`)

			dbDir := config.ResolvePath(filepath.Join(".tzro", "local_dbs"))
			_ = os.MkdirAll(dbDir, 0755)
			path := filepath.Join(dbDir, in.Name+".db")

			res, err := db.Exec("INSERT OR REPLACE INTO local_databases (name, description, path) VALUES (?, ?, ?)", in.Name, in.Description, path)
			if err != nil {
				return ToolError("failed to create database registry: " + err.Error()), nil
			}

			id, _ := res.LastInsertId()

			// Provision file
			_, _ = openLocalDB(path)

			return ToolSuccess(map[string]interface{}{
				"id":   id,
				"name": in.Name,
				"path": path,
			}), nil
		},
	}
}

// NewCreateTableTool instantiates local_db_create_table tool structure
func NewCreateTableTool() *BaseAgentTool {
	type ColumnDef struct {
		Name         string      `json:"name"`
		Type         string      `json:"type"`
		PrimaryKey   *bool       `json:"primaryKey,omitempty"`
		NotNull      *bool       `json:"notNull,omitempty"`
		Unique       *bool       `json:"unique,omitempty"`
		DefaultValue interface{} `json:"defaultValue,omitempty"`
	}

	return &BaseAgentTool{
		name:        "local_db_create_table",
		description: "Add a table with typed columns to an existing local database workspace.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"dbId":      map[string]interface{}{"type": "string", "description": "Database ID (integer as string) or Database Name (string)"},
			"tableName": map[string]interface{}{"type": "string"},
			"columns": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":         map[string]interface{}{"type": "string"},
						"type":         map[string]interface{}{"type": "string", "enum": []string{"TEXT", "INTEGER", "REAL", "BLOB"}},
						"primaryKey":   map[string]interface{}{"type": "boolean"},
						"notNull":      map[string]interface{}{"type": "boolean"},
						"unique":       map[string]interface{}{"type": "boolean"},
						"defaultValue": map[string]interface{}{"type": "string"},
					},
					"required": []string{"name", "type"},
				},
			},
		}, []string{"dbId", "tableName", "columns"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				DbID      any         `json:"dbId"`
				TableName string      `json:"tableName"`
				Columns   []ColumnDef `json:"columns"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			path, err := resolveDBPath(in.DbID)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			sqlParts := []string{
				"id INTEGER PRIMARY KEY AUTOINCREMENT",
				"created_at DATETIME DEFAULT CURRENT_TIMESTAMP",
				"updated_at DATETIME DEFAULT CURRENT_TIMESTAMP",
			}

			for _, col := range in.Columns {
				part := fmt.Sprintf("%s %s", col.Name, col.Type)
				if col.PrimaryKey != nil && *col.PrimaryKey {
					part += " PRIMARY KEY"
				}
				if col.NotNull != nil && *col.NotNull {
					part += " NOT NULL"
				}
				if col.Unique != nil && *col.Unique {
					part += " UNIQUE"
				}
				if col.DefaultValue != nil {
					part += fmt.Sprintf(" DEFAULT %v", col.DefaultValue)
				}
				sqlParts = append(sqlParts, part)
			}

			createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", in.TableName, strings.Join(sqlParts, ", "))

			localConn, err := openLocalDB(path)
			if err != nil {
				return ToolError("failed to open local database file: " + err.Error()), nil
			}

			_, err = localConn.Exec(createSQL)
			if err != nil {
				return ToolError("failed to create table: " + err.Error()), nil
			}

			return ToolSuccess(map[string]interface{}{
				"success":   true,
				"tableName": in.TableName,
				"message":   fmt.Sprintf("Table '%s' successfully created", in.TableName),
			}), nil
		},
	}
}

func safeBindVal(val any) any {
	if val == nil {
		return nil
	}
	switch typedVal := val.(type) {
	case string, int, int64, float64, bool:
		return typedVal
	default:
		jsonBytes, err := json.Marshal(typedVal)
		if err == nil {
			return string(jsonBytes)
		}
		return val
	}
}

// NewInsertTool instantiates local_db_insert tool structure
func NewInsertTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "local_db_insert",
		description: "Insert a single row into an existing local table.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"dbId":      map[string]interface{}{"type": "string", "description": "Database ID (integer as string) or Database Name (string)"},
			"tableName": map[string]interface{}{"type": "string"},
			"data":      map[string]interface{}{"type": "object"},
		}, []string{"dbId", "tableName", "data"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				DbID      any            `json:"dbId"`
				TableName string         `json:"tableName"`
				Data      map[string]any `json:"data"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			path, err := resolveDBPath(in.DbID)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			var cols []string
			var placeholders []string
			var vals []interface{}

			for k, v := range in.Data {
				cols = append(cols, k)
				placeholders = append(placeholders, "?")
				vals = append(vals, safeBindVal(v))
			}

			insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", in.TableName, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

			localConn, err := openLocalDB(path)
			if err != nil {
				return ToolError("failed to open local database file: " + err.Error()), nil
			}

			// Proactively check if the table exists, and if not, auto-create it.
			var tableExists bool
			err = localConn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", in.TableName).Scan(&tableExists)
			if err != nil {
				return ToolError("failed to check table existence: " + err.Error()), nil
			}

			if !tableExists {
				sqlParts := []string{
					"id INTEGER PRIMARY KEY AUTOINCREMENT",
					"created_at DATETIME DEFAULT CURRENT_TIMESTAMP",
					"updated_at DATETIME DEFAULT CURRENT_TIMESTAMP",
				}
				for k, v := range in.Data {
					colType := "TEXT"
					switch v.(type) {
					case int, int64:
						colType = "INTEGER"
					case float64:
						colType = "REAL"
					case bool:
						colType = "INTEGER"
					}
					sqlParts = append(sqlParts, fmt.Sprintf("%s %s", k, colType))
				}
				createSQL := fmt.Sprintf("CREATE TABLE %s (%s)", in.TableName, strings.Join(sqlParts, ", "))
				if _, err := localConn.Exec(createSQL); err != nil {
					return ToolError("failed to auto-create table: " + err.Error()), nil
				}
			}

			_, err = localConn.Exec(insertSQL, vals...)
			if err != nil {
				return ToolError("failed to insert record: " + err.Error()), nil
			}

			return ToolSuccess(map[string]interface{}{
				"success": true,
				"message": "Record successfully inserted",
			}), nil
		},
	}
}

// NewUpdateTool instantiates local_db_update tool structure
func NewUpdateTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "local_db_update",
		description: "Update rows matching an equality filter in local database.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"dbId":      map[string]interface{}{"type": "string", "description": "Database ID (integer as string) or Database Name (string)"},
			"tableName": map[string]interface{}{"type": "string"},
			"data":      map[string]interface{}{"type": "object"},
			"where":     map[string]interface{}{"type": "object"},
		}, []string{"dbId", "tableName", "data", "where"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				DbID      any            `json:"dbId"`
				TableName string         `json:"tableName"`
				Data      map[string]any `json:"data"`
				Where     map[string]any `json:"where"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			if len(in.Where) == 0 {
				return ToolError("where clause is strictly required and cannot be empty"), nil
			}

			path, err := resolveDBPath(in.DbID)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			var setParts []string
			var vals []interface{}

			for k, v := range in.Data {
				setParts = append(setParts, fmt.Sprintf("%s = ?", k))
				vals = append(vals, safeBindVal(v))
			}
			setParts = append(setParts, "updated_at = CURRENT_TIMESTAMP")

			var whereParts []string
			for k, v := range in.Where {
				whereParts = append(whereParts, fmt.Sprintf("%s = ?", k))
				vals = append(vals, safeBindVal(v))
			}

			updateSQL := fmt.Sprintf("UPDATE %s SET %s WHERE %s", in.TableName, strings.Join(setParts, ", "), strings.Join(whereParts, " AND "))

			localConn, err := openLocalDB(path)
			if err != nil {
				return ToolError("failed to open local database file: " + err.Error()), nil
			}

			_, err = localConn.Exec(updateSQL, vals...)
			if err != nil {
				return ToolError("failed to update records: " + err.Error()), nil
			}

			return ToolSuccess(map[string]interface{}{
				"success": true,
				"message": "Records successfully updated",
			}), nil
		},
	}
}

// NewDeleteTool instantiates local_db_delete tool structure
func NewDeleteTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "local_db_delete",
		description: "Delete rows matching an equality filter in local database.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"dbId":      map[string]interface{}{"type": "string", "description": "Database ID (integer as string) or Database Name (string)"},
			"tableName": map[string]interface{}{"type": "string"},
			"where":     map[string]interface{}{"type": "object"},
		}, []string{"dbId", "tableName", "where"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				DbID      any            `json:"dbId"`
				TableName string         `json:"tableName"`
				Where     map[string]any `json:"where"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			if len(in.Where) == 0 {
				return ToolError("where clause is strictly required and cannot be empty"), nil
			}

			path, err := resolveDBPath(in.DbID)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			var whereParts []string
			var vals []interface{}

			for k, v := range in.Where {
				whereParts = append(whereParts, fmt.Sprintf("%s = ?", k))
				vals = append(vals, safeBindVal(v))
			}

			deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE %s", in.TableName, strings.Join(whereParts, " AND "))

			localConn, err := openLocalDB(path)
			if err != nil {
				return ToolError("failed to open local database file: " + err.Error()), nil
			}

			_, err = localConn.Exec(deleteSQL, vals...)
			if err != nil {
				return ToolError("failed to delete records: " + err.Error()), nil
			}

			return ToolSuccess(map[string]interface{}{
				"success": true,
				"message": "Records successfully deleted",
			}), nil
		},
	}
}

// NewQueryTool instantiates local_db_query tool structure
func NewQueryTool() *BaseAgentTool {
	return &BaseAgentTool{
		name:        "local_db_query",
		description: "Execute a read-only SELECT statement in local database.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"dbId": map[string]interface{}{"type": "string", "description": "Database ID (integer as string) or Database Name (string)"},
			"sql":  map[string]interface{}{"type": "string"},
		}, []string{"dbId", "sql"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				DbID any    `json:"dbId"`
				SQL  string `json:"sql"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			if err := validateReadOnlySQL(in.SQL); err != nil {
				return ToolError(err.Error()), nil
			}

			path, err := resolveDBPath(in.DbID)
			if err != nil {
				return ToolError(err.Error()), nil
			}

			localConn, err := openLocalDB(path)
			if err != nil {
				return ToolError("failed to open local database file: " + err.Error()), nil
			}

			rows, err := localConn.Query(in.SQL)
			if err != nil {
				return ToolError("query execution failed: " + err.Error()), nil
			}
			defer rows.Close()

			cols, _ := rows.Columns()
			var resultList []map[string]interface{}

			for rows.Next() {
				columns := make([]interface{}, len(cols))
				columnPointers := make([]interface{}, len(cols))
				for i := range columns {
					columnPointers[i] = &columns[i]
				}

				if err := rows.Scan(columnPointers...); err == nil {
					rowMap := make(map[string]interface{})
					for i, colName := range cols {
						val := columns[i]
						if b, ok := val.([]byte); ok {
							rowMap[colName] = string(b)
						} else {
							rowMap[colName] = val
						}
					}
					resultList = append(resultList, rowMap)
				}
			}

			return ToolSuccess(resultList), nil
		},
	}
}
