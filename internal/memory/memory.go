package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"tzro/internal/embeddings"

	_ "modernc.org/sqlite"
)

type FactMemory struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	Type       string    `json:"type"` // "fact" | "preference" | "insight" | "correction" | "anti_pattern" | "strategy"
	Content    string    `json:"content"`
	Context    string    `json:"context"`
	Confidence float64   `json:"confidence"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"createdAt"`
	Embedding  []float32 `json:"embedding,omitempty"`
}

type KGNode struct {
	ID        string                 `json:"id"`
	NodeType  string                 `json:"nodeType"` // "account" | "contact" | "ticket" | "document"
	Name      string                 `json:"name"`
	Metadata  map[string]interface{} `json:"metadata"`
	Source    string                 `json:"source"`
	Weight    float64                `json:"weight"`
	Embedding []float32              `json:"embedding,omitempty"`
}

type KGEdge struct {
	ID       string                 `json:"id"`
	EdgeType string                 `json:"edgeType"` // "belongs_to" | "assigned_to" | "references"
	SourceID string                 `json:"sourceId"`
	TargetID string                 `json:"targetId"`
	Metadata map[string]interface{} `json:"metadata"`
	Weight   float64                `json:"weight"`
}

type KGSubGraph struct {
	Nodes []KGNode `json:"nodes"`
	Edges []KGEdge `json:"edges"`
}

type NodeState struct {
	TaskID      string `json:"taskId"`
	NodeID      string `json:"nodeId"`
	Status      string `json:"status"` // "pending" | "running" | "completed" | "failed" | "skipped"
	Output      string `json:"output"`
	CompletedAt int64  `json:"completedAt"`
}

type Skill struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	TriggerDescription string `json:"triggerDescription"`
	SOPContent         string `json:"sopContent"`
	CreatedAt          int64  `json:"createdAt"`
}

type EntityType struct {
	ID      string `json:"id"`      // Machine key used in KGNode.NodeType (e.g. "contact")
	Label   string `json:"label"`   // Human-readable display name (e.g. "Contact")
	Color   string `json:"color"`   // CSS HSL color string for canvas rendering
	Icon    string `json:"icon"`    // Optional icon hint (e.g. "user", "building", "tag")
	BuiltIn bool   `json:"builtIn"` // true for default types that cannot be deleted
}

type WorkflowDefinition struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	TriggerType   string `json:"triggerType"`   // "cron" | "manual"
	TriggerConfig string `json:"triggerConfig"` // cron expression
	Status        string `json:"status"`        // "active" | "paused"
	NextRunAt     int64  `json:"nextRunAt"`     // unix timestamp
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type WorkflowTask struct {
	WorkflowID     string `json:"workflowId"`
	TaskTemplateID string `json:"taskTemplateId"`
	Name           string `json:"name"`
	Instructions   string `json:"instructions"`
	Dependencies   string `json:"dependencies"` // comma-separated taskTemplateIds
}

type WorkflowExecution struct {
	ID          string `json:"id"`
	WorkflowID  string `json:"workflowId"`
	Status      string `json:"status"` // "running" | "completed" | "failed" | "cancelled"
	StartedAt   int64  `json:"startedAt"`
	CompletedAt int64  `json:"completedAt,omitempty"`
}

type WorkflowTaskExecution struct {
	WorkflowExecutionID string `json:"workflowExecutionId"`
	TaskTemplateID      string `json:"taskTemplateId"`
	TaskExecutionID     string `json:"taskExecutionId"` // tzro taskId
	Status              string `json:"status"`          // "pending" | "running" | "completed" | "failed"
	StartedAt           int64  `json:"startedAt"`
	CompletedAt         int64  `json:"completedAt,omitempty"`
}

