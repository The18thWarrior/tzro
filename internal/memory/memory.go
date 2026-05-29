package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"tzro/internal/embeddings"

	_ "modernc.org/sqlite"
)

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
	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

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
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}

	// Dynamic column migrations for backward compatibility with pre-vector databases
	if err := sdb.ensureColumnExistsTx(tx, "fact_memories", "embedding", "TEXT"); err != nil {
		return fmt.Errorf("failed to migrate fact_memories schema: %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "kg_nodes", "embedding", "TEXT"); err != nil {
		return fmt.Errorf("failed to migrate kg_nodes schema: %w", err)
	}

	return tx.Commit()
}

func (sdb *SqliteDatabase) ensureColumnExistsTx(tx *sql.Tx, tableName, columnName, columnType string) error {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
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
		if _, err := tx.Exec(alterQuery); err != nil {
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

// GetSessionID extracts the canonical session identifier from a taskID.
// It removes any turn-specific trailing suffix (e.g. "_t0", "_t1").
func GetSessionID(taskID string) string {
	if idx := strings.Index(taskID, "_t"); idx != -1 {
		return taskID[:idx]
	}
	return taskID
}

// AddSessionTurn automatically determines the next turn index, structures the Dialogue Turn log,
// and durably persists it in SQLite under the session ID.
func (sdb *SqliteDatabase) AddSessionTurn(sessionID string, userMessage string, executedTools []string) {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return
	}

	var count int
	_ = sdb.db.QueryRow("SELECT COUNT(*) FROM fact_memories WHERE type = 'session_turn' AND context = ?", sessionID).Scan(&count)

	log := SessionTurnLog{
		TurnIdx:       count + 1,
		UserMessage:   userMessage,
		ExecutedTools: executedTools,
	}

	logBytes, err := json.Marshal(log)
	if err != nil {
		return
	}

	id := fmt.Sprintf("mem_%d", time.Now().UnixNano())
	createdAt := time.Now()
	_, _ = sdb.db.Exec(`INSERT INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at)
		VALUES (?, 'default', 'session_turn', ?, ?, 1.0, 'auto_history', ?)`, id, string(logBytes), sessionID, createdAt)
}

// GetSessionHistoryContext retrieves the dialogue and tool execution history for a session,
// applying a sliding window of detail (last 2 turns in full detail, older turns in bulleted rollup).
func (sdb *SqliteDatabase) GetSessionHistoryContext(sessionID string) string {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return ""
	}

	// Query memories of type 'session_turn' for the given sessionID (stored in context field)
	rows, err := sdb.db.Query("SELECT content FROM fact_memories WHERE type = 'session_turn' AND context = ? ORDER BY created_at ASC", sessionID)
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query session turns: %v\n", err)
		return ""
	}
	defer rows.Close()

	var logs []SessionTurnLog
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err == nil {
			var log SessionTurnLog
			if json.Unmarshal([]byte(content), &log) == nil {
				logs = append(logs, log)
			}
		}
	}

	if len(logs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("### CONVERSATIONAL DIALOGUE HISTORY\n")
	sb.WriteString("Below is the record of previous user instructions and the actions executed by the agent in this session:\n\n")

	// Sliding window boundary: we keep the last 2 turns in full detail
	fullDetailThreshold := len(logs) - 2

	// Bulleted Rollup for older turns
	var rollupLines []string
	for i := 0; i < fullDetailThreshold; i++ {
		log := logs[i]
		toolsStr := "None"
		if len(log.ExecutedTools) > 0 {
			toolsStr = strings.Join(log.ExecutedTools, ", ")
		}
		rollupLines = append(rollupLines, fmt.Sprintf("*   **Turn %d**: User asked: \"%s\". Agent executed tools: %s.", log.TurnIdx, log.UserMessage, toolsStr))
	}

	if len(rollupLines) > 0 {
		sb.WriteString("#### Summary of Prior Turns\n")
		sb.WriteString(strings.Join(rollupLines, "\n"))
		sb.WriteString("\n\n")
	}

	// Full detail for the last 2 turns
	startDetail := fullDetailThreshold
	if startDetail < 0 {
		startDetail = 0
	}

	for i := startDetail; i < len(logs); i++ {
		log := logs[i]
		sb.WriteString(fmt.Sprintf("#### [Turn %d]\n", log.TurnIdx))
		sb.WriteString(fmt.Sprintf("*   **User Instruction**: \"%s\"\n", log.UserMessage))
		if len(log.ExecutedTools) > 0 {
			sb.WriteString("*   **Actions Executed**:\n")
			for _, t := range log.ExecutedTools {
				sb.WriteString(fmt.Sprintf("    *   `%s`\n", t))
			}
		} else {
			sb.WriteString("*   **Actions Executed**: None (No tool calls required/made).\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
