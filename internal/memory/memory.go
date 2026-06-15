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
	"tzro/internal/config"
	"tzro/internal/db"
	"tzro/internal/embeddings"

	_ "modernc.org/sqlite"
)

// SqliteDatabase replaces JSONDatabase. Conceptually acts as DatabaseManager.
type SqliteDatabase struct {
	db              *sql.DB
	dialect         db.DialectAdapter
	jsonPath        string
	dbPath          string
	mutex           sync.RWMutex
	EmbeddingEngine embeddings.EmbeddingEngine
}

// DatabaseManager is a type alias for SqliteDatabase to support Pristal standards
type DatabaseManager = SqliteDatabase

var DB = &SqliteDatabase{
	jsonPath: "tzro_db.json",
	dbPath:   "tzro.db",
	dialect:  &db.SqliteDialect{},
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

func (sdb *SqliteDatabase) InitWithConnection(conn *sql.DB, dialect db.DialectAdapter) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	sdb.db = conn
	sdb.dialect = dialect
	sdb.EmbeddingEngine = embeddings.NewPureGoEmbeddingEngine()

	// Run initialization queries provided by dialect
	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, query := range dialect.SchemaInitQueries() {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Seed default entity types
	if err := sdb.seedEntityTypes(); err != nil {
		return fmt.Errorf("failed to seed entity types: %w", err)
	}

	return nil
}

// Init loads the database from disk, creates tables, seeds defaults, and runs legacy migration if present
func (sdb *SqliteDatabase) Init() error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	// Resolve dbPath and jsonPath dynamically if env variables are set and the current paths are defaults
	if sdb.dbPath == "tzro.db" {
		if envPath := os.Getenv("TZRO_DB_PATH"); envPath != "" {
			sdb.dbPath = envPath
		} else {
			sdb.dbPath = config.ResolvePath("tzro.db")
		}
	}
	if sdb.jsonPath == "tzro_db.json" {
		sdb.jsonPath = config.ResolvePath("tzro_db.json")
	}

	var err error
	sdb.db, err = sql.Open("sqlite", sdb.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if sdb.dialect == nil {
		sdb.dialect = &db.SqliteDialect{}
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
			raw_output TEXT DEFAULT '',
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
			status TEXT CHECK(status IN ('pending', 'running', 'completed', 'failed', 'interrupted')) NOT NULL,
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
		`CREATE TABLE IF NOT EXISTS thought_chain (
			id           TEXT PRIMARY KEY,
			probe_id     TEXT NOT NULL,
			task_id      TEXT NOT NULL,
			step_index   INTEGER NOT NULL,
			thought      TEXT NOT NULL,
			tool_name    TEXT,
			tool_args    TEXT,
			tool_output  TEXT,
			embedding    BLOB,
			created_at   INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS thought_chain_summaries (
			id           TEXT PRIMARY KEY,
			probe_id     TEXT NOT NULL,
			task_id      TEXT NOT NULL,
			step_range   TEXT NOT NULL,
			summary      TEXT NOT NULL,
			embedding    BLOB,
			created_at   INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS edge_thoughts (
			id              TEXT PRIMARY KEY,
			task_id         TEXT NOT NULL,
			source_node     TEXT NOT NULL,
			target_node     TEXT NOT NULL,
			thought         TEXT NOT NULL,
			goal_confidence REAL NOT NULL,
			goal_achieved   INTEGER NOT NULL DEFAULT 0,
			step_index      INTEGER NOT NULL,
			created_at      INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS dashboard_specs (
			id TEXT PRIMARY KEY,
			spec TEXT NOT NULL,
			generated_at INTEGER NOT NULL,
			generator_task_id TEXT,
			ttl_seconds INTEGER NOT NULL DEFAULT 14400
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
	if err := sdb.ensureColumnExistsTx(tx, "node_states", "raw_output", "TEXT"); err != nil {
		return fmt.Errorf("failed to migrate node_states schema: %w", err)
	}

	// Dynamic Workflow Orchestration column migrations (PRD: Dynamic Workflow Orchestration)
	if err := sdb.ensureColumnExistsTx(tx, "workflows", "orchestration_mode", "TEXT DEFAULT 'static'"); err != nil {
		return fmt.Errorf("failed to migrate workflows schema (orchestration_mode): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflows", "goal", "TEXT DEFAULT ''"); err != nil {
		return fmt.Errorf("failed to migrate workflows schema (goal): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflows", "approved_level", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to migrate workflows schema (approved_level): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflows", "max_tokens", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to migrate workflows schema (max_tokens): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflows", "max_tool_calls", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to migrate workflows schema (max_tool_calls): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflows", "spawned_by", "TEXT DEFAULT ''"); err != nil {
		return fmt.Errorf("failed to migrate workflows schema (spawned_by): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflow_executions", "tokens_consumed", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to migrate workflow_executions schema (tokens_consumed): %w", err)
	}
	if err := sdb.ensureColumnExistsTx(tx, "workflow_executions", "tool_calls_consumed", "INTEGER DEFAULT 0"); err != nil {
		return fmt.Errorf("failed to migrate workflow_executions schema (tool_calls_consumed): %w", err)
	}

	return tx.Commit()
}

func (sdb *SqliteDatabase) ensureColumnExistsTx(tx *sql.Tx, tableName, columnName, columnType string) error {
	if sdb.dialect != nil && sdb.dialect.DriverName() != "sqlite" {
		return nil
	}
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
	query := sdb.dialect.UpsertNodeStateQuery()
	_, err := sdb.db.Exec(query, taskID, nodeID, status, output, completedAt)
	return err
}

func (sdb *SqliteDatabase) GetNodeState(taskID, nodeID string) (NodeState, bool) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var ns NodeState
	var rawOutput sql.NullString
	err := sdb.db.QueryRow("SELECT task_id, node_id, status, output, COALESCE(raw_output, '') as raw_output, completed_at FROM node_states WHERE task_id = ? AND node_id = ?", taskID, nodeID).
		Scan(&ns.TaskID, &ns.NodeID, &ns.Status, &ns.Output, &rawOutput, &ns.CompletedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return NodeState{}, false
		}
		fmt.Printf("[Memory Error] Failed to query node state: %v\n", err)
		return NodeState{}, false
	}
	if rawOutput.Valid {
		ns.RawOutput = rawOutput.String
	}
	return ns, true
}

// SetNodeRawOutput stores clean tool output for a node, used by downstream variable interpolation.
// This is separate from the display-formatted Output to avoid corruption from tier prefixes and compaction.
func (sdb *SqliteDatabase) SetNodeRawOutput(taskID, nodeID, rawOutput string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("UPDATE node_states SET raw_output = ? WHERE task_id = ? AND node_id = ?", rawOutput, taskID, nodeID)
	return err
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

// GetAllNodeStates returns all node states for a task, ordered by completion time ascending.
// Used by the accumulated context builder to collect upstream node outputs for structured
// context injection into GBNF bridge prompts.
func (sdb *SqliteDatabase) GetAllNodeStates(taskID string) []NodeState {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return nil
	}

	rows, err := sdb.db.Query(
		"SELECT task_id, node_id, status, output, COALESCE(raw_output, '') as raw_output, completed_at FROM node_states WHERE task_id = ? ORDER BY completed_at ASC",
		taskID,
	)
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query all node states: %v\n", err)
		return nil
	}
	defer rows.Close()

	var states []NodeState
	for rows.Next() {
		var ns NodeState
		var rawOutput sql.NullString
		if err := rows.Scan(&ns.TaskID, &ns.NodeID, &ns.Status, &ns.Output, &rawOutput, &ns.CompletedAt); err != nil {
			fmt.Printf("[Memory Error] Failed to scan node state row: %v\n", err)
			continue
		}
		if rawOutput.Valid {
			ns.RawOutput = rawOutput.String
		}
		states = append(states, ns)
	}
	return states
}

// TaskSummary represents a derived summary of a planning task and its node states.
type TaskSummary struct {
	TaskID    string `json:"taskId"`
	Status    string `json:"status"` // "pending" | "running" | "completed" | "failed"
	CreatedAt int64  `json:"createdAt"`
	NodeCount int    `json:"nodeCount"`
}

// GetRecentTasks scans node_states to retrieve derived summaries of recent tasks.
func (sdb *SqliteDatabase) GetRecentTasks(limit int, statusFilter string) ([]TaskSummary, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT task_id, 
		       MAX(completed_at) as last_completed,
		       COUNT(*) as node_count,
		       SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed_count,
		       SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) as running_count,
		       SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) as completed_count
		FROM node_states
		GROUP BY task_id
	`
	var args []interface{}
	wrappedQuery := fmt.Sprintf(`
		SELECT task_id, last_completed, node_count, failed_count, running_count, completed_count
		FROM (%s)
	`, query)
	wrappedQuery += " ORDER BY last_completed DESC"

	// If no status filter, we can limit at the database level.
	// But since we filter status in Go memory (as it is derived), we do limit filtering after.
	// However, if we do a subquery, we can filter status in SQL!
	// Let's write the status derivation in SQL so we can filter and limit at DB level:
	// CASE WHEN failed_count > 0 THEN 'failed' ...
	// That's even cleaner! Let's do that.

	sqlWithStatus := `
		SELECT task_id, last_completed, node_count,
		       CASE 
		           WHEN failed_count > 0 THEN 'failed'
		           WHEN running_count > 0 THEN 'running'
		           WHEN completed_count = node_count AND node_count > 0 THEN 'completed'
		           ELSE 'pending'
		       END as derived_status
		FROM (
			SELECT task_id, 
			       MAX(completed_at) as last_completed,
			       COUNT(*) as node_count,
			       SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed_count,
			       SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) as running_count,
			       SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) as completed_count
			FROM node_states
			GROUP BY task_id
		)
	`

	finalQuery := sqlWithStatus
	if statusFilter != "" && statusFilter != "all" {
		finalQuery += " WHERE derived_status = ?"
		args = append(args, statusFilter)
	}
	finalQuery += " ORDER BY last_completed DESC"
	if limit > 0 {
		finalQuery += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := sdb.db.Query(finalQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TaskSummary
	for rows.Next() {
		var summary TaskSummary
		var lastCompleted sql.NullInt64
		err := rows.Scan(&summary.TaskID, &lastCompleted, &summary.NodeCount, &summary.Status)
		if err != nil {
			return nil, err
		}
		if lastCompleted.Valid {
			summary.CreatedAt = lastCompleted.Int64
		}
		list = append(list, summary)
	}

	if list == nil {
		list = []TaskSummary{}
	}
	return list, nil
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

	query := sdb.dialect.UpsertNotificationQuery()
	_, err := sdb.db.Exec(query,
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
	query := sdb.dialect.InsertMemoryQuery()
	_, _ = sdb.db.Exec(query, id, "default", "session_turn", string(logBytes), sessionID, 1.0, "auto_history", createdAt, "")
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

// SearchMemoriesAndNodes queries both fact memories and knowledge graph nodes using hybrid vector / cosine text similarity.
func (sdb *SqliteDatabase) SearchMemoriesAndNodes(query string, limit int) ([]FactMemory, []KGNode, error) {
	memories := sdb.GetMemories()
	nodesMap := sdb.GetNodes()

	var matchedMems []FactMemory
	var matchedNodes []KGNode

	useVector := false
	if sdb.EmbeddingEngine != nil {
		queryVec, err := sdb.EmbeddingEngine.Embed(context.Background(), query)
		if err == nil && len(queryVec) > 0 {
			type scoredMem struct {
				m     FactMemory
				score float64
			}
			type scoredNode struct {
				n     KGNode
				score float64
			}
			var vecMems []scoredMem
			var vecNodes []scoredNode

			for _, m := range memories {
				if len(m.Embedding) > 0 {
					sim := float64(sdb.EmbeddingEngine.CosineSimilarity(queryVec, m.Embedding))
					if sim >= 0.25 {
						vecMems = append(vecMems, scoredMem{m: m, score: sim})
					}
				}
			}

			for _, n := range nodesMap {
				if len(n.Embedding) > 0 {
					sim := float64(sdb.EmbeddingEngine.CosineSimilarity(queryVec, n.Embedding))
					if sim >= 0.25 {
						vecNodes = append(vecNodes, scoredNode{n: n, score: sim})
					}
				}
			}

			if len(vecMems) > 0 || len(vecNodes) > 0 {
				useVector = true
				// Sort & slice
				sort.Slice(vecMems, func(i, j int) bool {
					return vecMems[i].score > vecMems[j].score
				})
				sort.Slice(vecNodes, func(i, j int) bool {
					return vecNodes[i].score > vecNodes[j].score
				})

				for i := 0; i < len(vecMems); i++ {
					if limit > 0 && len(matchedMems) >= limit {
						break
					}
					matchedMems = append(matchedMems, vecMems[i].m)
				}
				for i := 0; i < len(vecNodes); i++ {
					if limit > 0 && len(matchedNodes) >= limit {
						break
					}
					matchedNodes = append(matchedNodes, vecNodes[i].n)
				}
			}
		}
	}

	if !useVector {
		// Fallback to text similarity
		type scoredMem struct {
			m     FactMemory
			score float64
		}
		type scoredNode struct {
			n     KGNode
			score float64
		}
		var textMems []scoredMem
		var textNodes []scoredNode

		for _, m := range memories {
			sim := embeddings.CosineSimilarity(query, m.Content)
			if sim >= 0.15 {
				textMems = append(textMems, scoredMem{m: m, score: sim})
			}
		}

		for _, n := range nodesMap {
			nodeText := n.Name + " " + n.NodeType
			sim := embeddings.CosineSimilarity(query, nodeText)
			if sim >= 0.15 {
				textNodes = append(textNodes, scoredNode{n: n, score: sim})
			}
		}

		sort.Slice(textMems, func(i, j int) bool {
			return textMems[i].score > textMems[j].score
		})
		sort.Slice(textNodes, func(i, j int) bool {
			return textNodes[i].score > textNodes[j].score
		})

		for i := 0; i < len(textMems); i++ {
			if limit > 0 && len(matchedMems) >= limit {
				break
			}
			matchedMems = append(matchedMems, textMems[i].m)
		}
		for i := 0; i < len(textNodes); i++ {
			if limit > 0 && len(matchedNodes) >= limit {
				break
			}
			matchedNodes = append(matchedNodes, textNodes[i].n)
		}
	}

	return matchedMems, matchedNodes, nil
}

// AddThoughtStep persists a single Thought Chain step to SQLite.
// Each step is committed immediately for durability — if the process crashes,
// execution can resume from the last committed step.
func (sdb *SqliteDatabase) AddThoughtStep(step ThoughtStep) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := sdb.db.Exec(
		`INSERT INTO thought_chain (id, probe_id, task_id, step_index, thought, tool_name, tool_args, tool_output, embedding, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		step.ID, step.ProbeID, step.TaskID, step.StepIndex, step.Thought,
		step.ToolName, step.ToolArgs, step.ToolOutput, step.Embedding, step.CreatedAt,
	)
	return err
}

// GetThoughtSteps retrieves all thought chain steps for a given probe, ordered by step_index ascending.
func (sdb *SqliteDatabase) GetThoughtSteps(probeID string) ([]ThoughtStep, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := sdb.db.Query(
		`SELECT id, probe_id, task_id, step_index, thought, COALESCE(tool_name,''), COALESCE(tool_args,''), COALESCE(tool_output,''), embedding, created_at
		FROM thought_chain WHERE probe_id = ? ORDER BY step_index ASC`, probeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []ThoughtStep
	for rows.Next() {
		var s ThoughtStep
		if err := rows.Scan(&s.ID, &s.ProbeID, &s.TaskID, &s.StepIndex, &s.Thought,
			&s.ToolName, &s.ToolArgs, &s.ToolOutput, &s.Embedding, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan thought step: %w", err)
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// AddThoughtSummary persists a rolling compaction summary of a Thought Chain.
func (sdb *SqliteDatabase) AddThoughtSummary(summary ThoughtSummary) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := sdb.db.Exec(
		`INSERT INTO thought_chain_summaries (id, probe_id, task_id, step_range, summary, embedding, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		summary.ID, summary.ProbeID, summary.TaskID, summary.StepRange,
		summary.Summary, summary.Embedding, summary.CreatedAt,
	)
	return err
}

// GetLatestSummary retrieves the most recent compaction summary for a probe.
func (sdb *SqliteDatabase) GetLatestSummary(probeID string) (ThoughtSummary, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return ThoughtSummary{}, fmt.Errorf("database not initialized")
	}

	var s ThoughtSummary
	err := sdb.db.QueryRow(
		`SELECT id, probe_id, task_id, step_range, summary, embedding, created_at
		FROM thought_chain_summaries WHERE probe_id = ? ORDER BY created_at DESC LIMIT 1`, probeID,
	).Scan(&s.ID, &s.ProbeID, &s.TaskID, &s.StepRange, &s.Summary, &s.Embedding, &s.CreatedAt)

	if err != nil {
		return ThoughtSummary{}, err
	}
	return s, nil
}

// --- Edge Thought persistence (ADR-0024) ---

// AddEdgeThought persists an EdgeThought to the edge_thoughts table.
func (sdb *SqliteDatabase) AddEdgeThought(et EdgeThought) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	goalAchievedInt := 0
	if et.GoalAchieved {
		goalAchievedInt = 1
	}

	_, err := sdb.db.Exec(
		`INSERT OR REPLACE INTO edge_thoughts (id, task_id, source_node, target_node, thought, goal_confidence, goal_achieved, step_index, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		et.ID, et.TaskID, et.SourceNode, et.TargetNode, et.Thought,
		et.GoalConfidence, goalAchievedInt, et.StepIndex, et.CreatedAt,
	)
	return err
}

// GetEdgeThoughtsForNode returns all edge thoughts targeting a specific node in a task.
func (sdb *SqliteDatabase) GetEdgeThoughtsForNode(taskID, targetNode string) ([]EdgeThought, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	rows, err := sdb.db.Query(
		`SELECT id, task_id, source_node, target_node, thought, goal_confidence, goal_achieved, step_index, created_at
		FROM edge_thoughts WHERE task_id = ? AND target_node = ? ORDER BY step_index ASC`,
		taskID, targetNode,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var thoughts []EdgeThought
	for rows.Next() {
		var et EdgeThought
		var goalAchievedInt int
		if err := rows.Scan(&et.ID, &et.TaskID, &et.SourceNode, &et.TargetNode, &et.Thought,
			&et.GoalConfidence, &goalAchievedInt, &et.StepIndex, &et.CreatedAt); err != nil {
			return nil, err
		}
		et.GoalAchieved = goalAchievedInt != 0
		thoughts = append(thoughts, et)
	}
	return thoughts, nil
}

// GetLatestEdgeThought returns the most recent edge thought for a task (by step_index).
func (sdb *SqliteDatabase) GetLatestEdgeThought(taskID string) (*EdgeThought, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var et EdgeThought
	var goalAchievedInt int
	err := sdb.db.QueryRow(
		`SELECT id, task_id, source_node, target_node, thought, goal_confidence, goal_achieved, step_index, created_at
		FROM edge_thoughts WHERE task_id = ? ORDER BY step_index DESC LIMIT 1`,
		taskID,
	).Scan(&et.ID, &et.TaskID, &et.SourceNode, &et.TargetNode, &et.Thought,
		&et.GoalConfidence, &goalAchievedInt, &et.StepIndex, &et.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	et.GoalAchieved = goalAchievedInt != 0
	return &et, nil
}