type OpenAPIIntegration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OpenAPISpec string `json:"openapiSpec"`
	AuthType    string `json:"authType"`
	AuthKey     string `json:"authKey,omitempty"`
	AuthValue   string `json:"authValue,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

type DurableNotification struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Type          string `json:"type"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	TaskID        string `json:"taskId,omitempty"`
	WorkflowID    string `json:"workflowId,omitempty"`
	TargetID      string `json:"targetId,omitempty"`
	Status        string `json:"status"`
	ActionPayload string `json:"actionPayload,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}


// SqliteDatabase replaces JSONDatabase
type SqliteDatabase struct {
	db              *sql.DB
	jsonPath        string
	dbPath          string
	mutex           sync.RWMutex
	EmbeddingEngine embeddings.EmbeddingEngine
}

var DB = &SqliteDatabase{
	jsonPath: "tzro_db.json",
	dbPath:   "tzro.db",
}

func (sdb *SqliteDatabase) SetDBPathForTesting(path string) {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()
	sdb.dbPath = path
}

func (sdb *SqliteDatabase) GetDBPathForTesting() string {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()
	return sdb.dbPath
}

func (sdb *SqliteDatabase) RawDB() *sql.DB {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()
	return sdb.db
}


// Init loads the database from disk, creates tables, seeds defaults, and runs legacy migration if present
func (sdb *SqliteDatabase) Init() error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	var err error
	sdb.db, err = sql.Open("sqlite", sdb.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Initialize the default Pure Go Embedding Engine for local semantic matching
	sdb.EmbeddingEngine = embeddings.NewPureGoEmbeddingEngine()

	// Create tables if they don't exist
	if err := sdb.createTables(); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// Check if we need to migrate from legacy JSON DB
	if err := sdb.migrateFromJsonIfPresent(); err != nil {
		fmt.Printf("[Memory Warning] Legacy JSON migration failed: %v\n", err)
	}

	// Seed default entity types
	if err := sdb.seedEntityTypes(); err != nil {
		return fmt.Errorf("failed to seed entity types: %w", err)
	}

	return nil
}

// Close closes the database connection
func (sdb *SqliteDatabase) Close() error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()
	if sdb.db != nil {
		return sdb.db.Close()
	}
	return nil
}

func (sdb *SqliteDatabase) createTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS fact_memories (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			type TEXT,
			content TEXT,
			context TEXT,
			confidence REAL,
			source TEXT,
			created_at DATETIME,
			embedding TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS kg_nodes (
			id TEXT PRIMARY KEY,
			node_type TEXT,
			name TEXT,
			metadata TEXT,
			source TEXT,
			weight REAL,
			embedding TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS kg_edges (
			id TEXT PRIMARY KEY,
			edge_type TEXT,
			source_id TEXT,
			target_id TEXT,
			metadata TEXT,
			weight REAL
		);`,
		`CREATE TABLE IF NOT EXISTS node_states (
			task_id TEXT,
			node_id TEXT,
			status TEXT,
			output TEXT,
			completed_at INTEGER,
			PRIMARY KEY (task_id, node_id)
		);`,
		`CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY,
			name TEXT,
			trigger_description TEXT,
			sop_content TEXT,
			created_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS entity_types (
			id TEXT PRIMARY KEY,
			label TEXT,
			color TEXT,
			icon TEXT,
			built_in INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS disk_cache (
			cache_id TEXT PRIMARY KEY,
			raw_payload TEXT,
			envelope_json TEXT,
			created_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS workflows (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			trigger_type TEXT NOT NULL,
			trigger_config TEXT,
			status TEXT CHECK(status IN ('active', 'paused')) NOT NULL DEFAULT 'active',
			next_run_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_tasks (
			workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
			task_template_id TEXT NOT NULL,
			name TEXT NOT NULL,
			instructions TEXT NOT NULL,
			dependencies TEXT,
			PRIMARY KEY (workflow_id, task_template_id)
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_executions (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
			status TEXT CHECK(status IN ('running', 'completed', 'failed', 'cancelled')) NOT NULL,
			started_at INTEGER NOT NULL,
			completed_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_task_executions (
			workflow_execution_id TEXT NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
			task_template_id TEXT NOT NULL,
			task_execution_id TEXT,
			status TEXT CHECK(status IN ('pending', 'running', 'completed', 'failed')) NOT NULL,
			started_at INTEGER,
			completed_at INTEGER,
			PRIMARY KEY (workflow_execution_id, task_template_id)
		);`,
		`CREATE TABLE IF NOT EXISTS openapi_integrations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			openapi_spec TEXT NOT NULL,
			auth_type TEXT NOT NULL,
			auth_key TEXT,
			auth_value TEXT,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS durable_notifications (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			task_id TEXT,
			workflow_id TEXT,
			target_id TEXT,
			status TEXT NOT NULL,
			action_payload TEXT,
			created_at INTEGER NOT NULL
		);`,
	}

	for _, query := range queries {
		if _, err := sdb.db.Exec(query); err != nil {
			return err
		}
	}

	// Dynamic column migrations for backward compatibility with pre-vector databases
	if err := sdb.ensureColumnExists("fact_memories", "embedding", "TEXT"); err != nil {
		return fmt.Errorf("failed to migrate fact_memories schema: %w", err)
	}
	if err := sdb.ensureColumnExists("kg_nodes", "embedding", "TEXT"); err != nil {
		return fmt.Errorf("failed to migrate kg_nodes schema: %w", err)
	}

	return nil
}

func (sdb *SqliteDatabase) ensureColumnExists(tableName, columnName, columnType string) error {
	rows, err := sdb.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return fmt.Errorf("failed to query table info for %s: %w", tableName, err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name string
		var typeStr string
		var notnull int
		var dfltVal sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typeStr, &notnull, &dfltVal, &pk); err != nil {
			return fmt.Errorf("failed to scan table info row: %w", err)
		}
		if name == columnName {
			hasColumn = true
			break
		}
	}

	if !hasColumn {
		fmt.Printf("[Migration] Column '%s' is missing in table '%s'. Adding it...\n", columnName, tableName)
		alterQuery := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, columnType)
		if _, err := sdb.db.Exec(alterQuery); err != nil {
			return fmt.Errorf("failed to execute alter table for %s: %w", tableName, err)
		}
	}
	return nil
}


type legacyJSONDB struct {
	Memories    []FactMemory         `json:"memories"`
	Nodes       map[string]KGNode    `json:"nodes"`
	Edges       map[string]KGEdge    `json:"edges"`
	States      map[string]NodeState `json:"states"`
	Skills      []Skill              `json:"skills"`
	EntityTypes []EntityType         `json:"entityTypes"`
}

