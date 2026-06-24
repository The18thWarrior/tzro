package db

type ConnectionConfig struct {
	Host          string
	Port          int
	DatabaseName  string
	Username      string
	Password      string
	SslEnabled    bool
	ExtraOptions  string
	ConnectionURL string
}

type DialectAdapter interface {
	DriverName() string
	DataSourceName(cfg ConnectionConfig) string
	TableInfoQuery(tableName string) string
	SchemaInitQueries() []string

	// Dialect Query Registry for operations that vary significantly by SQL engine
	UpsertNodeStateQuery() string
	UpsertNotificationQuery() string
	InsertMemoryQuery() string
}

// SqliteDialect implements DialectAdapter for SQLite
type SqliteDialect struct{}

func (d *SqliteDialect) DriverName() string {
	return "sqlite"
}

func (d *SqliteDialect) DataSourceName(cfg ConnectionConfig) string {
	if cfg.ConnectionURL != "" {
		return cfg.ConnectionURL
	}
	return "tzro.db"
}

func (d *SqliteDialect) TableInfoQuery(tableName string) string {
	return "PRAGMA table_info(" + tableName + ")"
}

func (d *SqliteDialect) SchemaInitQueries() []string {
	return []string{
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
		`CREATE TABLE IF NOT EXISTS inference_samples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			prompt_tokens INTEGER NOT NULL,
			completion_tokens INTEGER NOT NULL,
			duration_us INTEGER NOT NULL,
			recorded_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS cache_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			is_hit INTEGER NOT NULL,
			recorded_at INTEGER NOT NULL
		);`,
	}
}

func (d *SqliteDialect) UpsertNodeStateQuery() string {
	return `INSERT INTO node_states (task_id, node_id, status, output, completed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(task_id, node_id) DO UPDATE SET
		status=excluded.status,
		output=excluded.output,
		completed_at=excluded.completed_at`
}

func (d *SqliteDialect) UpsertNotificationQuery() string {
	return `INSERT OR REPLACE INTO durable_notifications 
		(id, source, type, title, message, task_id, workflow_id, target_id, status, action_payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func (d *SqliteDialect) InsertMemoryQuery() string {
	return `INSERT OR IGNORE INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
}
