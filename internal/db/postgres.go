package db

import (
	"fmt"
)

// PostgresDialect implements DialectAdapter for PostgreSQL
type PostgresDialect struct{}

func (d *PostgresDialect) DriverName() string {
	return "postgres"
}

func (d *PostgresDialect) DataSourceName(cfg ConnectionConfig) string {
	if cfg.ConnectionURL != "" {
		return cfg.ConnectionURL
	}
	sslMode := "disable"
	if cfg.SslEnabled {
		sslMode = "require"
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s %s",
		cfg.Host, cfg.Port, cfg.Username, cfg.Password, cfg.DatabaseName, sslMode, cfg.ExtraOptions)
}

func (d *PostgresDialect) TableInfoQuery(tableName string) string {
	return fmt.Sprintf(`SELECT column_name, data_type FROM information_schema.columns WHERE table_name = '%s'`, tableName)
}

func (d *PostgresDialect) SchemaInitQueries() []string {
	// PostgreSQL schemas
	return []string{
		`CREATE TABLE IF NOT EXISTS fact_memories (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			type TEXT,
			content TEXT,
			context TEXT,
			confidence REAL,
			source TEXT,
			created_at TIMESTAMP,
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
			completed_at BIGINT,
			PRIMARY KEY (task_id, node_id)
		);`,
		`CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY,
			name TEXT,
			trigger_description TEXT,
			sop_content TEXT,
			created_at BIGINT
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
			created_at BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS workflows (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			trigger_type TEXT NOT NULL,
			trigger_config TEXT,
			status TEXT CHECK(status IN ('active', 'paused')) NOT NULL DEFAULT 'active',
			next_run_at BIGINT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
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
			started_at BIGINT NOT NULL,
			completed_at BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_task_executions (
			workflow_execution_id TEXT NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
			task_template_id TEXT NOT NULL,
			task_execution_id TEXT,
			status TEXT CHECK(status IN ('pending', 'running', 'completed', 'failed')) NOT NULL,
			started_at BIGINT,
			completed_at BIGINT,
			PRIMARY KEY (workflow_execution_id, task_template_id)
		);`,
		`CREATE TABLE IF NOT EXISTS openapi_integrations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			openapi_spec TEXT NOT NULL,
			auth_type TEXT NOT NULL,
			auth_key TEXT,
			auth_value TEXT,
			created_at BIGINT NOT NULL
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
			created_at BIGINT NOT NULL
		);`,
	}
}

func (d *PostgresDialect) UpsertNodeStateQuery() string {
	return `INSERT INTO node_states (task_id, node_id, status, output, completed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (task_id, node_id) DO UPDATE 
		SET status = EXCLUDED.status, output = EXCLUDED.output, completed_at = EXCLUDED.completed_at`
}

func (d *PostgresDialect) UpsertNotificationQuery() string {
	return `INSERT INTO durable_notifications 
		(id, source, type, title, message, task_id, workflow_id, target_id, status, action_payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE 
		SET source = EXCLUDED.source, type = EXCLUDED.type, title = EXCLUDED.title, message = EXCLUDED.message, 
		    task_id = EXCLUDED.task_id, workflow_id = EXCLUDED.workflow_id, target_id = EXCLUDED.target_id, 
		    status = EXCLUDED.status, action_payload = EXCLUDED.action_payload, created_at = EXCLUDED.created_at`
}

func (d *PostgresDialect) InsertMemoryQuery() string {
	return `INSERT INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at, embedding)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`
}