func (sdb *SqliteDatabase) migrateFromJsonIfPresent() error {
	if _, err := os.Stat(sdb.jsonPath); os.IsNotExist(err) {
		return nil
	}

	fmt.Printf("[Init] Found legacy JSON database '%s'. Migrating data to SQLite...\n", sdb.jsonPath)

	file, err := os.Open(sdb.jsonPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var legacy legacyJSONDB
	if err := json.NewDecoder(file).Decode(&legacy); err != nil {
		return err
	}

	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Migrate memories
	for _, m := range legacy.Memories {
		_, err = tx.Exec(`INSERT OR IGNORE INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, m.ID, m.UserID, m.Type, m.Content, m.Context, m.Confidence, m.Source, m.CreatedAt)
		if err != nil {
			return err
		}
	}

	// 2. Migrate nodes
	for _, n := range legacy.Nodes {
		metaStr := serializeMetadata(n.Metadata)
		_, err = tx.Exec(`INSERT OR IGNORE INTO kg_nodes (id, node_type, name, metadata, source, weight)
			VALUES (?, ?, ?, ?, ?, ?)`, n.ID, n.NodeType, n.Name, metaStr, n.Source, n.Weight)
		if err != nil {
			return err
		}
	}

	// 3. Migrate edges
	for _, e := range legacy.Edges {
		metaStr := serializeMetadata(e.Metadata)
		_, err = tx.Exec(`INSERT OR IGNORE INTO kg_edges (id, edge_type, source_id, target_id, metadata, weight)
			VALUES (?, ?, ?, ?, ?, ?)`, e.ID, e.EdgeType, e.SourceID, e.TargetID, metaStr, e.Weight)
		if err != nil {
			return err
		}
	}

	// 4. Migrate states
	for _, s := range legacy.States {
		_, err = tx.Exec(`INSERT OR IGNORE INTO node_states (task_id, node_id, status, output, completed_at)
			VALUES (?, ?, ?, ?, ?)`, s.TaskID, s.NodeID, s.Status, s.Output, s.CompletedAt)
		if err != nil {
			return err
		}
	}

	// 5. Migrate skills
	for _, s := range legacy.Skills {
		_, err = tx.Exec(`INSERT OR IGNORE INTO skills (id, name, trigger_description, sop_content, created_at)
			VALUES (?, ?, ?, ?, ?)`, s.ID, s.Name, s.TriggerDescription, s.SOPContent, s.CreatedAt)
		if err != nil {
			return err
		}
	}

	// 6. Migrate EntityTypes
	for _, et := range legacy.EntityTypes {
		builtInInt := 0
		if et.BuiltIn {
			builtInInt = 1
		}
		_, err = tx.Exec(`INSERT OR IGNORE INTO entity_types (id, label, color, icon, built_in)
			VALUES (?, ?, ?, ?, ?)`, et.ID, et.Label, et.Color, et.Icon, builtInInt)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Rename legacy file so we don't migrate it again
	backupPath := sdb.jsonPath + ".bak"
	if err := os.Rename(sdb.jsonPath, backupPath); err != nil {
		fmt.Printf("[Memory Warning] Failed to backup migrated JSON DB: %v\n", err)
	} else {
		fmt.Printf("[Init] Successfully migrated legacy JSON DB data. Backed up to '%s'.\n", backupPath)
	}

	return nil
}

func defaultEntityTypes() []EntityType {
	return []EntityType{
		{ID: "account", Label: "Account", Color: "hsl(265, 85%, 65%)", Icon: "building", BuiltIn: true},
		{ID: "contact", Label: "Contact", Color: "hsl(175, 80%, 48%)", Icon: "user", BuiltIn: true},
		{ID: "ticket", Label: "Ticket", Color: "hsl(348, 82%, 60%)", Icon: "ticket", BuiltIn: true},
		{ID: "document", Label: "Document", Color: "hsl(45, 90%, 55%)", Icon: "file", BuiltIn: true},
	}
}

func (sdb *SqliteDatabase) seedEntityTypes() error {
	defaults := defaultEntityTypes()
	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, et := range defaults {
		builtInInt := 0
		if et.BuiltIn {
			builtInInt = 1
		}
		_, err = tx.Exec(`INSERT OR IGNORE INTO entity_types (id, label, color, icon, built_in)
			VALUES (?, ?, ?, ?, ?)`, et.ID, et.Label, et.Color, et.Icon, builtInInt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

type NeighborhoodParams struct {
	NodeTypes     []string
	EdgeTypes     []string
	MinNodeWeight float64
	MinEdgeWeight float64
	Direction     string // "incoming", "outgoing", "undirected" (default)
	Limit         int    // max total nodes returned
}

type NeighborhoodOption func(*NeighborhoodParams)

func WithNodeTypes(types []string) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.NodeTypes = types
	}
}

func WithEdgeTypes(types []string) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.EdgeTypes = types
	}
}

func WithMinNodeWeight(w float64) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.MinNodeWeight = w
	}
}

func WithMinEdgeWeight(w float64) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.MinEdgeWeight = w
	}
}

func WithDirection(dir string) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.Direction = dir
	}
}

func WithLimit(limit int) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.Limit = limit
	}
}

// GetEntityNeighborhood traverses connected nodes up to maxHops (Graph-RAG traversal) with customizable filters.
func (sdb *SqliteDatabase) GetEntityNeighborhood(entityID string, maxHops int, opts ...NeighborhoodOption) KGSubGraph {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return KGSubGraph{}
	}

	nodes, err := sdb.getNodesMapLocked()
	if err != nil {
		return KGSubGraph{}
	}
	edges, err := sdb.getEdgesSliceLocked()
	if err != nil {
		return KGSubGraph{}
	}

	return sdb.getEntityNeighborhoodLocked(entityID, maxHops, nodes, edges, opts...)
}

func (sdb *SqliteDatabase) getEntityNeighborhoodLocked(
	entityID string,
	maxHops int,
	nodes map[string]KGNode,
	edges []KGEdge,
	opts ...NeighborhoodOption,
) KGSubGraph {
	p := &NeighborhoodParams{
		Direction: "undirected",
	}
	for _, opt := range opts {
		opt(p)
	}

	nodeTypeMap := make(map[string]bool)
	for _, t := range p.NodeTypes {
		nodeTypeMap[t] = true
	}

	edgeTypeMap := make(map[string]bool)
	for _, t := range p.EdgeTypes {
		edgeTypeMap[t] = true
	}

	visited := map[string]bool{entityID: true}
	var allNodes []KGNode
	var allEdges []KGEdge

	// Add start node if it exists
	if startNode, exists := nodes[entityID]; exists {
		if len(nodeTypeMap) > 0 && !nodeTypeMap[startNode.NodeType] {
			return KGSubGraph{}
		}
		if p.MinNodeWeight > 0 && startNode.Weight < p.MinNodeWeight {
			return KGSubGraph{}
		}
		allNodes = append(allNodes, startNode)
	} else {
		return KGSubGraph{}
	}

	if p.Limit > 0 && len(allNodes) >= p.Limit {
		return KGSubGraph{Nodes: allNodes, Edges: []KGEdge{}}
	}

	frontier := []string{entityID}

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []string
		reachedLimit := false

		for _, nodeID := range frontier {
			// Find edges connected to nodeID
			for _, edge := range edges {
				if p.MinEdgeWeight > 0 && edge.Weight < p.MinEdgeWeight {
					continue
				}
				if len(edgeTypeMap) > 0 && !edgeTypeMap[edge.EdgeType] {
					continue
				}

				// Identify neighbor node and validate direction
				var neighborID string
				var isValidDir bool
				if edge.SourceID == nodeID {
					neighborID = edge.TargetID
					isValidDir = (p.Direction == "outgoing" || p.Direction == "undirected" || p.Direction == "")
				} else if edge.TargetID == nodeID {
					neighborID = edge.SourceID
					isValidDir = (p.Direction == "incoming" || p.Direction == "undirected" || p.Direction == "")
				}

				if !isValidDir {
					continue
				}

				// Fetch neighbor node
				neighborNode, exists := nodes[neighborID]
				if !exists {
					continue
				}

				// Filter neighbor node
				if len(nodeTypeMap) > 0 && !nodeTypeMap[neighborNode.NodeType] {
					continue
				}
				if p.MinNodeWeight > 0 && neighborNode.Weight < p.MinNodeWeight {
					continue
				}

				// Append edge if not already added
				alreadyAdded := false
				for _, existingEdge := range allEdges {
					if existingEdge.ID == edge.ID {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					allEdges = append(allEdges, edge)
				}

				// Visit neighbor
				if !visited[neighborID] {
					visited[neighborID] = true
					nextFrontier = append(nextFrontier, neighborID)
					allNodes = append(allNodes, neighborNode)
					if p.Limit > 0 && len(allNodes) >= p.Limit {
						reachedLimit = true
						break
					}
				}
			}
			if reachedLimit {
				break
			}
		}
		if reachedLimit {
			break
		}
		frontier = nextFrontier
	}

	// Filter edges to only include those whose source and target are in the final nodes set
	finalNodeIDs := make(map[string]bool)
	for _, n := range allNodes {
		finalNodeIDs[n.ID] = true
	}
	var finalEdges []KGEdge
	for _, e := range allEdges {
		if finalNodeIDs[e.SourceID] && finalNodeIDs[e.TargetID] {
			finalEdges = append(finalEdges, e)
		}
	}

	return KGSubGraph{Nodes: allNodes, Edges: finalEdges}
}

// GetGraphRAGContext scans a natural language prompt, matches active entity Names or IDs,
// traverses up to 2-hop neighborhoods, and outputs a formatted Markdown Graph-RAG context.
func (sdb *SqliteDatabase) GetGraphRAGContext(prompt string) string {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return ""
	}

	nodes, err := sdb.getNodesMapLocked()
	if err != nil || len(nodes) == 0 {
		return ""
	}
	edges, err := sdb.getEdgesSliceLocked()
	if err != nil {
		return ""
	}

	// 1. Identify matched nodes in the prompt via Hybrid Vector Search
	var matchedIDs []string
	matchedIDsMap := make(map[string]bool)

	// First, check exact word matches (FTS5 / literal candidate pool fallback)
	for id, node := range nodes {
		if isWordMatch(prompt, id) || isWordMatch(prompt, node.Name) {
			matchedIDs = append(matchedIDs, id)
			matchedIDsMap[id] = true
		}
	}

	// Second, if EmbeddingEngine is available, calculate semantic similarity for candidates
	if sdb.EmbeddingEngine != nil {
		promptVec, err := sdb.EmbeddingEngine.Embed(context.Background(), prompt)
		if err == nil {
			for id, node := range nodes {
				if matchedIDsMap[id] {
					continue
				}
				if len(node.Embedding) > 0 {
					sim := sdb.EmbeddingEngine.CosineSimilarity(promptVec, node.Embedding)
					// Threshold of 0.30 indicates strong semantic alignment for sparse vectors
					if sim >= 0.30 {
						matchedIDs = append(matchedIDs, id)
						matchedIDsMap[id] = true
					}
				}
			}
		}
	}

	if len(matchedIDs) == 0 {
		return ""
	}

	// 2. Traverse neighborhood (2 hops) for all matched nodes and deduplicate
	dedupNodes := make(map[string]KGNode)
	dedupEdges := make(map[string]KGEdge)

	for _, matchedID := range matchedIDs {
		sub := sdb.getEntityNeighborhoodLocked(matchedID, 2, nodes, edges)
		for _, n := range sub.Nodes {
			dedupNodes[n.ID] = n
		}
		for _, e := range sub.Edges {
			dedupEdges[e.ID] = e
		}
	}

	// 3. Format into Markdown
	var sb strings.Builder
	sb.WriteString("### RELATIONAL KNOWLEDGE GRAPH CONTEXT (Graph-RAG)\n")
	sb.WriteString("Based on active entities detected in your request, the following local sub-graph has been retrieved:\n\n")

	sb.WriteString("#### Connected Entities\n")
	sb.WriteString("| ID | Type | Name | Weight | Source | Metadata |\n")
	sb.WriteString("| --- | --- | --- | --- | --- | --- |\n")

	// Sort node IDs deterministically
	var sortedNodeIDs []string
	for id := range dedupNodes {
		sortedNodeIDs = append(sortedNodeIDs, id)
	}
	sort.Strings(sortedNodeIDs)

	for _, id := range sortedNodeIDs {
		n := dedupNodes[id]
		metaBytes, _ := json.Marshal(n.Metadata)
		metaStr := string(metaBytes)
		if metaStr == "" {
			metaStr = "{}"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %.2f | %s | %s |\n", n.ID, n.NodeType, n.Name, n.Weight, n.Source, metaStr))
	}
	sb.WriteString("\n")

	sb.WriteString("#### Relationships\n")
	if len(dedupEdges) == 0 {
		sb.WriteString("No active relationships within the retrieved neighborhood.\n")
	} else {
		var sortedEdgeIDs []string
		for id := range dedupEdges {
			sortedEdgeIDs = append(sortedEdgeIDs, id)
		}
		sort.Strings(sortedEdgeIDs)

		for _, id := range sortedEdgeIDs {
			e := dedupEdges[id]
			srcName := e.SourceID
			if sn, exists := dedupNodes[e.SourceID]; exists {
				srcName = sn.Name
			}
			tgtName := e.TargetID
			if tn, exists := dedupNodes[e.TargetID]; exists {
				tgtName = tn.Name
			}
			metaBytes, _ := json.Marshal(e.Metadata)
			metaStr := string(metaBytes)
			if metaStr == "" {
				metaStr = "{}"
			}
			sb.WriteString(fmt.Sprintf("- **%s** (`%s`) --[%s (Weight: %.2f)]--> **%s** (`%s`) | Metadata: %s\n",
				srcName, e.SourceID, e.EdgeType, e.Weight, tgtName, e.TargetID, metaStr))
		}
	}

	return sb.String()
}

// isWordMatch helper to perform a precise, case-insensitive word-boundary search
func isWordMatch(text, word string) bool {
	if len(word) == 0 {
		return false
	}
	textLower := strings.ToLower(text)
	wordLower := strings.ToLower(word)

	start := 0
	for {
		idx := strings.Index(textLower[start:], wordLower)
		if idx == -1 {
			return false
		}
		pos := start + idx

		// Check boundary before
		beforeWord := true
		if pos > 0 {
			rBefore := rune(textLower[pos-1])
			if unicode.IsLetter(rBefore) || unicode.IsDigit(rBefore) || rBefore == '_' {
				beforeWord = false
			}
		}

		// Check boundary after
		afterWord := true
		endPos := pos + len(wordLower)
		if endPos < len(textLower) {
			rAfter := rune(textLower[endPos])
			if unicode.IsLetter(rAfter) || unicode.IsDigit(rAfter) || rAfter == '_' {
				afterWord = false
			}
		}

		if beforeWord && afterWord {
			return true
		}

		start = pos + 1
	}
}

// Tabular KV Memory methods
func (sdb *SqliteDatabase) AddMemory(m FactMemory) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	m.ID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
	m.CreatedAt = time.Now()

	embStr := ""
	if len(m.Embedding) > 0 {
		b, _ := json.Marshal(m.Embedding)
		embStr = string(b)
	}

	_, err := sdb.db.Exec(`INSERT INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.ID, m.UserID, m.Type, m.Content, m.Context, m.Confidence, m.Source, m.CreatedAt, embStr)
	return err
}

func (sdb *SqliteDatabase) GetMemories() []FactMemory {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	rows, err := sdb.db.Query("SELECT id, user_id, type, content, context, confidence, source, created_at, embedding FROM fact_memories ORDER BY created_at DESC")
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query memories: %v\n", err)
		return []FactMemory{}
	}
	defer rows.Close()

	var list []FactMemory
	for rows.Next() {
		var m FactMemory
		var embStr sql.NullString
		err := rows.Scan(&m.ID, &m.UserID, &m.Type, &m.Content, &m.Context, &m.Confidence, &m.Source, &m.CreatedAt, &embStr)
		if err != nil {
			fmt.Printf("[Memory Error] Failed to scan memory row: %v\n", err)
			continue
		}
		if embStr.Valid && embStr.String != "" {
			_ = json.Unmarshal([]byte(embStr.String), &m.Embedding)
		}
		list = append(list, m)
	}
	if list == nil {
		list = []FactMemory{}
	}
	return list
}

// Relational Knowledge Graph methods
func (sdb *SqliteDatabase) AddNode(n KGNode) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	embStr := ""
	if len(n.Embedding) > 0 {
		b, _ := json.Marshal(n.Embedding)
		embStr = string(b)
	}

	metaStr := serializeMetadata(n.Metadata)
	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO kg_nodes (id, node_type, name, metadata, source, weight, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, n.ID, n.NodeType, n.Name, metaStr, n.Source, n.Weight, embStr)
	return err
}

func (sdb *SqliteDatabase) AddEdge(e KGEdge) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	metaStr := serializeMetadata(e.Metadata)
	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO kg_edges (id, edge_type, source_id, target_id, metadata, weight)
		VALUES (?, ?, ?, ?, ?, ?)`, e.ID, e.EdgeType, e.SourceID, e.TargetID, metaStr, e.Weight)
	return err
}

func (sdb *SqliteDatabase) GetNodes() map[string]KGNode {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	nodes, err := sdb.getNodesMapLocked()
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query nodes: %v\n", err)
		return make(map[string]KGNode)
	}
	return nodes
}

func (sdb *SqliteDatabase) getNodesMapLocked() (map[string]KGNode, error) {
	rows, err := sdb.db.Query("SELECT id, node_type, name, metadata, source, weight, embedding FROM kg_nodes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make(map[string]KGNode)
	for rows.Next() {
		var n KGNode
		var metaStr string
		var embStr sql.NullString
		err := rows.Scan(&n.ID, &n.NodeType, &n.Name, &metaStr, &n.Source, &n.Weight, &embStr)
		if err != nil {
			return nil, err
		}
		n.Metadata = deserializeMetadata(metaStr)
		if embStr.Valid && embStr.String != "" {
			_ = json.Unmarshal([]byte(embStr.String), &n.Embedding)
		}
		nodes[n.ID] = n
	}
	return nodes, nil
}

func (sdb *SqliteDatabase) GetEdges() map[string]KGEdge {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	edges, err := sdb.getEdgesSliceLocked()
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query edges: %v\n", err)
		return make(map[string]KGEdge)
	}

	edgeMap := make(map[string]KGEdge)
	for _, e := range edges {
		edgeMap[e.ID] = e
	}
	return edgeMap
}

func (sdb *SqliteDatabase) getEdgesSliceLocked() ([]KGEdge, error) {
	rows, err := sdb.db.Query("SELECT id, edge_type, source_id, target_id, metadata, weight FROM kg_edges")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []KGEdge
	for rows.Next() {
		var e KGEdge
		var metaStr string
		err := rows.Scan(&e.ID, &e.EdgeType, &e.SourceID, &e.TargetID, &metaStr, &e.Weight)
		if err != nil {
			return nil, err
		}
		e.Metadata = deserializeMetadata(metaStr)
		edges = append(edges, e)
	}
	return edges, nil
}

// State Checkpointing methods
func (sdb *SqliteDatabase) SetNodeState(taskID, nodeID, status, output string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	completedAt := time.Now().Unix()
	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO node_states (task_id, node_id, status, output, completed_at)
		VALUES (?, ?, ?, ?, ?)`, taskID, nodeID, status, output, completedAt)
	return err
}

func (sdb *SqliteDatabase) GetNodeState(taskID, nodeID string) (NodeState, bool) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var ns NodeState
	err := sdb.db.QueryRow("SELECT task_id, node_id, status, output, completed_at FROM node_states WHERE task_id = ? AND node_id = ?", taskID, nodeID).
		Scan(&ns.TaskID, &ns.NodeID, &ns.Status, &ns.Output, &ns.CompletedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return NodeState{}, false
		}
		fmt.Printf("[Memory Error] Failed to query node state: %v\n", err)
		return NodeState{}, false
	}
	return ns, true
}

func (sdb *SqliteDatabase) GetLatestNodeOutput(taskID string) (string, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var output string
	err := sdb.db.QueryRow("SELECT output FROM node_states WHERE task_id = ? ORDER BY completed_at DESC LIMIT 1", taskID).Scan(&output)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return output, nil
}

// Skill Synthesizer methods
func (sdb *SqliteDatabase) AddSkill(s *Skill) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	s.ID = fmt.Sprintf("skill_%d", time.Now().UnixNano())
	s.CreatedAt = time.Now().Unix()

	_, err := sdb.db.Exec(`INSERT INTO skills (id, name, trigger_description, sop_content, created_at)
		VALUES (?, ?, ?, ?, ?)`, s.ID, s.Name, s.TriggerDescription, s.SOPContent, s.CreatedAt)
	return err
}

func (sdb *SqliteDatabase) GetSkills() []Skill {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return []Skill{}
	}

	rows, err := sdb.db.Query("SELECT id, name, trigger_description, sop_content, created_at FROM skills ORDER BY created_at DESC")
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query skills: %v\n", err)
		return []Skill{}
	}
	defer rows.Close()

	var list []Skill
	for rows.Next() {
		var s Skill
		err := rows.Scan(&s.ID, &s.Name, &s.TriggerDescription, &s.SOPContent, &s.CreatedAt)
		if err != nil {
			fmt.Printf("[Memory Error] Failed to scan skill row: %v\n", err)
			continue
		}
		list = append(list, s)
	}
	if list == nil {
		list = []Skill{}
	}
	return list
}

// EntityType registry methods
func (sdb *SqliteDatabase) GetEntityTypes() []EntityType {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	rows, err := sdb.db.Query("SELECT id, label, color, icon, built_in FROM entity_types")
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query entity types: %v\n", err)
		return []EntityType{}
	}
	defer rows.Close()

	var list []EntityType
	for rows.Next() {
		var et EntityType
		var builtInInt int
		err := rows.Scan(&et.ID, &et.Label, &et.Color, &et.Icon, &builtInInt)
		if err != nil {
			fmt.Printf("[Memory Error] Failed to scan entity type row: %v\n", err)
			continue
		}
		et.BuiltIn = (builtInInt != 0)
		list = append(list, et)
	}
	if list == nil {
		list = []EntityType{}
	}
	return list
}

func (sdb *SqliteDatabase) AddEntityType(et EntityType) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	// Prevent duplicate IDs
	var exists int
	err := sdb.db.QueryRow("SELECT COUNT(*) FROM entity_types WHERE id = ?", et.ID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("entity type '%s' already exists", et.ID)
	}

	et.BuiltIn = false
	builtInInt := 0

	_, err = sdb.db.Exec(`INSERT INTO entity_types (id, label, color, icon, built_in)
		VALUES (?, ?, ?, ?, ?)`, et.ID, et.Label, et.Color, et.Icon, builtInInt)
	return err
}

func (sdb *SqliteDatabase) DeleteEntityType(id string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	var builtIn int
	err := sdb.db.QueryRow("SELECT built_in FROM entity_types WHERE id = ?", id).Scan(&builtIn)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("entity type '%s' not found", id)
		}
		return err
	}

	if builtIn != 0 {
		return fmt.Errorf("cannot delete built-in entity type '%s'", id)
	}

	_, err = sdb.db.Exec("DELETE FROM entity_types WHERE id = ?", id)
	return err
}

func serializeMetadata(m map[string]interface{}) string {
	if m == nil {
		return "{}"
	}
	bytes, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

func deserializeMetadata(s string) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return make(map[string]interface{})
	}
	if m == nil {
		return make(map[string]interface{})
	}
	return m
}
// Workflows Persistence Operations


func (sdb *SqliteDatabase) SaveWorkflow(wf WorkflowDefinition, tasks []WorkflowTask) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Save or replace workflow definition
	_, err = tx.Exec(`INSERT OR REPLACE INTO workflows (id, name, description, trigger_type, trigger_config, status, next_run_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, wf.ID, wf.Name, wf.Description, wf.TriggerType, wf.TriggerConfig, wf.Status, wf.NextRunAt, wf.CreatedAt, wf.UpdatedAt)
	if err != nil {
		return err
	}

	// 2. Clear old tasks for this workflow
	_, err = tx.Exec(`DELETE FROM workflow_tasks WHERE workflow_id = ?`, wf.ID)
	if err != nil {
		return err
	}

	// 3. Save new tasks
	for _, t := range tasks {
		_, err = tx.Exec(`INSERT INTO workflow_tasks (workflow_id, task_template_id, name, instructions, dependencies)
			VALUES (?, ?, ?, ?, ?)`, wf.ID, t.TaskTemplateID, t.Name, t.Instructions, t.Dependencies)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (sdb *SqliteDatabase) GetWorkflows() ([]WorkflowDefinition, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return []WorkflowDefinition{}, nil
	}

	rows, err := sdb.db.Query("SELECT id, name, description, trigger_type, trigger_config, status, next_run_at, created_at, updated_at FROM workflows ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WorkflowDefinition
	for rows.Next() {
		var wf WorkflowDefinition
		var nextRun sql.NullInt64
		err := rows.Scan(&wf.ID, &wf.Name, &wf.Description, &wf.TriggerType, &wf.TriggerConfig, &wf.Status, &nextRun, &wf.CreatedAt, &wf.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if nextRun.Valid {
			wf.NextRunAt = nextRun.Int64
		}
		list = append(list, wf)
	}
	if list == nil {
		list = []WorkflowDefinition{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) GetWorkflowTasks(wfID string) ([]WorkflowTask, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return []WorkflowTask{}, nil
	}

	rows, err := sdb.db.Query("SELECT workflow_id, task_template_id, name, instructions, dependencies FROM workflow_tasks WHERE workflow_id = ?", wfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WorkflowTask
	for rows.Next() {
		var t WorkflowTask
		var dep sql.NullString
		err := rows.Scan(&t.WorkflowID, &t.TaskTemplateID, &t.Name, &t.Instructions, &dep)
		if err != nil {
			return nil, err
		}
		if dep.Valid {
			t.Dependencies = dep.String
		}
		list = append(list, t)
	}
	if list == nil {
		list = []WorkflowTask{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) DeleteWorkflow(wfID string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("DELETE FROM workflows WHERE id = ?", wfID)
	return err
}

func (sdb *SqliteDatabase) ToggleWorkflow(wfID string, status string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("UPDATE workflows SET status = ?, updated_at = ? WHERE id = ?", status, time.Now().Unix(), wfID)
	return err
}

func (sdb *SqliteDatabase) UpdateWorkflowNextRun(wfID string, nextRun int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("UPDATE workflows SET next_run_at = ? WHERE id = ?", nextRun, wfID)
	return err
}

func (sdb *SqliteDatabase) CreateWorkflowExecution(exec WorkflowExecution, taskRuns []WorkflowTaskExecution) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO workflow_executions (id, workflow_id, status, started_at)
		VALUES (?, ?, ?, ?)`, exec.ID, exec.WorkflowID, exec.Status, exec.StartedAt)
	if err != nil {
		return err
	}

	for _, tr := range taskRuns {
		var taskExecID sql.NullString
		if tr.TaskExecutionID != "" {
			taskExecID.String = tr.TaskExecutionID
			taskExecID.Valid = true
		}
		_, err = tx.Exec(`INSERT INTO workflow_task_executions (workflow_execution_id, task_template_id, task_execution_id, status, started_at)
			VALUES (?, ?, ?, ?, ?)`, tr.WorkflowExecutionID, tr.TaskTemplateID, taskExecID, tr.Status, tr.StartedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (sdb *SqliteDatabase) UpdateWorkflowExecutionStatus(execID string, status string, completedAt int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	var completedVal interface{}
	if completedAt > 0 {
		completedVal = completedAt
	} else {
		completedVal = nil
	}

	_, err := sdb.db.Exec("UPDATE workflow_executions SET status = ?, completed_at = ? WHERE id = ?", status, completedVal, execID)
	return err
}

func (sdb *SqliteDatabase) UpdateWorkflowTaskExecution(execID string, taskTemplateID string, taskExecID string, status string, completedAt int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	var completedVal interface{}
	if completedAt > 0 {
		completedVal = completedAt
	} else {
		completedVal = nil
	}

	var taskExecVal interface{}
	if taskExecID != "" {
		taskExecVal = taskExecID
	} else {
		taskExecVal = nil
	}

	_, err := sdb.db.Exec(`UPDATE workflow_task_executions 
		SET status = ?, task_execution_id = ?, completed_at = ?
		WHERE workflow_execution_id = ? AND task_template_id = ?`, status, taskExecVal, completedVal, execID, taskTemplateID)
	return err
}

func (sdb *SqliteDatabase) GetWorkflowExecutions(wfID string) ([]WorkflowExecution, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var rows *sql.Rows
	var err error
	if wfID != "" {
		rows, err = sdb.db.Query("SELECT id, workflow_id, status, started_at, completed_at FROM workflow_executions WHERE workflow_id = ? ORDER BY started_at DESC", wfID)
	} else {
		rows, err = sdb.db.Query("SELECT id, workflow_id, status, started_at, completed_at FROM workflow_executions ORDER BY started_at DESC")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WorkflowExecution
	for rows.Next() {
		var exec WorkflowExecution
		var completed sql.NullInt64
		err := rows.Scan(&exec.ID, &exec.WorkflowID, &exec.Status, &exec.StartedAt, &completed)
		if err != nil {
			return nil, err
		}
		if completed.Valid {
			exec.CompletedAt = completed.Int64
		}
		list = append(list, exec)
	}
	if list == nil {
		list = []WorkflowExecution{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) GetWorkflowExecutionDetails(execID string) (*WorkflowExecution, []WorkflowTaskExecution, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var exec WorkflowExecution
	var completed sql.NullInt64
	err := sdb.db.QueryRow("SELECT id, workflow_id, status, started_at, completed_at FROM workflow_executions WHERE id = ?", execID).
		Scan(&exec.ID, &exec.WorkflowID, &exec.Status, &exec.StartedAt, &completed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, fmt.Errorf("workflow execution '%s' not found", execID)
		}
		return nil, nil, err
	}
	if completed.Valid {
		exec.CompletedAt = completed.Int64
	}

	rows, err := sdb.db.Query("SELECT workflow_execution_id, task_template_id, task_execution_id, status, started_at, completed_at FROM workflow_task_executions WHERE workflow_execution_id = ?", execID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var taskRuns []WorkflowTaskExecution
	for rows.Next() {
		var tr WorkflowTaskExecution
		var taskExecID sql.NullString
		var completedVal sql.NullInt64
		err := rows.Scan(&tr.WorkflowExecutionID, &tr.TaskTemplateID, &taskExecID, &tr.Status, &tr.StartedAt, &completedVal)
		if err != nil {
			return nil, nil, err
		}
		if taskExecID.Valid {
			tr.TaskExecutionID = taskExecID.String
		}
		if completedVal.Valid {
			tr.CompletedAt = completedVal.Int64
		}
		taskRuns = append(taskRuns, tr)
	}
	if taskRuns == nil {
		taskRuns = []WorkflowTaskExecution{}
	}

	return &exec, taskRuns, nil
}

// OpenAPI Integrations persistence methods
func (sdb *SqliteDatabase) SaveOpenAPIIntegration(oi OpenAPIIntegration) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO openapi_integrations (id, name, openapi_spec, auth_type, auth_key, auth_value, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, oi.ID, oi.Name, oi.OpenAPISpec, oi.AuthType, oi.AuthKey, oi.AuthValue, oi.CreatedAt)
	return err
}

func (sdb *SqliteDatabase) GetOpenAPIIntegrations() ([]OpenAPIIntegration, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	rows, err := sdb.db.Query("SELECT id, name, openapi_spec, auth_type, auth_key, auth_value, created_at FROM openapi_integrations ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []OpenAPIIntegration
	for rows.Next() {
		var oi OpenAPIIntegration
		var authKey, authValue sql.NullString
		err := rows.Scan(&oi.ID, &oi.Name, &oi.OpenAPISpec, &oi.AuthType, &authKey, &authValue, &oi.CreatedAt)
		if err != nil {
			return nil, err
		}
		if authKey.Valid {
			oi.AuthKey = authKey.String
		}
		if authValue.Valid {
			oi.AuthValue = authValue.String
		}
		list = append(list, oi)
	}
	if list == nil {
		list = []OpenAPIIntegration{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) DeleteOpenAPIIntegration(id string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("DELETE FROM openapi_integrations WHERE id = ?", id)
	return err
}

func (sdb *SqliteDatabase) AddNotification(n DurableNotification) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO durable_notifications 
		(id, source, type, title, message, task_id, workflow_id, target_id, status, action_payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Source, n.Type, n.Title, n.Message,
		sqlNullString(n.TaskID), sqlNullString(n.WorkflowID), sqlNullString(n.TargetID),
		n.Status, sqlNullString(n.ActionPayload), n.CreatedAt)
	return err
}

func (sdb *SqliteDatabase) GetNotifications(statusFilter string) ([]DurableNotification, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	query := "SELECT id, source, type, title, message, task_id, workflow_id, target_id, status, action_payload, created_at FROM durable_notifications"
	var args []interface{}
	if statusFilter != "" {
		query += " WHERE status = ?"
		args = append(args, statusFilter)
	}
	query += " ORDER BY created_at DESC"

	rows, err := sdb.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []DurableNotification
	for rows.Next() {
		var n DurableNotification
		var taskID, workflowID, targetID, actionPayload sql.NullString
		err := rows.Scan(&n.ID, &n.Source, &n.Type, &n.Title, &n.Message,
			&taskID, &workflowID, &targetID, &n.Status, &actionPayload, &n.CreatedAt)
		if err != nil {
			return nil, err
		}
		n.TaskID = taskID.String
		n.WorkflowID = workflowID.String
		n.TargetID = targetID.String
		n.ActionPayload = actionPayload.String
		list = append(list, n)
	}
	if list == nil {
		list = []DurableNotification{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) UpdateNotificationStatus(id string, status string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := sdb.db.Exec("UPDATE durable_notifications SET status = ? WHERE id = ?", status, id)
	return err
}

func sqlNullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

